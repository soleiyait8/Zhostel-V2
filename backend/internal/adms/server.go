// Package adms is the device-facing ingestion service. It speaks the
// protocol defined in internal/protocol and is deployed separately from the
// application API so punches keep flowing when nobody is logged in.
//
// Reliability rules enforced here:
//   - An upload is acknowledged only after it is committed. If the database is
//     down the device gets a 503, keeps the records and retries later.
//   - Re-sent records are recognised by payload hash and acknowledged without
//     being stored twice.
//   - Unknown or disabled devices are never acknowledged, so their records stay
//     on the device and are re-sent once an administrator registers it.
//   - Biometric templates and photos are never persisted.
package adms

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/lib/pq"

	"github.com/your-org/adms/internal/httpx"
	"github.com/your-org/adms/internal/protocol"
)

// Server is the ADMS HTTP handler.
type Server struct {
	DB              *sql.DB
	Profile         protocol.Profile
	Log             *slog.Logger
	MaxBody         int64
	TrustProxy      bool
	EnableCommands  bool
	LogHeartbeats   bool
	FutureTolerance time.Duration
	Limiter         *httpx.RateLimiter
	Now             func() time.Time
}

type device struct {
	ID       string
	Enabled  bool
	TZ       string
	CIDRs    []string
	AttStamp sql.NullString
	OpStamp  sql.NullString
	loc      *time.Location
}

// exchange accumulates what happened during one request for the
// communication log.
type exchange struct {
	start     time.Time
	ip        string
	serial    string
	deviceID  string
	table     string
	body      []byte
	status    int
	response  string
	outcome   string
	errMsg    string
	commLogID int64 // set when the log row was written inside the ingest transaction
	skipDBLog bool
	req       *http.Request
}

// Handler returns the routes for the ADMS service. Only /iclock/* and
// /healthz exist; nothing here requires or accepts browser sessions.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("/iclock/", s.iclock)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	})
	return mux
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.DB.PingContext(ctx); err != nil {
		httpx.JSON(w, 503, map[string]string{"status": "degraded", "database": "unreachable"})
		return
	}
	httpx.JSON(w, 200, map[string]any{"status": "ok", "profile": s.Profile.Name, "profile_verified": s.Profile.Verified})
}

func (s *Server) reply(w http.ResponseWriter, ex *exchange, status int, body, outcome string) {
	ex.status, ex.response, ex.outcome = status, body, outcome
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	io.WriteString(w, body)
}

func (s *Server) iclock(w http.ResponseWriter, r *http.Request) {
	ex := &exchange{start: s.Now(), ip: httpx.ClientIP(r, s.TrustProxy), req: r}
	if !s.Limiter.Allow(ex.ip) {
		s.Log.Warn("adms rate limited", "ip", ex.ip)
		s.reply(w, ex, http.StatusTooManyRequests, "rate limited", "rate_limited")
		return
	}
	defer s.finish(r, ex)

	q := r.URL.Query()
	ex.serial = q.Get("SN")
	ex.table = strings.ToUpper(q.Get("table"))
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, s.MaxBody))
	if err != nil {
		ex.skipDBLog = true
		s.reply(w, ex, http.StatusRequestEntityTooLarge, "payload too large", "payload_too_large")
		return
	}
	ex.body = body

	kind := protocol.Classify(r.Method, r.URL.Path)
	if err := protocol.ValidateSerial(ex.serial); err != nil {
		ex.skipDBLog = true // never write unauthenticated garbage to the database
		s.reply(w, ex, http.StatusBadRequest, "invalid SN", "invalid_serial")
		return
	}

	ctx := r.Context()
	dev, err := s.lookupDevice(ctx, ex.serial)
	if err != nil {
		ex.skipDBLog = true
		ex.errMsg = err.Error()
		s.reply(w, ex, http.StatusServiceUnavailable, "server unavailable", "db_error")
		return
	}
	if dev == nil {
		ex.skipDBLog = true
		s.syncError(ctx, "", ex.serial, "unknown_device",
			"A device that is not registered contacted the server.",
			map[string]any{"ip": ex.ip, "path": r.URL.Path, "query": r.URL.RawQuery})
		s.reply(w, ex, http.StatusForbidden, "device not registered", "unknown_device")
		return
	}
	ex.deviceID = dev.ID
	if !dev.Enabled {
		s.syncError(ctx, dev.ID, ex.serial, "device_disabled", "A disabled device contacted the server.", map[string]any{"ip": ex.ip})
		s.reply(w, ex, http.StatusForbidden, "device disabled", "device_disabled")
		return
	}
	if !ipAllowed(ex.ip, dev.CIDRs) {
		s.syncError(ctx, dev.ID, ex.serial, "source_ip_not_allowed",
			"Device connected from an address outside its allowed networks.", map[string]any{"ip": ex.ip})
		s.reply(w, ex, http.StatusForbidden, "source not allowed", "source_ip_not_allowed")
		return
	}
	s.touch(ctx, dev.ID, ex.ip, q.Get("pushver"), protocol.FirmwareFromInfo(q))

	switch kind {
	case protocol.KindHandshake:
		s.reply(w, ex, 200, s.Profile.HandshakeResponse(ex.serial, dev.AttStamp.String, dev.OpStamp.String), "handshake")
	case protocol.KindGetRequest:
		s.getRequest(ctx, w, ex, dev)
	case protocol.KindDeviceCmd:
		s.deviceCmd(ctx, w, ex, dev)
	case protocol.KindUpload:
		s.upload(ctx, w, ex, dev, q.Get("Stamp"))
	default:
		s.syncError(ctx, dev.ID, ex.serial, "unknown_endpoint",
			"Device called an endpoint this adapter does not implement.",
			map[string]any{"method": r.Method, "path": r.URL.Path})
		s.reply(w, ex, http.StatusNotFound, "unsupported", "unknown_endpoint")
	}
}

var stampRe = regexp.MustCompile(`^[0-9]{1,20}$`)

func (s *Server) upload(ctx context.Context, w http.ResponseWriter, ex *exchange, dev *device, stamp string) {
	if !stampRe.MatchString(stamp) {
		stamp = ""
	}
	switch ex.table {
	case "ATTLOG":
		n, err := s.ingestAttLog(ctx, ex, dev, stamp)
		if err != nil {
			ex.errMsg = err.Error()
			s.Log.Error("attlog ingest failed; not acknowledging", "serial", ex.serial, "err", err)
			s.reply(w, ex, http.StatusServiceUnavailable, "server unavailable", "db_error")
			return
		}
		s.reply(w, ex, 200, s.Profile.UploadAck(n), "attlog_stored")
	case "OPERLOG", "USERINFO":
		users := protocol.ParseUserLines(ex.body)
		if err := s.storeUsers(ctx, dev.ID, users, stamp); err != nil {
			ex.errMsg = err.Error()
			s.reply(w, ex, http.StatusServiceUnavailable, "server unavailable", "db_error")
			return
		}
		s.reply(w, ex, 200, s.Profile.UploadAck(countLines(ex.body)), "operlog_stored")
	default:
		// Photos, templates and tables this adapter does not use are
		// acknowledged so the device does not resend them forever, but their
		// content is never stored.
		s.reply(w, ex, 200, s.Profile.UploadAck(countLines(ex.body)), "table_ignored")
	}
}

func countLines(b []byte) int {
	n := 0
	for _, l := range strings.Split(protocol.NormalizeBody(b), "\n") {
		if strings.TrimSpace(l) != "" {
			n++
		}
	}
	return n
}

// IngestResult summarises one ATTLOG batch (exported for tests).
type IngestResult struct{ Accepted, Rejected, Duplicates int }

func (s *Server) ingestAttLog(ctx context.Context, ex *exchange, dev *device, stamp string) (int, error) {
	results := protocol.ParseAttLog(ex.body)
	ack := s.Profile.UploadAck(len(results))

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	var commID int64
	if err := tx.QueryRowContext(ctx, commLogInsert, append(s.commLogArgs(ex, 200, ack, "attlog_stored"), dev.ID)...).Scan(&commID); err != nil {
		return 0, fmt.Errorf("comm log: %w", err)
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO raw_attendance_logs
		(device_id, device_serial_number, device_user_id, device_timestamp, punch_at,
		 event_type, verification_method, work_code, original_payload, payload_hash,
		 parse_status, rejection_reason, comm_log_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
		ON CONFLICT (device_serial_number, payload_hash) DO NOTHING`)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()

	var res IngestResult
	reasons := map[string]int{}
	limit := s.Now().Add(s.FutureTolerance)
	for _, lr := range results {
		var userID, wall, punch, status, verify, work, reason any
		parse := "accepted"
		if lr.Log != nil {
			l := lr.Log
			userID, status, verify, work = l.UserID, nz(l.Status), nz(l.Verify), nz(l.WorkCode)
			wall = l.Wall.Format("2006-01-02 15:04:05")
			p := protocol.Instant(l.Wall, dev.loc)
			punch = p
			if p.After(limit) {
				parse, reason = "rejected", "timestamp_in_future"
			}
		} else {
			parse, reason = "rejected", lr.Reason
		}
		r, err := stmt.ExecContext(ctx, dev.ID, ex.serial, userID, wall, punch, status, verify, work,
			lr.Raw, protocol.PayloadHash(ex.serial, lr.Raw), parse, reason, commID)
		if err != nil {
			return 0, err
		}
		if n, _ := r.RowsAffected(); n == 0 {
			res.Duplicates++
		} else if parse == "accepted" {
			res.Accepted++
		} else {
			res.Rejected++
			reasons[reason.(string)]++
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE biometric_devices SET
		attlog_stamp = coalesce($2, attlog_stamp),
		last_transaction_at = CASE WHEN $3 THEN now() ELSE last_transaction_at END
		WHERE id=$1`, dev.ID, nz(stamp), res.Accepted > 0); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	ex.commLogID = commID
	if res.Rejected > 0 {
		s.syncError(ctx, dev.ID, ex.serial, "rejected_records",
			fmt.Sprintf("%d attendance record(s) could not be accepted and are held for review.", res.Rejected),
			map[string]any{"reasons": reasons, "comm_log_id": commID})
	}
	s.Log.Info("attlog batch", "serial", ex.serial, "accepted", res.Accepted,
		"rejected", res.Rejected, "duplicates", res.Duplicates)
	return len(results), nil
}

func (s *Server) storeUsers(ctx context.Context, deviceID string, users []protocol.DeviceUser, stamp string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, u := range users {
		if _, err := tx.ExecContext(ctx, `INSERT INTO device_users (device_id, device_user_id, name, privilege)
			VALUES ($1,$2,$3,$4)
			ON CONFLICT (device_id, device_user_id) DO UPDATE
			SET name=EXCLUDED.name, privilege=EXCLUDED.privilege, last_seen_at=now()`,
			deviceID, u.UserID, u.Name, u.Privilege); err != nil {
			return err
		}
	}
	if stamp != "" {
		if _, err := tx.ExecContext(ctx, `UPDATE biometric_devices SET operlog_stamp=$2 WHERE id=$1`, deviceID, stamp); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Server) getRequest(ctx context.Context, w http.ResponseWriter, ex *exchange, dev *device) {
	if !s.EnableCommands {
		if !s.LogHeartbeats {
			ex.skipDBLog = true
		}
		s.reply(w, ex, 200, s.Profile.IdleResponse(), "heartbeat")
		return
	}
	var id int64
	var cmd string
	err := s.DB.QueryRowContext(ctx, `UPDATE device_commands SET status='sent', sent_at=now()
		WHERE id = (SELECT id FROM device_commands WHERE device_id=$1 AND status='pending'
		            ORDER BY id LIMIT 1 FOR UPDATE SKIP LOCKED)
		RETURNING id, command`, dev.ID).Scan(&id, &cmd)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if !s.LogHeartbeats {
			ex.skipDBLog = true
		}
		s.reply(w, ex, 200, s.Profile.IdleResponse(), "heartbeat")
	case err != nil:
		ex.errMsg = err.Error()
		s.reply(w, ex, 200, s.Profile.IdleResponse(), "heartbeat_db_error")
	default:
		s.reply(w, ex, 200, s.Profile.CommandResponse(id, cmd), "command_sent")
	}
}

func (s *Server) deviceCmd(ctx context.Context, w http.ResponseWriter, ex *exchange, dev *device) {
	for _, res := range protocol.ParseCommandResults(ex.body) {
		status := "failed"
		if res.Return == "0" {
			status = "succeeded"
		}
		s.DB.ExecContext(ctx, `UPDATE device_commands SET status=$3, return_code=$4, result=$5, completed_at=now()
			WHERE id=$1 AND device_id=$2 AND status='sent'`, res.ID, dev.ID, status, res.Return, res.Cmd)
	}
	s.reply(w, ex, 200, "OK", "command_result")
}

func (s *Server) lookupDevice(ctx context.Context, serial string) (*device, error) {
	var d device
	err := s.DB.QueryRowContext(ctx, `SELECT id, enabled, timezone, allowed_cidrs::text[], attlog_stamp, operlog_stamp
		FROM biometric_devices WHERE serial_number=$1`, serial).
		Scan(&d.ID, &d.Enabled, &d.TZ, pq.Array(&d.CIDRs), &d.AttStamp, &d.OpStamp)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if d.loc, err = time.LoadLocation(d.TZ); err != nil {
		return nil, fmt.Errorf("device %s has invalid timezone %q", serial, d.TZ)
	}
	return &d, nil
}

func (s *Server) touch(ctx context.Context, id, ip, pushver, firmware string) {
	_, err := s.DB.ExecContext(ctx, `UPDATE biometric_devices SET last_seen_at=now(), last_ip=$2::inet,
		connection_status='online',
		push_version=coalesce($3, push_version), firmware_version=coalesce($4, firmware_version)
		WHERE id=$1`, id, nz(ip), nz(trunc(pushver, 32)), nz(firmware))
	if err != nil {
		s.Log.Warn("touch device failed", "err", err)
	}
}

func (s *Server) syncError(ctx context.Context, deviceID, serial, typ, msg string, details map[string]any) {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO sync_error_logs (device_id, serial_number, error_type, message, details)
		VALUES ($1,$2,$3,$4,$5::jsonb)
		ON CONFLICT (coalesce(serial_number,''), error_type) WHERE resolved_at IS NULL
		DO UPDATE SET occurrences = sync_error_logs.occurrences + 1, last_seen_at = now(),
		              message = EXCLUDED.message, details = EXCLUDED.details,
		              device_id = coalesce(EXCLUDED.device_id, sync_error_logs.device_id)`,
		nz(deviceID), serial, typ, msg, toJSON(details))
	if err != nil {
		s.Log.Warn("sync error log failed", "err", err)
	}
}

const commLogInsert = `INSERT INTO device_communication_logs
	(serial_number, method, path, query, content_type, body_size, body, response_status,
	 response_body, remote_ip, outcome, error, duration_ms, device_id)
	VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10::inet,$11,$12,$13,$14) RETURNING id`

func (s *Server) commLogArgs(ex *exchange, status int, resp, outcome string) []any {
	r := ex.req
	return []any{ex.serial, r.Method, r.URL.Path, trunc(r.URL.RawQuery, 1024),
		trunc(r.Header.Get("Content-Type"), 128), len(ex.body),
		protocol.Redact(ex.table, ex.body, 64*1024), status, trunc(resp, 2048),
		nz(ex.ip), outcome, trunc(ex.errMsg, 1024), int(s.Now().Sub(ex.start).Milliseconds())}
}

// finish writes the communication log after the response has been sent.
func (s *Server) finish(r *http.Request, ex *exchange) {
	s.Log.Info("adms request", "serial", ex.serial, "path", r.URL.Path, "table", ex.table,
		"status", ex.status, "outcome", ex.outcome, "ip", ex.ip)
	if ex.skipDBLog || ex.commLogID != 0 || ex.deviceID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var id int64
	args := append(s.commLogArgs(ex, ex.status, ex.response, ex.outcome), ex.deviceID)
	if err := s.DB.QueryRowContext(ctx, commLogInsert, args...).Scan(&id); err != nil {
		s.Log.Warn("comm log write failed", "err", err)
	}
}

func ipAllowed(ip string, cidrs []string) bool {
	if len(cidrs) == 0 {
		return true
	}
	addr := net.ParseIP(ip)
	for _, c := range cidrs {
		if _, n, err := net.ParseCIDR(c); err == nil && addr != nil && n.Contains(addr) {
			return true
		}
	}
	return false
}

func nz(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func trunc(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func toJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}
