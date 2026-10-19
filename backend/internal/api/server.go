// Package api is the authenticated application API used by the HR dashboard.
// It never serves device protocol routes; those live in internal/adms.
package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/lib/pq"

	"github.com/your-org/adms/internal/audit"
	"github.com/your-org/adms/internal/auth"
	"github.com/your-org/adms/internal/httpx"
)

// App holds API dependencies.
type App struct {
	DB         *sql.DB
	Auth       *auth.Service
	Log        *slog.Logger
	TrustProxy bool
	// Where the ADMS service can be reached from inside the deployment,
	// used by the reachability check.
	ADMSInternalURL string
	// Address devices are told to use, shown in setup instructions.
	ADMSPublicHost  string
	ADMSPublicPort  int
	ADMSPublicHTTPS bool
	HTTPClient      *http.Client
}

// Routes registers every application endpoint.
func (a *App) Routes() http.Handler {
	m := http.NewServeMux()
	V, H, A := auth.RoleViewer, auth.RoleHR, auth.RoleAdmin
	req := a.Auth.Require

	m.HandleFunc("GET /healthz", a.health)
	m.HandleFunc("POST /api/auth/login", a.login)
	m.HandleFunc("POST /api/auth/logout", req(V, a.logout))
	m.HandleFunc("GET /api/auth/me", req(V, a.me))
	m.HandleFunc("POST /api/auth/password", req(V, a.changePassword))

	m.HandleFunc("GET /api/users", req(A, a.listUsers))
	m.HandleFunc("POST /api/users", req(A, a.createUser))
	m.HandleFunc("PUT /api/users/{id}", req(A, a.updateUser))

	m.HandleFunc("GET /api/departments", req(V, a.listDepartments))
	m.HandleFunc("POST /api/departments", req(H, a.createDepartment))
	m.HandleFunc("PUT /api/departments/{id}", req(H, a.updateDepartment))

	m.HandleFunc("GET /api/employees", req(V, a.listEmployees))
	m.HandleFunc("POST /api/employees", req(H, a.createEmployee))
	m.HandleFunc("GET /api/employees/export.csv", req(V, a.exportEmployees))
	m.HandleFunc("POST /api/employees/import", req(H, a.importEmployees))
	m.HandleFunc("GET /api/employees/{id}", req(V, a.getEmployee))
	m.HandleFunc("PUT /api/employees/{id}", req(H, a.updateEmployee))
	m.HandleFunc("POST /api/employees/{id}/deactivate", req(H, a.deactivateEmployee))
	m.HandleFunc("GET /api/employees/{id}/mappings", req(V, a.listMappings))
	m.HandleFunc("POST /api/employees/{id}/mappings", req(H, a.createMapping))
	m.HandleFunc("POST /api/mappings/{id}/end", req(H, a.endMapping))

	m.HandleFunc("GET /api/devices", req(V, a.listDevices))
	m.HandleFunc("POST /api/devices", req(A, a.createDevice))
	m.HandleFunc("GET /api/devices/setup-info", req(V, a.setupInfo))
	m.HandleFunc("GET /api/devices/{id}", req(V, a.getDevice))
	m.HandleFunc("PUT /api/devices/{id}", req(A, a.updateDevice))
	m.HandleFunc("GET /api/devices/{id}/status", req(V, a.deviceStatus))
	m.HandleFunc("GET /api/devices/{id}/communications", req(H, a.deviceComms))
	m.HandleFunc("GET /api/devices/{id}/users", req(V, a.deviceUsers))
	m.HandleFunc("POST /api/devices/{id}/reprocess", req(A, a.reprocess))
	m.HandleFunc("GET /api/devices/{id}/reachability", req(V, a.reachability))

	m.HandleFunc("GET /api/sync-errors", req(V, a.listSyncErrors))
	m.HandleFunc("POST /api/sync-errors/{id}/resolve", req(H, a.resolveSyncError))
	m.HandleFunc("GET /api/punches", req(V, a.listPunches))
	m.HandleFunc("GET /api/unmatched-users", req(V, a.unmatchedUsers))
	m.HandleFunc("GET /api/dashboard", req(V, a.dashboard))
	m.HandleFunc("GET /api/audit-logs", req(H, a.listAudit))

	m.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		httpx.Error(w, 404, "not_found", "no such endpoint")
	})
	return httpx.SecurityHeaders(m)
}

// ------------------------------------------------------------------ helpers

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func validUUID(s string) bool { return uuidRe.MatchString(s) }

// pathID returns the {id} path value or writes a 404.
func pathID(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := r.PathValue("id")
	if !validUUID(id) {
		httpx.Error(w, 404, "not_found", "not found")
		return "", false
	}
	return id, true
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(io.LimitReader(r.Body, 64<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		httpx.Error(w, 400, "invalid_json", "request body is not valid: "+err.Error())
		return false
	}
	return true
}

// Problems collects field validation errors.
type Problems map[string]string

func (p Problems) add(field, msg string) { p[field] = msg }

func (p Problems) write(w http.ResponseWriter) bool {
	if len(p) == 0 {
		return false
	}
	httpx.JSON(w, 422, map[string]any{"error": map[string]any{
		"code": "validation_failed", "message": "some fields need attention", "fields": p}})
	return true
}

func (a *App) ip(r *http.Request) string { return httpx.ClientIP(r, a.TrustProxy) }

func actor(r *http.Request) string { return auth.FromContext(r.Context()).ID }

func (a *App) entry(r *http.Request, action, typ, id string, before, after any) audit.Entry {
	return audit.Entry{Actor: actor(r), Action: action, EntityType: typ, EntityID: id,
		Before: before, After: after, SourceIP: a.ip(r)}
}

func (a *App) fail(w http.ResponseWriter, err error) {
	if errors.Is(err, sql.ErrNoRows) {
		httpx.Error(w, 404, "not_found", "not found")
		return
	}
	var pe *pq.Error
	if errors.As(err, &pe) {
		switch pe.Code {
		case "23505":
			httpx.Error(w, 409, "conflict", "a record with the same unique value already exists")
			return
		case "23503":
			httpx.Error(w, 422, "invalid_reference", "a referenced record does not exist")
			return
		case "23P01":
			httpx.Error(w, 409, "overlap", "this overlaps an existing record for the same period")
			return
		case "23514":
			httpx.Error(w, 422, "constraint", "a value is outside the allowed range")
			return
		}
	}
	a.Log.Error("api error", "err", err)
	httpx.Error(w, 500, "internal", "something went wrong; the error has been logged")
}

func limitOffset(r *http.Request, def, max int) (int, int) {
	l, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	o, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if l <= 0 {
		l = def
	}
	if l > max {
		l = max
	}
	if o < 0 {
		o = 0
	}
	return l, o
}

func strPtr(ns sql.NullString) *string {
	if !ns.Valid {
		return nil
	}
	return &ns.String
}

func timePtr(nt sql.NullTime) *time.Time {
	if !nt.Valid {
		return nil
	}
	return &nt.Time
}

func nullStr(p *string) any {
	if p == nil || strings.TrimSpace(*p) == "" {
		return nil
	}
	return strings.TrimSpace(*p)
}

func (a *App) businessTZ(ctx context.Context) *time.Location {
	var name string
	if err := a.DB.QueryRowContext(ctx, `SELECT value #>> '{}' FROM app_config WHERE key='business_timezone'`).Scan(&name); err == nil {
		if loc, err := time.LoadLocation(name); err == nil {
			return loc
		}
	}
	loc, _ := time.LoadLocation("Asia/Manila")
	return loc
}

func (a *App) onlineWindow(ctx context.Context) int {
	var n int
	if err := a.DB.QueryRowContext(ctx, `SELECT (value #>> '{}')::int FROM app_config WHERE key='device_online_window_seconds'`).Scan(&n); err != nil || n <= 0 {
		return 300
	}
	return n
}

// ------------------------------------------------------------------ health & auth

func (a *App) health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := a.DB.PingContext(ctx); err != nil {
		httpx.JSON(w, 503, map[string]string{"status": "degraded"})
		return
	}
	httpx.JSON(w, 200, map[string]string{"status": "ok"})
}

func (a *App) login(w http.ResponseWriter, r *http.Request) {
	ip := a.ip(r)
	if !a.Auth.LoginLimiter.Allow(ip) {
		httpx.Error(w, 429, "rate_limited", "too many sign-in attempts; wait 15 minutes and try again")
		return
	}
	var in struct{ Email, Password string }
	if !decode(w, r, &in) {
		return
	}
	u, err := a.Auth.Login(r.Context(), w, r, in.Email, in.Password)
	if errors.Is(err, auth.ErrInvalidCredentials) {
		audit.Write(r.Context(), a.DB, audit.Entry{Action: "login_failed", EntityType: "user",
			After: map[string]string{"email": in.Email}, SourceIP: ip})
		httpx.Error(w, 401, "invalid_credentials", err.Error())
		return
	}
	if err != nil {
		a.fail(w, err)
		return
	}
	a.Auth.LoginLimiter.Reset(ip)
	audit.Write(r.Context(), a.DB, audit.Entry{Actor: u.ID, Action: "login", EntityType: "user", EntityID: u.ID, SourceIP: ip})
	httpx.JSON(w, 200, u)
}

func (a *App) logout(w http.ResponseWriter, r *http.Request) {
	a.Auth.Logout(r.Context(), w, r)
	w.WriteHeader(204)
}

func (a *App) me(w http.ResponseWriter, r *http.Request) {
	httpx.JSON(w, 200, auth.FromContext(r.Context()))
}

func (a *App) changePassword(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Current string `json:"current_password"`
		New     string `json:"new_password"`
	}
	if !decode(w, r, &in) {
		return
	}
	if err := auth.CheckPolicy(in.New); err != nil {
		httpx.Error(w, 422, "weak_password", err.Error())
		return
	}
	u := auth.FromContext(r.Context())
	var hash sql.NullString
	if err := a.DB.QueryRowContext(r.Context(), `SELECT password_hash FROM users WHERE id=$1`, u.ID).Scan(&hash); err != nil {
		a.fail(w, err)
		return
	}
	if !hash.Valid || !auth.VerifyPassword(hash.String, in.Current) {
		httpx.Error(w, 401, "invalid_credentials", "current password is incorrect")
		return
	}
	nh, err := auth.HashPassword(in.New)
	if err != nil {
		a.fail(w, err)
		return
	}
	tx, ok := a.begin(w, r)
	if !ok {
		return
	}
	defer tx.Rollback()
	tx.ExecContext(r.Context(), `UPDATE users SET password_hash=$2, updated_at=now() WHERE id=$1`, u.ID, nh)
	tx.ExecContext(r.Context(), `DELETE FROM sessions WHERE user_id=$1`, u.ID) // sign out everywhere
	audit.Write(r.Context(), tx, a.entry(r, "password_changed", "user", u.ID, nil, nil))
	if err := tx.Commit(); err != nil {
		a.fail(w, err)
		return
	}
	a.Auth.Logout(r.Context(), w, r)
	w.WriteHeader(204)
}

// ------------------------------------------------------------------ users

type userOut struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Email       string     `json:"email"`
	Role        string     `json:"role"`
	Active      bool       `json:"active"`
	LastLoginAt *time.Time `json:"last_login_at"`
	CreatedAt   time.Time  `json:"created_at"`
}

func (a *App) listUsers(w http.ResponseWriter, r *http.Request) {
	rows, err := a.DB.QueryContext(r.Context(), `SELECT id, name, email, role, active, last_login_at, created_at FROM users ORDER BY name`)
	if err != nil {
		a.fail(w, err)
		return
	}
	defer rows.Close()
	out := []userOut{}
	for rows.Next() {
		var u userOut
		var ll sql.NullTime
		rows.Scan(&u.ID, &u.Name, &u.Email, &u.Role, &u.Active, &ll, &u.CreatedAt)
		u.LastLoginAt = timePtr(ll)
		out = append(out, u)
	}
	httpx.JSON(w, 200, map[string]any{"items": out})
}

var emailRe = regexp.MustCompile(`^[^@\s]{1,64}@[^@\s]{1,190}\.[^@\s]{2,}$`)

func (a *App) createUser(w http.ResponseWriter, r *http.Request) {
	var in struct{ Name, Email, Role, Password string }
	if !decode(w, r, &in) {
		return
	}
	p := Problems{}
	in.Name, in.Email = strings.TrimSpace(in.Name), strings.TrimSpace(in.Email)
	if in.Name == "" || len(in.Name) > 100 {
		p.add("name", "enter a name up to 100 characters")
	}
	if !emailRe.MatchString(in.Email) {
		p.add("email", "enter a valid email address")
	}
	if !auth.ValidRole(in.Role) {
		p.add("role", "role must be admin, hr or viewer")
	}
	if auth.CheckPolicy(in.Password) != nil {
		p.add("password", auth.ErrWeakPassword.Error())
	}
	if p.write(w) {
		return
	}
	hash, err := auth.HashPassword(in.Password)
	if err != nil {
		a.fail(w, err)
		return
	}
	tx, ok := a.begin(w, r)
	if !ok {
		return
	}
	defer tx.Rollback()
	var id string
	if err := tx.QueryRowContext(r.Context(), `INSERT INTO users (name, email, role, password_hash) VALUES ($1,$2,$3,$4) RETURNING id`,
		in.Name, in.Email, in.Role, hash).Scan(&id); err != nil {
		a.fail(w, err)
		return
	}
	audit.Write(r.Context(), tx, a.entry(r, "create", "user", id, nil, map[string]string{"name": in.Name, "email": in.Email, "role": in.Role}))
	if err := tx.Commit(); err != nil {
		a.fail(w, err)
		return
	}
	httpx.JSON(w, 201, map[string]string{"id": id})
}

func (a *App) updateUser(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in struct {
		Name   *string `json:"name"`
		Role   *string `json:"role"`
		Active *bool   `json:"active"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Role != nil && !auth.ValidRole(*in.Role) {
		httpx.Error(w, 422, "validation_failed", "role must be admin, hr or viewer")
		return
	}
	if id == actor(r) && ((in.Active != nil && !*in.Active) || (in.Role != nil && *in.Role != auth.RoleAdmin)) {
		httpx.Error(w, 422, "self_lockout", "you cannot deactivate or demote your own account")
		return
	}
	tx, ok2 := a.begin(w, r)
	if !ok2 {
		return
	}
	defer tx.Rollback()
	var before userOut
	var ll sql.NullTime
	if err := tx.QueryRowContext(r.Context(), `SELECT id, name, email, role, active, last_login_at, created_at FROM users WHERE id=$1 FOR UPDATE`, id).
		Scan(&before.ID, &before.Name, &before.Email, &before.Role, &before.Active, &ll, &before.CreatedAt); err != nil {
		a.fail(w, err)
		return
	}
	after := before
	if in.Name != nil && strings.TrimSpace(*in.Name) != "" {
		after.Name = strings.TrimSpace(*in.Name)
	}
	if in.Role != nil {
		after.Role = *in.Role
	}
	if in.Active != nil {
		after.Active = *in.Active
	}
	if _, err := tx.ExecContext(r.Context(), `UPDATE users SET name=$2, role=$3, active=$4, updated_at=now() WHERE id=$1`,
		id, after.Name, after.Role, after.Active); err != nil {
		a.fail(w, err)
		return
	}
	if !after.Active || after.Role != before.Role {
		tx.ExecContext(r.Context(), `DELETE FROM sessions WHERE user_id=$1`, id)
	}
	audit.Write(r.Context(), tx, a.entry(r, "update", "user", id, before, after))
	if err := tx.Commit(); err != nil {
		a.fail(w, err)
		return
	}
	httpx.JSON(w, 200, after)
}
