package api

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/your-org/adms/internal/httpx"
)

// The join that answers "which employee did this device user ID belong to at
// the moment of the punch". Historical mappings are respected.
const mappingAtPunch = `LEFT JOIN employee_device_mappings m ON m.device_id=r.device_id
	AND m.device_user_id=r.device_user_id AND r.punch_at >= m.valid_from
	AND (m.valid_until IS NULL OR r.punch_at < m.valid_until)
	LEFT JOIN employees e ON e.id=m.employee_id`

// dateRange parses from/to (YYYY-MM-DD, inclusive) in the business timezone,
// defaulting to today, capped at 93 days.
func dateRange(r *http.Request, loc *time.Location) (time.Time, time.Time, error) {
	today := time.Now().In(loc)
	day := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, loc)
	from, to := day, day
	var err error
	if s := r.URL.Query().Get("from"); s != "" {
		if from, err = time.ParseInLocation("2006-01-02", s, loc); err != nil {
			return from, to, fmt.Errorf("from must be YYYY-MM-DD")
		}
		to = from
	}
	if s := r.URL.Query().Get("to"); s != "" {
		if to, err = time.ParseInLocation("2006-01-02", s, loc); err != nil {
			return from, to, fmt.Errorf("to must be YYYY-MM-DD")
		}
	}
	if to.Before(from) || to.Sub(from) > 93*24*time.Hour {
		return from, to, fmt.Errorf("choose a range of up to 93 days with from on or before to")
	}
	return from, to.AddDate(0, 0, 1), nil
}

type Punch struct {
	ID              string     `json:"id"`
	DeviceID        *string    `json:"device_id"`
	DeviceName      *string    `json:"device_name"`
	DeviceUserID    *string    `json:"device_user_id"`
	DeviceTimestamp *string    `json:"device_timestamp"`
	PunchAt         *time.Time `json:"punch_at"`
	ReceivedAt      time.Time  `json:"received_at"`
	EventType       *string    `json:"event_type"`
	Verification    *string    `json:"verification_method"`
	WorkCode        *string    `json:"work_code"`
	ParseStatus     string     `json:"parse_status"`
	RejectionReason *string    `json:"rejection_reason"`
	OriginalPayload string     `json:"original_payload"`
	EmployeeID      *string    `json:"employee_id"`
	EmployeeNumber  *string    `json:"employee_number"`
	EmployeeName    *string    `json:"employee_name"`
}

func (a *App) listPunches(w http.ResponseWriter, r *http.Request) {
	loc := a.businessTZ(r.Context())
	from, to, err := dateRange(r, loc)
	if err != nil {
		httpx.Error(w, 422, "validation_failed", err.Error())
		return
	}
	q := r.URL.Query()
	limit, offset := limitOffset(r, 100, 1000)
	args := []any{from, to}
	arg := func(v any) string { args = append(args, v); return fmt.Sprintf("$%d", len(args)) }
	where := []string{"coalesce(r.punch_at, r.received_at) >= $1", "coalesce(r.punch_at, r.received_at) < $2"}
	if v := q.Get("device_id"); validUUID(v) {
		where = append(where, "r.device_id="+arg(v))
	}
	if v := q.Get("employee_id"); validUUID(v) {
		where = append(where, "e.id="+arg(v))
	}
	if v := q.Get("department_id"); validUUID(v) {
		where = append(where, "e.department_id="+arg(v))
	}
	if v := q.Get("device_user_id"); v != "" {
		where = append(where, "r.device_user_id="+arg(v))
	}
	switch q.Get("status") {
	case "accepted", "rejected":
		where = append(where, "r.parse_status="+arg(q.Get("status")))
	case "unmatched":
		where = append(where, "r.parse_status='accepted' AND e.id IS NULL")
	}
	cond := strings.Join(where, " AND ")
	var total int
	if err := a.DB.QueryRowContext(r.Context(), `SELECT count(*) FROM raw_attendance_logs r `+mappingAtPunch+` WHERE `+cond, args...).Scan(&total); err != nil {
		a.fail(w, err)
		return
	}
	rows, err := a.DB.QueryContext(r.Context(), `SELECT r.id, r.device_id, d.device_name, r.device_user_id,
		to_char(r.device_timestamp,'YYYY-MM-DD HH24:MI:SS'), r.punch_at, r.received_at, r.event_type,
		r.verification_method, r.work_code, r.parse_status, r.rejection_reason, r.original_payload,
		e.id, e.employee_number, e.first_name||' '||e.last_name
		FROM raw_attendance_logs r LEFT JOIN biometric_devices d ON d.id=r.device_id `+mappingAtPunch+`
		WHERE `+cond+fmt.Sprintf(` ORDER BY coalesce(r.punch_at, r.received_at) DESC LIMIT %d OFFSET %d`, limit, offset), args...)
	if err != nil {
		a.fail(w, err)
		return
	}
	defer rows.Close()
	out := []Punch{}
	for rows.Next() {
		var p Punch
		var did, dn, uid, ts, et, vm, wc, rr, eid, en, ename sql.NullString
		var pa sql.NullTime
		if err := rows.Scan(&p.ID, &did, &dn, &uid, &ts, &pa, &p.ReceivedAt, &et, &vm, &wc, &p.ParseStatus, &rr,
			&p.OriginalPayload, &eid, &en, &ename); err != nil {
			a.fail(w, err)
			return
		}
		p.DeviceID, p.DeviceName, p.DeviceUserID, p.DeviceTimestamp = strPtr(did), strPtr(dn), strPtr(uid), strPtr(ts)
		p.PunchAt, p.EventType, p.Verification, p.WorkCode = timePtr(pa), strPtr(et), strPtr(vm), strPtr(wc)
		p.RejectionReason, p.EmployeeID, p.EmployeeNumber, p.EmployeeName = strPtr(rr), strPtr(eid), strPtr(en), strPtr(ename)
		out = append(out, p)
	}
	httpx.JSON(w, 200, map[string]any{"items": out, "total": total, "limit": limit, "offset": offset,
		"timezone": loc.String()})
}

func (a *App) unmatchedUsers(w http.ResponseWriter, r *http.Request) {
	rows, err := a.DB.QueryContext(r.Context(), `SELECT r.device_id, d.device_name, r.device_user_id, du.name,
		count(*), min(r.punch_at), max(r.punch_at)
		FROM raw_attendance_logs r JOIN biometric_devices d ON d.id=r.device_id
		LEFT JOIN device_users du ON du.device_id=r.device_id AND du.device_user_id=r.device_user_id
		`+mappingAtPunch+`
		WHERE r.parse_status='accepted' AND e.id IS NULL AND r.punch_at > now() - interval '62 days'
		GROUP BY r.device_id, d.device_name, r.device_user_id, du.name
		ORDER BY max(r.punch_at) DESC LIMIT 500`)
	if err != nil {
		a.fail(w, err)
		return
	}
	defer rows.Close()
	type um struct {
		DeviceID     string    `json:"device_id"`
		DeviceName   string    `json:"device_name"`
		DeviceUserID string    `json:"device_user_id"`
		NameOnDevice *string   `json:"name_on_device"`
		Punches      int       `json:"punches"`
		FirstPunch   time.Time `json:"first_punch_at"`
		LastPunch    time.Time `json:"last_punch_at"`
	}
	out := []um{}
	for rows.Next() {
		var u um
		var n sql.NullString
		rows.Scan(&u.DeviceID, &u.DeviceName, &u.DeviceUserID, &n, &u.Punches, &u.FirstPunch, &u.LastPunch)
		u.NameOnDevice = strPtr(n)
		out = append(out, u)
	}
	httpx.JSON(w, 200, map[string]any{"items": out})
}

func (a *App) dashboard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	loc := a.businessTZ(ctx)
	tz := loc.String()
	var active, present, unmatched, rejected int
	err := a.DB.QueryRowContext(ctx, `SELECT
		(SELECT count(*) FROM employees WHERE employment_status='active'),
		(SELECT count(DISTINCT e.id) FROM raw_attendance_logs r `+mappingAtPunch+`
		  WHERE r.parse_status='accepted' AND e.employment_status='active'
		  AND (r.punch_at AT TIME ZONE $1)::date = (now() AT TIME ZONE $1)::date),
		(SELECT count(*) FROM raw_attendance_logs r `+mappingAtPunch+`
		  WHERE r.parse_status='accepted' AND e.id IS NULL
		  AND (r.punch_at AT TIME ZONE $1)::date = (now() AT TIME ZONE $1)::date),
		(SELECT count(*) FROM raw_attendance_logs r WHERE r.parse_status='rejected'
		  AND (r.received_at AT TIME ZONE $1)::date = (now() AT TIME ZONE $1)::date)`, tz).
		Scan(&active, &present, &unmatched, &rejected)
	if err != nil {
		a.fail(w, err)
		return
	}
	hours := make([]int, 24)
	rows, err := a.DB.QueryContext(ctx, `SELECT extract(hour FROM r.punch_at AT TIME ZONE $1)::int, count(*)
		FROM raw_attendance_logs r WHERE r.parse_status='accepted'
		AND (r.punch_at AT TIME ZONE $1)::date = (now() AT TIME ZONE $1)::date GROUP BY 1`, tz)
	if err == nil {
		for rows.Next() {
			var h, n int
			rows.Scan(&h, &n)
			if h >= 0 && h < 24 {
				hours[h] = n
			}
		}
		rows.Close()
	}
	devs, err := a.devices(ctx, "")
	if err != nil {
		a.fail(w, err)
		return
	}
	var errs []map[string]any
	erows, err := a.DB.QueryContext(ctx, `SELECT s.id, coalesce(d.device_name, s.serial_number, ''), s.error_type, s.message,
		s.occurrences, s.last_seen_at FROM sync_error_logs s LEFT JOIN biometric_devices d ON d.id=s.device_id
		WHERE s.resolved_at IS NULL ORDER BY s.last_seen_at DESC LIMIT 8`)
	if err == nil {
		for erows.Next() {
			var id int64
			var dev, typ, msg string
			var n int
			var last time.Time
			erows.Scan(&id, &dev, &typ, &msg, &n, &last)
			errs = append(errs, map[string]any{"id": id, "device": dev, "error_type": typ, "message": msg,
				"occurrences": n, "last_seen_at": last})
		}
		erows.Close()
	}
	if errs == nil {
		errs = []map[string]any{}
	}
	now := time.Now().In(loc)
	httpx.JSON(w, 200, map[string]any{
		"date":                    now.Format("2006-01-02"),
		"timezone":                tz,
		"active_employees":        active,
		"present_today":           present,
		"not_yet_punched":         max(active-present, 0),
		"unmatched_punches_today": unmatched,
		"rejected_records_today":  rejected,
		"punches_by_hour":         hours,
		"devices":                 devs,
		"sync_errors":             errs,
		// These need shifts and the attendance engine (Phase 3). They are
		// reported as null rather than guessed.
		"late_arrivals":           nil,
		"missing_punches":         nil,
		"overtime_minutes":        nil,
		"attendance_engine_ready": false,
	})
}

func (a *App) listAudit(w http.ResponseWriter, r *http.Request) {
	limit, offset := limitOffset(r, 50, 500)
	q := r.URL.Query()
	args := []any{}
	arg := func(v any) string { args = append(args, v); return fmt.Sprintf("$%d", len(args)) }
	where := []string{"true"}
	if v := q.Get("entity_type"); v != "" {
		where = append(where, "a.entity_type="+arg(v))
	}
	if v := q.Get("entity_id"); v != "" {
		where = append(where, "a.entity_id="+arg(v))
	}
	if v := q.Get("actor_user_id"); validUUID(v) {
		where = append(where, "a.actor_user_id="+arg(v))
	}
	rows, err := a.DB.QueryContext(r.Context(), `SELECT a.id, a.actor_user_id, u.name, a.action, a.entity_type, a.entity_id,
		a.before_data, a.after_data, host(a.source_ip), a.created_at
		FROM audit_logs a LEFT JOIN users u ON u.id=a.actor_user_id
		WHERE `+strings.Join(where, " AND ")+fmt.Sprintf(` ORDER BY a.id DESC LIMIT %d OFFSET %d`, limit, offset), args...)
	if err != nil {
		a.fail(w, err)
		return
	}
	defer rows.Close()
	type entry struct {
		ID        int64           `json:"id"`
		ActorID   *string         `json:"actor_user_id"`
		ActorName *string         `json:"actor_name"`
		Action    string          `json:"action"`
		Type      string          `json:"entity_type"`
		EntityID  *string         `json:"entity_id"`
		Before    json.RawMessage `json:"before"`
		After     json.RawMessage `json:"after"`
		SourceIP  *string         `json:"source_ip"`
		CreatedAt time.Time       `json:"created_at"`
	}
	out := []entry{}
	for rows.Next() {
		var e entry
		var aid, an, eid, ip sql.NullString
		var b, af []byte
		rows.Scan(&e.ID, &aid, &an, &e.Action, &e.Type, &eid, &b, &af, &ip, &e.CreatedAt)
		e.ActorID, e.ActorName, e.EntityID, e.SourceIP = strPtr(aid), strPtr(an), strPtr(eid), strPtr(ip)
		if b != nil {
			e.Before = b
		}
		if af != nil {
			e.After = af
		}
		out = append(out, e)
	}
	httpx.JSON(w, 200, map[string]any{"items": out})
}
