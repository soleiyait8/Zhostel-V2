package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/lib/pq"

	"github.com/your-org/adms/internal/audit"
	"github.com/your-org/adms/internal/httpx"
	"github.com/your-org/adms/internal/protocol"
)

// ------------------------------------------------------------------ devices

type Device struct {
	ID                string     `json:"id"`
	DeviceName        string     `json:"device_name"`
	SerialNumber      string     `json:"serial_number"`
	Model             string     `json:"model"`
	FirmwareVersion   *string    `json:"firmware_version"`
	PushVersion       *string    `json:"push_version"`
	Location          string     `json:"location"`
	Timezone          string     `json:"timezone"`
	AllowedCIDRs      []string   `json:"allowed_cidrs"`
	Enabled           bool       `json:"enabled"`
	Status            string     `json:"status"`
	LastSeenAt        *time.Time `json:"last_seen_at"`
	LastIP            *string    `json:"last_ip"`
	LastTransactionAt *time.Time `json:"last_transaction_at"`
	PunchesToday      int        `json:"punches_today"`
	OpenErrors        int        `json:"open_errors"`
	CreatedAt         time.Time  `json:"created_at"`
}

const deviceSelect = `SELECT d.id, d.device_name, d.serial_number, d.model, d.firmware_version, d.push_version,
	d.location, d.timezone, d.allowed_cidrs::text[], d.enabled,
	CASE WHEN NOT d.enabled THEN 'disabled'
	     WHEN d.last_seen_at IS NULL THEN 'never_connected'
	     WHEN d.last_seen_at > now() - make_interval(secs => $1) THEN 'online'
	     ELSE 'offline' END,
	d.last_seen_at, host(d.last_ip), d.last_transaction_at,
	(SELECT count(*) FROM raw_attendance_logs r WHERE r.device_id=d.id AND r.parse_status='accepted'
	   AND (r.punch_at AT TIME ZONE $2)::date = (now() AT TIME ZONE $2)::date),
	(SELECT count(*) FROM sync_error_logs s WHERE s.device_id=d.id AND s.resolved_at IS NULL),
	d.created_at
	FROM biometric_devices d`

func scanDevice(sc interface{ Scan(...any) error }) (Device, error) {
	var d Device
	var fw, pv, ip sql.NullString
	var ls, lt sql.NullTime
	err := sc.Scan(&d.ID, &d.DeviceName, &d.SerialNumber, &d.Model, &fw, &pv, &d.Location, &d.Timezone,
		pq.Array(&d.AllowedCIDRs), &d.Enabled, &d.Status, &ls, &ip, &lt, &d.PunchesToday, &d.OpenErrors, &d.CreatedAt)
	d.FirmwareVersion, d.PushVersion, d.LastIP = strPtr(fw), strPtr(pv), strPtr(ip)
	d.LastSeenAt, d.LastTransactionAt = timePtr(ls), timePtr(lt)
	if d.AllowedCIDRs == nil {
		d.AllowedCIDRs = []string{}
	}
	return d, err
}

func (a *App) devices(ctx context.Context, where string, args ...any) ([]Device, error) {
	base := []any{a.onlineWindow(ctx), a.businessTZ(ctx).String()}
	rows, err := a.DB.QueryContext(ctx, deviceSelect+" "+where+" ORDER BY d.device_name", append(base, args...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Device{}
	for rows.Next() {
		d, err := scanDevice(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (a *App) listDevices(w http.ResponseWriter, r *http.Request) {
	ds, err := a.devices(r.Context(), "")
	if err != nil {
		a.fail(w, err)
		return
	}
	httpx.JSON(w, 200, map[string]any{"items": ds})
}

func (a *App) getDevice(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	ds, err := a.devices(r.Context(), "WHERE d.id=$3", id)
	if err != nil {
		a.fail(w, err)
		return
	}
	if len(ds) == 0 {
		httpx.Error(w, 404, "not_found", "device not found")
		return
	}
	httpx.JSON(w, 200, ds[0])
}

type deviceIn struct {
	DeviceName   string   `json:"device_name"`
	SerialNumber string   `json:"serial_number"`
	Model        string   `json:"model"`
	Location     string   `json:"location"`
	Timezone     string   `json:"timezone"`
	AllowedCIDRs []string `json:"allowed_cidrs"`
	Enabled      *bool    `json:"enabled"`
}

func (in *deviceIn) validate(creating bool) Problems {
	p := Problems{}
	in.DeviceName, in.Location = strings.TrimSpace(in.DeviceName), strings.TrimSpace(in.Location)
	in.SerialNumber = strings.TrimSpace(in.SerialNumber)
	if in.DeviceName == "" || len(in.DeviceName) > 100 {
		p.add("device_name", "enter a name up to 100 characters")
	}
	if creating && protocol.ValidateSerial(in.SerialNumber) != nil {
		p.add("serial_number", "enter the serial number exactly as shown on the device (4–32 letters, digits, _ or -)")
	}
	if in.Model == "" {
		in.Model = "MB10-VL"
	}
	if in.Timezone == "" {
		in.Timezone = "Asia/Manila"
	}
	if _, err := time.LoadLocation(in.Timezone); err != nil || !strings.Contains(in.Timezone, "/") {
		p.add("timezone", "use an IANA timezone such as Asia/Manila")
	}
	clean := []string{}
	for _, c := range in.AllowedCIDRs {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		if !strings.Contains(c, "/") {
			if ip := net.ParseIP(c); ip != nil && ip.To4() != nil {
				c += "/32"
			} else if ip != nil {
				c += "/128"
			}
		}
		if _, n, err := net.ParseCIDR(c); err != nil {
			p.add("allowed_cidrs", c+" is not a valid IP address or network")
		} else {
			clean = append(clean, n.String())
		}
	}
	in.AllowedCIDRs = clean
	return p
}

func (a *App) createDevice(w http.ResponseWriter, r *http.Request) {
	var in deviceIn
	if !decode(w, r, &in) || in.validate(true).write(w) {
		return
	}
	tx, ok := a.begin(w, r)
	if !ok {
		return
	}
	defer tx.Rollback()
	var id string
	enabled := in.Enabled == nil || *in.Enabled
	if err := tx.QueryRowContext(r.Context(), `INSERT INTO biometric_devices
		(device_name, serial_number, model, location, timezone, allowed_cidrs, enabled)
		VALUES ($1,$2,$3,$4,$5,$6::cidr[],$7) RETURNING id`,
		in.DeviceName, in.SerialNumber, in.Model, in.Location, in.Timezone, pq.Array(in.AllowedCIDRs), enabled).Scan(&id); err != nil {
		a.fail(w, err)
		return
	}
	// The device was probably already knocking: close those alerts.
	tx.ExecContext(r.Context(), `UPDATE sync_error_logs SET resolved_at=now(), resolved_by=$2, device_id=$3
		WHERE serial_number=$1 AND error_type='unknown_device' AND resolved_at IS NULL`, in.SerialNumber, actor(r), id)
	audit.Write(r.Context(), tx, a.entry(r, "create", "device", id, nil, in))
	if err := tx.Commit(); err != nil {
		a.fail(w, err)
		return
	}
	httpx.JSON(w, 201, map[string]string{"id": id})
}

func (a *App) updateDevice(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in deviceIn
	if !decode(w, r, &in) || in.validate(false).write(w) {
		return
	}
	tx, ok := a.begin(w, r)
	if !ok {
		return
	}
	defer tx.Rollback()
	var before deviceIn
	var en bool
	if err := tx.QueryRowContext(r.Context(), `SELECT device_name, serial_number, model, location, timezone, allowed_cidrs::text[], enabled
		FROM biometric_devices WHERE id=$1 FOR UPDATE`, id).Scan(&before.DeviceName, &before.SerialNumber, &before.Model,
		&before.Location, &before.Timezone, pq.Array(&before.AllowedCIDRs), &en); err != nil {
		a.fail(w, err)
		return
	}
	before.Enabled = &en
	enabled := en
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	if _, err := tx.ExecContext(r.Context(), `UPDATE biometric_devices SET device_name=$2, model=$3, location=$4,
		timezone=$5, allowed_cidrs=$6::cidr[], enabled=$7, updated_at=now() WHERE id=$1`,
		id, in.DeviceName, in.Model, in.Location, in.Timezone, pq.Array(in.AllowedCIDRs), enabled); err != nil {
		a.fail(w, err)
		return
	}
	in.SerialNumber = before.SerialNumber // serial is immutable; register a new device instead
	audit.Write(r.Context(), tx, a.entry(r, "update", "device", id, before, in))
	if err := tx.Commit(); err != nil {
		a.fail(w, err)
		return
	}
	w.WriteHeader(204)
}

func (a *App) deviceStatus(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	ds, err := a.devices(r.Context(), "WHERE d.id=$3", id)
	if err != nil {
		a.fail(w, err)
		return
	}
	if len(ds) == 0 {
		httpx.Error(w, 404, "not_found", "device not found")
		return
	}
	outcomes := map[string]int{}
	rows, err := a.DB.QueryContext(r.Context(), `SELECT outcome, count(*) FROM device_communication_logs
		WHERE device_id=$1 AND created_at > now() - interval '24 hours' GROUP BY outcome`, id)
	if err == nil {
		for rows.Next() {
			var o string
			var n int
			rows.Scan(&o, &n)
			outcomes[o] = n
		}
		rows.Close()
	}
	var last struct {
		UserID   *string    `json:"device_user_id"`
		PunchAt  *time.Time `json:"punch_at"`
		Received time.Time  `json:"received_at"`
	}
	var uid sql.NullString
	var pa sql.NullTime
	hasLast := a.DB.QueryRowContext(r.Context(), `SELECT device_user_id, punch_at, received_at FROM raw_attendance_logs
		WHERE device_id=$1 AND parse_status='accepted' ORDER BY received_at DESC LIMIT 1`, id).Scan(&uid, &pa, &last.Received) == nil
	last.UserID, last.PunchAt = strPtr(uid), timePtr(pa)
	resp := map[string]any{"device": ds[0], "outcomes_24h": outcomes, "last_transaction": nil}
	if hasLast {
		resp["last_transaction"] = last
	}
	httpx.JSON(w, 200, resp)
}

func (a *App) deviceComms(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	limit, offset := limitOffset(r, 50, 200)
	rows, err := a.DB.QueryContext(r.Context(), `SELECT id, method, path, query, content_type, body_size, body,
		response_status, response_body, host(remote_ip), outcome, error, duration_ms, created_at
		FROM device_communication_logs WHERE device_id=$1 ORDER BY id DESC LIMIT $2 OFFSET $3`, id, limit, offset)
	if err != nil {
		a.fail(w, err)
		return
	}
	defer rows.Close()
	type comm struct {
		ID           int64     `json:"id"`
		Method       string    `json:"method"`
		Path         string    `json:"path"`
		Query        string    `json:"query"`
		ContentType  string    `json:"content_type"`
		BodySize     int       `json:"body_size"`
		Body         string    `json:"body"`
		Status       int       `json:"response_status"`
		ResponseBody string    `json:"response_body"`
		RemoteIP     *string   `json:"remote_ip"`
		Outcome      string    `json:"outcome"`
		Error        string    `json:"error"`
		DurationMS   int       `json:"duration_ms"`
		CreatedAt    time.Time `json:"created_at"`
	}
	out := []comm{}
	for rows.Next() {
		var c comm
		var ip sql.NullString
		rows.Scan(&c.ID, &c.Method, &c.Path, &c.Query, &c.ContentType, &c.BodySize, &c.Body, &c.Status,
			&c.ResponseBody, &ip, &c.Outcome, &c.Error, &c.DurationMS, &c.CreatedAt)
		c.RemoteIP = strPtr(ip)
		out = append(out, c)
	}
	httpx.JSON(w, 200, map[string]any{"items": out})
}

// deviceUsers lists every user ID known for a device (enrolled on it or seen
// in punches) with its current employee mapping, for the mapping screen.
func (a *App) deviceUsers(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	rows, err := a.DB.QueryContext(r.Context(), `
		WITH ids AS (
			SELECT device_user_id FROM device_users WHERE device_id=$1
			UNION SELECT DISTINCT device_user_id FROM raw_attendance_logs
			      WHERE device_id=$1 AND parse_status='accepted' AND device_user_id IS NOT NULL)
		SELECT ids.device_user_id, du.name,
		       (SELECT max(punch_at) FROM raw_attendance_logs r WHERE r.device_id=$1 AND r.device_user_id=ids.device_user_id),
		       e.id, e.employee_number, e.first_name||' '||e.last_name
		FROM ids
		LEFT JOIN device_users du ON du.device_id=$1 AND du.device_user_id=ids.device_user_id
		LEFT JOIN employee_device_mappings m ON m.device_id=$1 AND m.device_user_id=ids.device_user_id AND m.valid_until IS NULL
		LEFT JOIN employees e ON e.id=m.employee_id
		ORDER BY (e.id IS NOT NULL), ids.device_user_id`, id)
	if err != nil {
		a.fail(w, err)
		return
	}
	defer rows.Close()
	type du struct {
		DeviceUserID   string     `json:"device_user_id"`
		NameOnDevice   *string    `json:"name_on_device"`
		LastPunchAt    *time.Time `json:"last_punch_at"`
		EmployeeID     *string    `json:"employee_id"`
		EmployeeNumber *string    `json:"employee_number"`
		EmployeeName   *string    `json:"employee_name"`
	}
	out := []du{}
	for rows.Next() {
		var u du
		var n, eid, en, ename sql.NullString
		var lp sql.NullTime
		rows.Scan(&u.DeviceUserID, &n, &lp, &eid, &en, &ename)
		u.NameOnDevice, u.LastPunchAt, u.EmployeeID, u.EmployeeNumber, u.EmployeeName = strPtr(n), timePtr(lp), strPtr(eid), strPtr(en), strPtr(ename)
		out = append(out, u)
	}
	httpx.JSON(w, 200, map[string]any{"items": out})
}

// reprocess re-evaluates rejected records for a device with the current
// parser and device settings. Original payloads are never modified.
func (a *App) reprocess(w http.ResponseWriter, r *http.Request) {
	devID, ok := pathID(w, r)
	if !ok {
		return
	}
	var in struct {
		IDs          []string `json:"ids"`
		AcceptFuture bool     `json:"accept_future"`
	}
	if !decode(w, r, &in) {
		return
	}
	for _, id := range in.IDs {
		if !validUUID(id) {
			httpx.Error(w, 422, "validation_failed", "ids must be record UUIDs")
			return
		}
	}
	tx, ok := a.begin(w, r)
	if !ok {
		return
	}
	defer tx.Rollback()
	var tz string
	if err := tx.QueryRowContext(r.Context(), `SELECT timezone FROM biometric_devices WHERE id=$1`, devID).Scan(&tz); err != nil {
		a.fail(w, err)
		return
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		httpx.Error(w, 422, "invalid_timezone", "fix the device timezone first")
		return
	}
	q := `SELECT id, original_payload, rejection_reason FROM raw_attendance_logs
		WHERE device_id=$1 AND parse_status='rejected'`
	args := []any{devID}
	if len(in.IDs) > 0 {
		q += ` AND id = ANY($2::uuid[])`
		args = append(args, pq.Array(in.IDs))
	}
	rows, err := tx.QueryContext(r.Context(), q+` ORDER BY received_at LIMIT 5000 FOR UPDATE`, args...)
	if err != nil {
		a.fail(w, err)
		return
	}
	type rec struct{ id, payload, reason string }
	var recs []rec
	for rows.Next() {
		var x rec
		var reason sql.NullString
		rows.Scan(&x.id, &x.payload, &reason)
		x.reason = reason.String
		recs = append(recs, x)
	}
	rows.Close()
	accepted, still := 0, 0
	limit := time.Now().Add(24 * time.Hour)
	for _, x := range recs {
		lr := protocol.ParseAttLogLine(1, x.payload)
		if lr.Log == nil {
			tx.ExecContext(r.Context(), `UPDATE raw_attendance_logs SET rejection_reason=$2, reprocessed_at=now() WHERE id=$1`, x.id, lr.Reason)
			still++
			continue
		}
		punch := protocol.Instant(lr.Log.Wall, loc)
		if punch.After(limit) && !in.AcceptFuture {
			tx.ExecContext(r.Context(), `UPDATE raw_attendance_logs SET rejection_reason='timestamp_in_future', reprocessed_at=now() WHERE id=$1`, x.id)
			still++
			continue
		}
		if _, err := tx.ExecContext(r.Context(), `UPDATE raw_attendance_logs SET parse_status='accepted', rejection_reason=NULL,
			device_user_id=$2, device_timestamp=coalesce(device_timestamp, $3::timestamp), punch_at=$4,
			event_type=$5, verification_method=$6, work_code=$7, reprocessed_at=now() WHERE id=$1`,
			x.id, lr.Log.UserID, lr.Log.Wall.Format("2006-01-02 15:04:05"), punch,
			nullStr(&lr.Log.Status), nullStr(&lr.Log.Verify), nullStr(&lr.Log.WorkCode)); err != nil {
			a.fail(w, err)
			return
		}
		accepted++
	}
	result := map[string]any{"examined": len(recs), "accepted": accepted, "still_rejected": still, "accept_future": in.AcceptFuture}
	audit.Write(r.Context(), tx, a.entry(r, "reprocess", "device", devID, nil, result))
	if err := tx.Commit(); err != nil {
		a.fail(w, err)
		return
	}
	httpx.JSON(w, 200, result)
}

func (a *App) setupInfoMap() map[string]any {
	scheme := "http"
	if a.ADMSPublicHTTPS {
		scheme = "https"
	}
	return map[string]any{
		"server_address": a.ADMSPublicHost,
		"server_port":    a.ADMSPublicPort,
		"https":          a.ADMSPublicHTTPS,
		"endpoint":       fmt.Sprintf("%s://%s:%d/iclock/cdata", scheme, a.ADMSPublicHost, a.ADMSPublicPort),
	}
}

func (a *App) setupInfo(w http.ResponseWriter, r *http.Request) {
	httpx.JSON(w, 200, a.setupInfoMap())
}

// reachability checks the server side of the path: is the ADMS service up,
// and when did this device last reach it. A device-side test still has to be
// done from the terminal itself (see the device guide).
func (a *App) reachability(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	svc := map[string]any{"reachable": false}
	req, _ := http.NewRequestWithContext(ctx, "GET", a.ADMSInternalURL+"/healthz", nil)
	if resp, err := a.HTTPClient.Do(req); err == nil {
		var body map[string]any
		json.NewDecoder(resp.Body).Decode(&body)
		resp.Body.Close()
		svc = map[string]any{"reachable": resp.StatusCode == 200, "status_code": resp.StatusCode, "detail": body}
	} else {
		svc["error"] = err.Error()
	}
	var last sql.NullTime
	var ip sql.NullString
	a.DB.QueryRowContext(r.Context(), `SELECT last_seen_at, host(last_ip) FROM biometric_devices WHERE id=$1`, id).Scan(&last, &ip)
	httpx.JSON(w, 200, map[string]any{"adms_service": svc, "device_last_seen_at": timePtr(last),
		"device_last_ip": strPtr(ip), "setup": a.setupInfoMap()})
}

// ------------------------------------------------------------------ sync errors

func (a *App) listSyncErrors(w http.ResponseWriter, r *http.Request) {
	limit, _ := limitOffset(r, 50, 200)
	cond := "s.resolved_at IS NULL"
	if r.URL.Query().Get("status") == "all" {
		cond = "true"
	}
	args := []any{limit}
	if d := r.URL.Query().Get("device_id"); validUUID(d) {
		cond += " AND s.device_id=$2"
		args = append(args, d)
	}
	rows, err := a.DB.QueryContext(r.Context(), `SELECT s.id, s.device_id, d.device_name, s.serial_number, s.error_type,
		s.message, s.details, s.occurrences, s.first_seen_at, s.last_seen_at, s.resolved_at
		FROM sync_error_logs s LEFT JOIN biometric_devices d ON d.id=s.device_id
		WHERE `+cond+` ORDER BY s.last_seen_at DESC LIMIT $1`, args...)
	if err != nil {
		a.fail(w, err)
		return
	}
	defer rows.Close()
	type se struct {
		ID          int64           `json:"id"`
		DeviceID    *string         `json:"device_id"`
		DeviceName  *string         `json:"device_name"`
		Serial      *string         `json:"serial_number"`
		ErrorType   string          `json:"error_type"`
		Message     string          `json:"message"`
		Details     json.RawMessage `json:"details"`
		Occurrences int             `json:"occurrences"`
		FirstSeen   time.Time       `json:"first_seen_at"`
		LastSeen    time.Time       `json:"last_seen_at"`
		ResolvedAt  *time.Time      `json:"resolved_at"`
	}
	out := []se{}
	for rows.Next() {
		var s se
		var did, dn, sn sql.NullString
		var ra sql.NullTime
		var det []byte
		rows.Scan(&s.ID, &did, &dn, &sn, &s.ErrorType, &s.Message, &det, &s.Occurrences, &s.FirstSeen, &s.LastSeen, &ra)
		s.DeviceID, s.DeviceName, s.Serial, s.ResolvedAt, s.Details = strPtr(did), strPtr(dn), strPtr(sn), timePtr(ra), det
		out = append(out, s)
	}
	httpx.JSON(w, 200, map[string]any{"items": out})
}

func (a *App) resolveSyncError(w http.ResponseWriter, r *http.Request) {
	var id int64
	if _, err := fmt.Sscan(r.PathValue("id"), &id); err != nil {
		httpx.Error(w, 404, "not_found", "not found")
		return
	}
	tx, ok := a.begin(w, r)
	if !ok {
		return
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(r.Context(), `UPDATE sync_error_logs SET resolved_at=now(), resolved_by=$2 WHERE id=$1 AND resolved_at IS NULL`, id, actor(r))
	if err != nil {
		a.fail(w, err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		httpx.Error(w, 404, "not_found", "no open error with that id")
		return
	}
	audit.Write(r.Context(), tx, a.entry(r, "resolve", "sync_error", fmt.Sprint(id), nil, nil))
	if err := tx.Commit(); err != nil {
		a.fail(w, err)
		return
	}
	w.WriteHeader(204)
}
