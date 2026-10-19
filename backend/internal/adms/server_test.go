package adms

import (
	"database/sql"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/your-org/adms/internal/httpx"
	"github.com/your-org/adms/internal/protocol"
	"github.com/your-org/adms/internal/testdb"
)

// Integration tests with SIMULATED device traffic. They exercise the real
// schema but not real MB10-VL firmware.

const sn = "SIMMB10VL0001"

var fixedNow = time.Date(2026, 10, 19, 10, 0, 0, 0, time.UTC)

func setup(t *testing.T) (*sql.DB, *Server, *httptest.Server) {
	conn := testdb.Open(t)
	s := &Server{
		DB: conn, Profile: protocol.Profile{Name: "test", AckStyle: "ok_count", LineEnding: "\n",
			ErrorDelay: 30, Delay: 10, TransTimes: "00:00", TransInterval: 1, TransFlag: "1111000000", TimeZone: 8},
		Log:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		MaxBody: 1 << 20, FutureTolerance: 24 * time.Hour,
		Limiter: httpx.NewRateLimiter(10000, time.Minute),
		Now:     func() time.Time { return fixedNow },
	}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return conn, s, ts
}

func register(t *testing.T, conn *sql.DB, serial string) string {
	var id string
	if err := conn.QueryRow(`INSERT INTO biometric_devices (device_name, serial_number) VALUES ('Lobby', $1) RETURNING id`, serial).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func do(t *testing.T, method, url, body string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(method, url, strings.NewReader(body))
	req.Header.Set("Content-Type", "text/plain")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func count(t *testing.T, conn *sql.DB, q string, args ...any) int {
	t.Helper()
	var n int
	if err := conn.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

const batch = "1001\t2026-10-19 08:01:22\t0\t1\t0\t0\t0\r\n" +
	"1002\t2026-10-19 08:03:05\t0\t15\t0\t0\t0\r\n" +
	"garbage line without tabs\r\n" +
	"1001\t2026-10-19 17:30:00\t1\t1\t0\t0\t0\r\n"

func TestUnknownDeviceNotAcknowledged(t *testing.T) {
	conn, _, ts := setup(t)
	code, body := do(t, "POST", ts.URL+"/iclock/cdata?SN="+sn+"&table=ATTLOG&Stamp=1", batch)
	if code != 403 || strings.HasPrefix(body, "OK") {
		t.Fatalf("unknown device must not be acked: %d %q", code, body)
	}
	if n := count(t, conn, `SELECT count(*) FROM raw_attendance_logs`); n != 0 {
		t.Errorf("stored %d rows for unknown device", n)
	}
	do(t, "GET", ts.URL+"/iclock/getrequest?SN="+sn, "")
	if n := count(t, conn, `SELECT occurrences FROM sync_error_logs WHERE error_type='unknown_device'`); n != 2 {
		t.Errorf("occurrences=%d want 2 (repeats collapse)", n)
	}
}

func TestInvalidSerialRejected(t *testing.T) {
	_, _, ts := setup(t)
	for _, q := range []string{"", "?SN=", "?SN=a%27%3B--", "?SN=AB"} {
		if code, _ := do(t, "GET", ts.URL+"/iclock/cdata"+q, ""); code != 400 {
			t.Errorf("%q: code %d want 400", q, code)
		}
	}
}

func TestHandshakeAndHeartbeat(t *testing.T) {
	conn, _, ts := setup(t)
	register(t, conn, sn)
	code, body := do(t, "GET", ts.URL+"/iclock/cdata?SN="+sn+"&options=all&pushver=2.4.1", "")
	if code != 200 || !strings.HasPrefix(body, "GET OPTION FROM: "+sn+"\n") || !strings.Contains(body, "ATTLOGStamp=None") {
		t.Fatalf("handshake: %d %q", code, body)
	}
	code, body = do(t, "GET", ts.URL+"/iclock/getrequest?SN="+sn+"&INFO=Ver%206.60%20Apr%202%202019,1,2", "")
	if code != 200 || body != "OK" {
		t.Fatalf("heartbeat: %d %q", code, body)
	}
	var status, fw, pv string
	conn.QueryRow(`SELECT connection_status, firmware_version, push_version FROM biometric_devices`).Scan(&status, &fw, &pv)
	if status != "online" || fw != "Ver 6.60 Apr 2 2019" || pv != "2.4.1" {
		t.Errorf("device state %s/%s/%s", status, fw, pv)
	}
}

func TestAttLogIngestDedupAndStamp(t *testing.T) {
	conn, _, ts := setup(t)
	register(t, conn, sn)
	url := ts.URL + "/iclock/cdata?SN=" + sn + "&table=ATTLOG&Stamp=9999"
	code, body := do(t, "POST", url, batch)
	if code != 200 || body != "OK: 4" {
		t.Fatalf("upload: %d %q", code, body)
	}
	if a := count(t, conn, `SELECT count(*) FROM raw_attendance_logs WHERE parse_status='accepted'`); a != 3 {
		t.Errorf("accepted=%d want 3", a)
	}
	if r := count(t, conn, `SELECT count(*) FROM raw_attendance_logs WHERE parse_status='rejected' AND rejection_reason='not_tab_separated'`); r != 1 {
		t.Errorf("rejected=%d want 1", r)
	}
	// Replay the same batch (e.g. ack lost on the network): acked, not duplicated.
	code, body = do(t, "POST", url, batch)
	if code != 200 || body != "OK: 4" {
		t.Fatalf("replay: %d %q", code, body)
	}
	if n := count(t, conn, `SELECT count(*) FROM raw_attendance_logs`); n != 4 {
		t.Errorf("rows after replay=%d want 4", n)
	}
	// Stamp is returned on the next handshake so the device only sends newer data.
	_, hs := do(t, "GET", ts.URL+"/iclock/cdata?SN="+sn+"&options=all", "")
	if !strings.Contains(hs, "ATTLOGStamp=9999") {
		t.Errorf("stamp not echoed:\n%s", hs)
	}
	// Device timezone: 08:01:22 Manila == 00:01:22 UTC.
	var punch time.Time
	conn.QueryRow(`SELECT punch_at FROM raw_attendance_logs WHERE device_user_id='1001' ORDER BY punch_at LIMIT 1`).Scan(&punch)
	if !punch.UTC().Equal(time.Date(2026, 10, 19, 0, 1, 22, 0, time.UTC)) {
		t.Errorf("punch_at=%v", punch.UTC())
	}
	// Every stored row links back to the communication log entry.
	if n := count(t, conn, `SELECT count(*) FROM raw_attendance_logs WHERE comm_log_id IS NULL`); n != 0 {
		t.Errorf("%d rows without comm log", n)
	}
	if n := count(t, conn, `SELECT occurrences FROM sync_error_logs WHERE error_type='rejected_records'`); n != 1 {
		t.Errorf("rejected_records occurrences=%d (replay of duplicates should not re-report)", n)
	}
}

func TestDatabaseFailureIsNotAcknowledgedThenRecovers(t *testing.T) {
	conn, _, ts := setup(t)
	register(t, conn, sn)
	// Simulate a storage failure mid-transaction.
	conn.Exec(`CREATE FUNCTION fail_insert() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'simulated outage'; END $$`)
	conn.Exec(`CREATE TRIGGER fail_insert BEFORE INSERT ON raw_attendance_logs FOR EACH ROW EXECUTE FUNCTION fail_insert()`)
	url := ts.URL + "/iclock/cdata?SN=" + sn + "&table=ATTLOG&Stamp=5"
	code, body := do(t, "POST", url, batch)
	if code != 503 || strings.HasPrefix(body, "OK") {
		t.Fatalf("failure must not be acked: %d %q", code, body)
	}
	if n := count(t, conn, `SELECT count(*) FROM raw_attendance_logs`); n != 0 {
		t.Fatal("partial batch committed")
	}
	var stamp sql.NullString
	conn.QueryRow(`SELECT attlog_stamp FROM biometric_devices`).Scan(&stamp)
	if stamp.Valid {
		t.Error("stamp advanced despite failure")
	}
	if n := count(t, conn, `SELECT count(*) FROM device_communication_logs WHERE outcome='db_error'`); n != 1 {
		t.Errorf("failure not logged (%d)", n)
	}
	conn.Exec(`DROP TRIGGER fail_insert ON raw_attendance_logs`)
	if code, body = do(t, "POST", url, batch); code != 200 || body != "OK: 4" {
		t.Fatalf("retry after recovery: %d %q", code, body)
	}
	if n := count(t, conn, `SELECT count(*) FROM raw_attendance_logs`); n != 4 {
		t.Errorf("rows=%d want 4", n)
	}
}

func TestFutureTimestampHeldForReview(t *testing.T) {
	conn, _, ts := setup(t)
	register(t, conn, sn)
	do(t, "POST", ts.URL+"/iclock/cdata?SN="+sn+"&table=ATTLOG", "1001\t2027-01-01 08:00:00\t0\t1\n")
	if n := count(t, conn, `SELECT count(*) FROM raw_attendance_logs WHERE rejection_reason='timestamp_in_future'`); n != 1 {
		t.Error("future punch should be retained as rejected")
	}
}

func TestDisabledDeviceAndIPAllowlist(t *testing.T) {
	conn, s, ts := setup(t)
	id := register(t, conn, sn)
	conn.Exec(`UPDATE biometric_devices SET allowed_cidrs='{203.0.113.0/24}' WHERE id=$1`, id)
	if code, _ := do(t, "GET", ts.URL+"/iclock/cdata?SN="+sn, ""); code != 403 {
		t.Errorf("loopback outside allowlist: %d", code)
	}
	conn.Exec(`UPDATE biometric_devices SET allowed_cidrs='{127.0.0.0/8}', enabled=false WHERE id=$1`, id)
	if code, _ := do(t, "GET", ts.URL+"/iclock/cdata?SN="+sn, ""); code != 403 {
		t.Errorf("disabled device: %d", code)
	}
	conn.Exec(`UPDATE biometric_devices SET enabled=true WHERE id=$1`, id)
	if code, _ := do(t, "GET", ts.URL+"/iclock/cdata?SN="+sn, ""); code != 200 {
		t.Errorf("re-enabled device: %d", code)
	}
	_ = s
}

func TestOperlogUsersStoredTemplatesNot(t *testing.T) {
	conn, _, ts := setup(t)
	register(t, conn, sn)
	body := "USER PIN=1001\tName=Maria Santos\tPri=0\n" +
		"FP PIN=1001\tFID=6\tSize=8\tValid=1\tTMP=U0VDUkVUVE1QTA==\n"
	code, resp := do(t, "POST", ts.URL+"/iclock/cdata?SN="+sn+"&table=OPERLOG&Stamp=77", body)
	if code != 200 || resp != "OK: 2" {
		t.Fatalf("%d %q", code, resp)
	}
	var name string
	conn.QueryRow(`SELECT name FROM device_users WHERE device_user_id='1001'`).Scan(&name)
	if name != "Maria Santos" {
		t.Errorf("name=%q", name)
	}
	if n := count(t, conn, `SELECT count(*) FROM device_communication_logs WHERE body LIKE '%U0VDUkVU%'`); n != 0 {
		t.Error("biometric template persisted in comm log")
	}
}

func TestRawLogsImmutable(t *testing.T) {
	conn, _, ts := setup(t)
	register(t, conn, sn)
	do(t, "POST", ts.URL+"/iclock/cdata?SN="+sn+"&table=ATTLOG", "1001\t2026-10-19 08:00:00\t0\t1\n")
	if _, err := conn.Exec(`UPDATE raw_attendance_logs SET original_payload='tampered'`); err == nil {
		t.Error("original payload was modifiable")
	}
	conn.Exec(`DELETE FROM raw_attendance_logs`)
	if n := count(t, conn, `SELECT count(*) FROM raw_attendance_logs`); n != 1 {
		t.Error("raw log was deletable")
	}
}

func TestCommandQueue(t *testing.T) {
	conn, s, ts := setup(t)
	s.EnableCommands = true
	id := register(t, conn, sn)
	var cmdID int64
	conn.QueryRow(`INSERT INTO device_commands (device_id, command) VALUES ($1,'INFO') RETURNING id`, id).Scan(&cmdID)
	_, body := do(t, "GET", ts.URL+"/iclock/getrequest?SN="+sn, "")
	if !strings.HasPrefix(body, "C:") || !strings.Contains(body, ":INFO") {
		t.Fatalf("command not delivered: %q", body)
	}
	do(t, "POST", ts.URL+"/iclock/devicecmd?SN="+sn, "ID="+itoa(cmdID)+"&Return=0&CMD=INFO\n")
	var st string
	conn.QueryRow(`SELECT status FROM device_commands WHERE id=$1`, cmdID).Scan(&st)
	if st != "succeeded" {
		t.Errorf("status=%s", st)
	}
	if _, body = do(t, "GET", ts.URL+"/iclock/getrequest?SN="+sn, ""); body != "OK" {
		t.Errorf("queue should be empty: %q", body)
	}
}

func TestPayloadTooLarge(t *testing.T) {
	conn, s, ts := setup(t)
	s.MaxBody = 64
	register(t, conn, sn)
	if code, _ := do(t, "POST", ts.URL+"/iclock/cdata?SN="+sn+"&table=ATTLOG", strings.Repeat("1\t2026-10-19 08:00:00\n", 10)); code != 413 {
		t.Errorf("code=%d want 413", code)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
