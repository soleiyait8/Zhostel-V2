package api

import (
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/your-org/adms/internal/auth"
	"github.com/your-org/adms/internal/httpx"
	"github.com/your-org/adms/internal/testdb"
)

type client struct {
	t    *testing.T
	base string
	hc   *http.Client
	csrf bool
}

func (c *client) do(method, path, body string) (int, map[string]any, string) {
	c.t.Helper()
	req, _ := http.NewRequest(method, c.base+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if c.csrf {
		req.Header.Set(auth.CSRFHeader, "1")
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var m map[string]any
	json.Unmarshal(raw, &m)
	return resp.StatusCode, m, string(raw)
}

func setup(t *testing.T) (*sql.DB, *httptest.Server) {
	auth.Iterations = 1000
	conn := testdb.Open(t)
	app := &App{DB: conn, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Auth:            &auth.Service{DB: conn, TTL: time.Hour, LoginLimiter: httpx.NewRateLimiter(1000, time.Minute)},
		ADMSInternalURL: "http://127.0.0.1:1", ADMSPublicHost: "adms.test", ADMSPublicPort: 443, ADMSPublicHTTPS: true,
		HTTPClient: &http.Client{Timeout: time.Second}}
	ts := httptest.NewServer(app.Routes())
	t.Cleanup(ts.Close)
	for _, u := range [][2]string{{"admin@test.ph", "admin"}, {"hr@test.ph", "hr"}, {"view@test.ph", "viewer"}} {
		h, _ := auth.HashPassword("a long test password")
		if _, err := conn.Exec(`INSERT INTO users (name, email, role, password_hash) VALUES ($1,$1,$2,$3)`, u[0], u[1], h); err != nil {
			t.Fatal(err)
		}
	}
	return conn, ts
}

func login(t *testing.T, ts *httptest.Server, email string) *client {
	jar, _ := cookiejar.New(nil)
	c := &client{t: t, base: ts.URL, hc: &http.Client{Jar: jar}, csrf: true}
	code, _, raw := c.do("POST", "/api/auth/login", `{"email":"`+email+`","password":"a long test password"}`)
	if code != 200 {
		t.Fatalf("login %s: %d %s", email, code, raw)
	}
	return c
}

func TestAuthAndRoles(t *testing.T) {
	_, ts := setup(t)
	anon := &client{t: t, base: ts.URL, hc: http.DefaultClient}
	if code, _, _ := anon.do("GET", "/api/employees", ""); code != 401 {
		t.Errorf("anonymous: %d", code)
	}
	if code, _, _ := anon.do("POST", "/api/auth/login", `{"email":"admin@test.ph","password":"wrong password!!"}`); code != 401 {
		t.Errorf("bad password: %d", code)
	}
	viewer := login(t, ts, "view@test.ph")
	if code, _, _ := viewer.do("GET", "/api/employees", ""); code != 200 {
		t.Errorf("viewer read: %d", code)
	}
	if code, _, _ := viewer.do("POST", "/api/employees", `{"employee_number":"X1","first_name":"A","last_name":"B"}`); code != 403 {
		t.Errorf("viewer write: %d", code)
	}
	hr := login(t, ts, "hr@test.ph")
	if code, _, _ := hr.do("POST", "/api/devices", `{"device_name":"x","serial_number":"ABCD1234"}`); code != 403 {
		t.Errorf("hr registering device: %d", code)
	}
	if code, _, _ := hr.do("GET", "/api/users", ""); code != 403 {
		t.Errorf("hr listing users: %d", code)
	}
	hr.csrf = false
	if code, m, _ := hr.do("POST", "/api/employees", `{"employee_number":"X1","first_name":"A","last_name":"B"}`); code != 403 ||
		m["error"].(map[string]any)["code"] != "csrf" {
		t.Errorf("missing CSRF header: %d", code)
	}
	hr.csrf = true
	hr.do("POST", "/api/auth/logout", "")
	if code, _, _ := hr.do("GET", "/api/employees", ""); code != 401 {
		t.Errorf("after logout: %d", code)
	}
}

func TestEmployeeLifecycleMappingsAndAudit(t *testing.T) {
	conn, ts := setup(t)
	admin := login(t, ts, "admin@test.ph")
	_, d, _ := admin.do("POST", "/api/departments", `{"name":"Operations"}`)
	depID := d["id"].(string)
	code, m, raw := admin.do("POST", "/api/employees", `{"employee_number":"E-1","first_name":"Maria","last_name":"Santos","department_id":"`+depID+`","hire_date":"2024-02-01"}`)
	if code != 201 {
		t.Fatalf("create: %d %s", code, raw)
	}
	emp := m["id"].(string)
	if code, m, _ = admin.do("POST", "/api/employees", `{"employee_number":"bad number!","first_name":"","last_name":"x","hire_date":"01/02/2024"}`); code != 422 {
		t.Errorf("validation: %d", code)
	} else if f := m["error"].(map[string]any)["fields"].(map[string]any); len(f) != 3 {
		t.Errorf("expected 3 field errors, got %v", f)
	}
	_, dv, _ := admin.do("POST", "/api/devices", `{"device_name":"Lobby","serial_number":"CQZ0001","allowed_cidrs":["203.0.113.7"]}`)
	dev := dv["id"].(string)
	if code, _, raw = admin.do("POST", "/api/employees/"+emp+"/mappings", `{"device_id":"`+dev+`","device_user_id":"1001"}`); code != 201 {
		t.Fatalf("map: %d %s", code, raw)
	}
	_, m2, _ := admin.do("POST", "/api/employees", `{"employee_number":"E-2","first_name":"Jose","last_name":"Reyes"}`)
	emp2 := m2["id"].(string)
	if code, m, _ = admin.do("POST", "/api/employees/"+emp2+"/mappings", `{"device_id":"`+dev+`","device_user_id":"1001"}`); code != 409 ||
		!strings.Contains(m["error"].(map[string]any)["message"].(string), "E-1") {
		t.Errorf("overlapping mapping should 409 naming E-1: %d %v", code, m)
	}
	// Deactivating closes the mapping, so the ID can be reused — history stays.
	if code, _, _ = admin.do("POST", "/api/employees/"+emp+"/deactivate", ""); code != 200 {
		t.Fatalf("deactivate: %d", code)
	}
	time.Sleep(1100 * time.Millisecond)
	if code, _, raw = admin.do("POST", "/api/employees/"+emp2+"/mappings", `{"device_id":"`+dev+`","device_user_id":"1001"}`); code != 201 {
		t.Fatalf("reuse after deactivation: %d %s", code, raw)
	}
	var n int
	conn.QueryRow(`SELECT count(*) FROM employee_device_mappings WHERE device_user_id='1001'`).Scan(&n)
	if n != 2 {
		t.Errorf("mapping history rows=%d want 2", n)
	}
	var status string
	conn.QueryRow(`SELECT employment_status FROM employees WHERE id=$1`, emp).Scan(&status)
	if status != "inactive" {
		t.Errorf("status=%s", status)
	}
	_, al, _ := admin.do("GET", "/api/audit-logs?entity_type=employee&entity_id="+emp, "")
	if items := al["items"].([]any); len(items) != 2 {
		t.Errorf("audit entries for employee=%d want 2 (create, deactivate)", len(items))
	}
	if _, err := conn.Exec(`DELETE FROM audit_logs`); err != nil {
		t.Fatal(err)
	}
	conn.QueryRow(`SELECT count(*) FROM audit_logs`).Scan(&n)
	if n == 0 {
		t.Error("audit log was deletable")
	}
}

func TestCSVImportExport(t *testing.T) {
	_, ts := setup(t)
	hr := login(t, ts, "hr@test.ph")
	hr.do("POST", "/api/departments", `{"name":"Finance"}`)
	csvBody := "\ufeffemployee_number,first_name,last_name,department,position,employment_status,hire_date\n" +
		"E-10,Ana,Cruz,Finance,=HYPERLINK(\"x\"),active,2023-01-09\n" +
		"E-11,Ben,Lim,,Clerk,,\n"
	req, _ := http.NewRequest("POST", ts.URL+"/api/employees/import?dry_run=true", strings.NewReader(csvBody))
	req.Header.Set(auth.CSRFHeader, "1")
	resp, _ := hr.hc.Do(req)
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	resp.Body.Close()
	if resp.StatusCode != 200 || out["saved"] != false {
		t.Fatalf("dry run: %d %v", resp.StatusCode, out)
	}
	req, _ = http.NewRequest("POST", ts.URL+"/api/employees/import", strings.NewReader(csvBody))
	req.Header.Set(auth.CSRFHeader, "1")
	resp, _ = hr.hc.Do(req)
	json.NewDecoder(resp.Body).Decode(&out)
	resp.Body.Close()
	if out["saved"] != true {
		t.Fatalf("import: %v", out)
	}
	bad := "employee_number,first_name,last_name,department\nE-12,X,Y,Nowhere\n"
	req, _ = http.NewRequest("POST", ts.URL+"/api/employees/import", strings.NewReader(bad))
	req.Header.Set(auth.CSRFHeader, "1")
	resp, _ = hr.hc.Do(req)
	resp.Body.Close()
	if resp.StatusCode != 422 {
		t.Errorf("unknown department should fail whole import: %d", resp.StatusCode)
	}
	resp, _ = hr.hc.Get(ts.URL + "/api/employees/export.csv")
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(b), `"'=HYPERLINK(""x"")"`) {
		t.Errorf("formula not neutralised in export:\n%s", b)
	}
}

func TestPunchesUseMappingAtPunchTimeAndReprocess(t *testing.T) {
	conn, ts := setup(t)
	admin := login(t, ts, "admin@test.ph")
	conn.Exec(`INSERT INTO sync_error_logs (serial_number, error_type, message) VALUES ('CQZ0002','unknown_device','x')`)
	_, dv, _ := admin.do("POST", "/api/devices", `{"device_name":"Gate","serial_number":"CQZ0002"}`)
	dev := dv["id"].(string)
	var open int
	conn.QueryRow(`SELECT count(*) FROM sync_error_logs WHERE resolved_at IS NULL`).Scan(&open)
	if open != 0 {
		t.Error("registering should resolve the unknown_device alert")
	}
	var e1, e2 string
	conn.QueryRow(`INSERT INTO employees (employee_number, first_name, last_name) VALUES ('E-1','Old','Holder') RETURNING id`).Scan(&e1)
	conn.QueryRow(`INSERT INTO employees (employee_number, first_name, last_name) VALUES ('E-2','New','Holder') RETURNING id`).Scan(&e2)
	conn.Exec(`INSERT INTO employee_device_mappings (employee_id, device_id, device_user_id, valid_from, valid_until)
		VALUES ($1,$3,'7','2026-01-01T00:00:00Z','2026-06-01T00:00:00Z'),($2,$3,'7','2026-06-01T00:00:00Z',NULL)`, e1, e2, dev)
	ins := `INSERT INTO raw_attendance_logs (device_id, device_serial_number, device_user_id, device_timestamp, punch_at,
		original_payload, payload_hash, parse_status, rejection_reason) VALUES ($1,'CQZ0002',$2,$3,$4,$5,$6,$7,$8)`
	conn.Exec(ins, dev, "7", "2026-03-02 08:00:00", "2026-03-02T00:00:00Z", "7\t2026-03-02 08:00:00", []byte("a"), "accepted", nil)
	conn.Exec(ins, dev, "7", "2026-07-02 08:00:00", "2026-07-02T00:00:00Z", "7\t2026-07-02 08:00:00", []byte("b"), "accepted", nil)
	conn.Exec(ins, dev, nil, nil, nil, "8\t2026-07-02 09:00:00", []byte("c"), "rejected", "invalid_timestamp")

	_, p, raw := admin.do("GET", "/api/punches?from=2026-03-01&to=2026-05-31", "")
	items := p["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["employee_number"] != "E-1" {
		t.Errorf("March punch should belong to E-1: %s", raw)
	}
	_, p, _ = admin.do("GET", "/api/punches?from=2026-07-01&to=2026-07-31&status=accepted", "")
	if items = p["items"].([]any); len(items) != 1 || items[0].(map[string]any)["employee_number"] != "E-2" {
		t.Errorf("July punch should belong to E-2: %v", items)
	}
	if code, _, _ := admin.do("GET", "/api/punches?from=2026-07-31&to=2026-07-01", ""); code != 422 {
		t.Error("inverted range should be rejected")
	}
	// The rejected line is actually valid with the current parser: reprocess accepts it.
	code, r, raw := admin.do("POST", "/api/devices/"+dev+"/reprocess", `{}`)
	if code != 200 || r["accepted"].(float64) != 1 {
		t.Fatalf("reprocess: %d %s", code, raw)
	}
	var uid, payload string
	conn.QueryRow(`SELECT device_user_id, original_payload FROM raw_attendance_logs WHERE payload_hash='c'`).Scan(&uid, &payload)
	if uid != "8" || payload != "8\t2026-07-02 09:00:00" {
		t.Errorf("reprocessed row: uid=%q payload=%q", uid, payload)
	}
}

func TestDashboardAndDeviceStatus(t *testing.T) {
	conn, ts := setup(t)
	admin := login(t, ts, "admin@test.ph")
	_, dv, _ := admin.do("POST", "/api/devices", `{"device_name":"Lobby","serial_number":"CQZ0003"}`)
	dev := dv["id"].(string)
	var e string
	conn.QueryRow(`INSERT INTO employees (employee_number, first_name, last_name) VALUES ('E-1','A','B') RETURNING id`).Scan(&e)
	conn.Exec(`INSERT INTO employees (employee_number, first_name, last_name) VALUES ('E-2','C','D')`)
	conn.Exec(`INSERT INTO employee_device_mappings (employee_id, device_id, device_user_id, valid_from) VALUES ($1,$2,'1', now() - interval '1 day')`, e, dev)
	conn.Exec(`INSERT INTO raw_attendance_logs (device_id, device_serial_number, device_user_id, punch_at, original_payload, payload_hash, parse_status)
		VALUES ($1,'CQZ0003','1', now() - interval '1 minute','x','h1','accepted'), ($1,'CQZ0003','99', now() - interval '1 minute','y','h2','accepted')`, dev)
	conn.Exec(`UPDATE biometric_devices SET last_seen_at=now()`)
	_, d, raw := admin.do("GET", "/api/dashboard", "")
	if d["active_employees"].(float64) != 2 || d["present_today"].(float64) != 1 ||
		d["not_yet_punched"].(float64) != 1 || d["unmatched_punches_today"].(float64) != 1 {
		t.Errorf("dashboard: %s", raw)
	}
	if d["late_arrivals"] != nil {
		t.Error("late arrivals must be null until the attendance engine exists")
	}
	_, s, _ := admin.do("GET", "/api/devices/"+dev+"/status", "")
	if s["device"].(map[string]any)["status"] != "online" {
		t.Errorf("status: %v", s["device"])
	}
	_, u, _ := admin.do("GET", "/api/unmatched-users", "")
	if items := u["items"].([]any); len(items) != 1 || items[0].(map[string]any)["device_user_id"] != "99" {
		t.Errorf("unmatched: %v", items)
	}
	_, rc, _ := admin.do("GET", "/api/devices/"+dev+"/reachability", "")
	if rc["adms_service"].(map[string]any)["reachable"] != false {
		t.Error("reachability should report the unreachable test URL honestly")
	}
}
