package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/your-org/adms/internal/httpx"
)

// Roles in increasing order of privilege.
const (
	RoleViewer = "viewer"
	RoleHR     = "hr"
	RoleAdmin  = "admin"
)

var rank = map[string]int{RoleViewer: 1, RoleHR: 2, RoleAdmin: 3}

// ValidRole reports whether r is a known role.
func ValidRole(r string) bool { _, ok := rank[r]; return ok }

// User is the authenticated principal attached to a request.
type User struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
	Role  string `json:"role"`
}

// Can reports whether u holds at least role min.
func (u *User) Can(min string) bool { return u != nil && rank[u.Role] >= rank[min] }

type ctxKey struct{}

// FromContext returns the authenticated user, or nil.
func FromContext(ctx context.Context) *User {
	u, _ := ctx.Value(ctxKey{}).(*User)
	return u
}

// WithUser attaches u to ctx (exported for tests).
func WithUser(ctx context.Context, u *User) context.Context {
	return context.WithValue(ctx, ctxKey{}, u)
}

const CookieName = "adms_session"

// CSRFHeader must be present on every state-changing request. Browsers cannot
// attach custom headers cross-origin without a CORS preflight, which this API
// never grants, so its presence proves a same-origin caller.
const CSRFHeader = "X-ADMS-CSRF"

// Service manages sessions.
type Service struct {
	DB           *sql.DB
	TTL          time.Duration
	SecureCookie bool
	TrustProxy   bool
	LoginLimiter *httpx.RateLimiter
}

var ErrInvalidCredentials = errors.New("email or password is incorrect")

func hashToken(t string) []byte { h := sha256.Sum256([]byte(t)); return h[:] }

// Login verifies credentials and creates a session; it sets the cookie on w.
func (s *Service) Login(ctx context.Context, w http.ResponseWriter, r *http.Request, email, password string) (*User, error) {
	var u User
	var hash sql.NullString
	err := s.DB.QueryRowContext(ctx, `SELECT id, name, email, role, password_hash FROM users
		WHERE lower(email)=lower($1) AND active`, strings.TrimSpace(email)).
		Scan(&u.ID, &u.Name, &u.Email, &u.Role, &hash)
	if errors.Is(err, sql.ErrNoRows) || !hash.Valid {
		VerifyPassword(dummyHash, password)
		return nil, ErrInvalidCredentials
	}
	if err != nil {
		return nil, err
	}
	if !VerifyPassword(hash.String, password) {
		return nil, ErrInvalidCredentials
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil, err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	expires := time.Now().Add(s.TTL)
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO sessions (user_id, token_hash, source_ip, user_agent, expires_at)
		VALUES ($1,$2,$3::inet,$4,$5)`, u.ID, hashToken(token), httpx.ClientIP(r, s.TrustProxy),
		truncate(r.UserAgent(), 256), expires); err != nil {
		return nil, err
	}
	s.DB.ExecContext(ctx, `UPDATE users SET last_login_at=now() WHERE id=$1`, u.ID)
	s.DB.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at < now()`)
	http.SetCookie(w, &http.Cookie{Name: CookieName, Value: token, Path: "/", Expires: expires,
		HttpOnly: true, Secure: s.SecureCookie, SameSite: http.SameSiteStrictMode})
	return &u, nil
}

// Logout deletes the current session and clears the cookie.
func (s *Service) Logout(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(CookieName); err == nil {
		s.DB.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash=$1`, hashToken(c.Value))
	}
	http.SetCookie(w, &http.Cookie{Name: CookieName, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: s.SecureCookie, SameSite: http.SameSiteStrictMode})
}

func (s *Service) lookup(ctx context.Context, r *http.Request) (*User, error) {
	c, err := r.Cookie(CookieName)
	if err != nil || c.Value == "" {
		return nil, nil
	}
	var u User
	err = s.DB.QueryRowContext(ctx, `SELECT u.id, u.name, u.email, u.role
		FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token_hash=$1 AND s.expires_at > now() AND u.active`, hashToken(c.Value)).
		Scan(&u.ID, &u.Name, &u.Email, &u.Role)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &u, err
}

// Require wraps h so it only runs for an authenticated user holding at least
// role min. State-changing methods must also carry the CSRF header.
func (s *Service) Require(min string, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, err := s.lookup(r.Context(), r)
		if err != nil {
			httpx.Error(w, 503, "unavailable", "session store unavailable")
			return
		}
		if u == nil {
			httpx.Error(w, 401, "unauthenticated", "sign in to continue")
			return
		}
		if !u.Can(min) {
			httpx.Error(w, 403, "forbidden", "your role does not allow this action")
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Header.Get(CSRFHeader) != "1" {
			httpx.Error(w, 403, "csrf", "missing "+CSRFHeader+" header")
			return
		}
		h(w, r.WithContext(WithUser(r.Context(), u)))
	}
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
