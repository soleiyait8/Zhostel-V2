package protocol

import (
	"bytes"
	"net/url"
	"strings"
	"testing"
	"time"
)

// All payloads in this file are SIMULATED. They follow the documented PUSH
// format and must be replaced/augmented with captures from the physical
// MB10-VL before production acceptance (see testdata/README.md).

func TestClassify(t *testing.T) {
	cases := []struct {
		m, p string
		want Kind
	}{
		{"GET", "/iclock/cdata", KindHandshake},
		{"GET", "/iclock/cdata.aspx", KindHandshake},
		{"POST", "/iclock/cdata", KindUpload},
		{"GET", "/iclock/getrequest", KindGetRequest},
		{"POST", "/iclock/devicecmd", KindDeviceCmd},
		{"PUT", "/iclock/cdata", KindUnknown},
		{"GET", "/iclock/../etc/passwd", KindUnknown},
		{"GET", "/api/devices", KindUnknown},
	}
	for _, c := range cases {
		if got := Classify(c.m, c.p); got != c.want {
			t.Errorf("Classify(%s %s)=%v want %v", c.m, c.p, got, c.want)
		}
	}
}

func TestValidateSerial(t *testing.T) {
	for _, ok := range []string{"CQZ7224460123", "ABCD", "MB10_VL-01"} {
		if ValidateSerial(ok) != nil {
			t.Errorf("%q should be valid", ok)
		}
	}
	for _, bad := range []string{"", "abc", "AB CD", "x'; DROP TABLE", strings.Repeat("A", 33)} {
		if ValidateSerial(bad) == nil {
			t.Errorf("%q should be invalid", bad)
		}
	}
}

func TestParseAttLog_MixedLineEndingsAndBOM(t *testing.T) {
	body := []byte("\xEF\xBB\xBF1001\t2026-10-19 08:01:22\t0\t1\t0\t0\t0\r\n" +
		"1002\t2026-10-19 08:03:05\t0\t15\t\t0\t0\r" +
		"1001\t2026-10-19 17:30:00\t1\t1\t0\t0\t0\n\n")
	res := ParseAttLog(body)
	if len(res) != 3 {
		t.Fatalf("got %d lines, want 3", len(res))
	}
	for _, r := range res {
		if r.Log == nil {
			t.Fatalf("line %d rejected: %s", r.LineNo, r.Reason)
		}
	}
	a := res[0].Log
	if a.UserID != "1001" || a.Status != "0" || a.Verify != "1" {
		t.Errorf("unexpected fields %+v", a)
	}
	want := time.Date(2026, 10, 19, 8, 1, 22, 0, time.UTC)
	if !a.Wall.Equal(want) {
		t.Errorf("wall=%v want %v", a.Wall, want)
	}
	if res[1].Log.Verify != "15" || res[1].Log.WorkCode != "" {
		t.Errorf("line 2 fields %+v", res[1].Log)
	}
}

func TestParseAttLog_MinimalTwoFields(t *testing.T) {
	r := ParseAttLogLine(1, "7\t2026-01-02 03:04:05")
	if r.Log == nil || r.Log.Status != "" {
		t.Fatalf("expected accepted with empty status, got %+v", r)
	}
}

func TestParseAttLog_Rejections(t *testing.T) {
	cases := map[string]string{
		"1001 2026-10-19 08:01:22 0 1":                          "not_tab_separated",
		"10 01\t2026-10-19 08:01:22":                            "invalid_user_id",
		"1001\t19/10/2026 08:01":                                "invalid_timestamp",
		"1001\t2026-02-30 08:00:00":                             "invalid_timestamp",
		"1001\t1999-12-31 23:59:59":                             "timestamp_out_of_range",
		"1001\t2026-10-19 08:01:22\t" + strings.Repeat("9", 20): "field_too_long",
		strings.Repeat("x", 600):                                "line_too_long",
	}
	for line, reason := range cases {
		r := ParseAttLogLine(1, line)
		if r.Log != nil || r.Reason != reason {
			t.Errorf("%q: got reason %q want %q", line[:min(len(line), 30)], r.Reason, reason)
		}
	}
}

func TestInstantUsesDeviceTimezone(t *testing.T) {
	manila, _ := time.LoadLocation("Asia/Manila")
	wall := time.Date(2026, 10, 19, 0, 30, 0, 0, time.UTC)
	got := Instant(wall, manila).UTC()
	want := time.Date(2026, 10, 18, 16, 30, 0, 0, time.UTC) // UTC+8, previous calendar day
	if !got.Equal(want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestPayloadHashStable(t *testing.T) {
	a := PayloadHash("SN1", "1001\t2026-10-19 08:01:22\t0\t1")
	b := PayloadHash("SN1", "1001\t2026-10-19 08:01:22\t0\t1  ")
	c := PayloadHash("SN2", "1001\t2026-10-19 08:01:22\t0\t1")
	if !bytes.Equal(a, b) {
		t.Error("trailing whitespace should not change hash")
	}
	if bytes.Equal(a, c) {
		t.Error("different devices must hash differently")
	}
}

func TestParseUserLinesAndIgnoreTemplates(t *testing.T) {
	body := []byte("USER PIN=1001\tName=Juan Dela Cruz\tPri=0\tPasswd=\tCard=\tGrp=1\n" +
		"FP PIN=1001\tFID=6\tSize=1024\tValid=1\tTMP=AAAAQUJDREVGRw==\n" +
		"OPLOG 4\t0\t2026-10-19 08:00:00\t1001\t0\t0\t0\n" +
		"USER PIN=bad pin\tName=x\n")
	users := ParseUserLines(body)
	if len(users) != 1 || users[0].UserID != "1001" || users[0].Name != "Juan Dela Cruz" {
		t.Fatalf("got %+v", users)
	}
}

func TestRedactRemovesBiometrics(t *testing.T) {
	body := []byte("USER PIN=1\tName=A\nFP PIN=1\tTMP=SECRETTEMPLATE\nFACE PIN=1\tTMP=SECRETFACE\n")
	out := Redact("OPERLOG", body, 4096)
	if strings.Contains(out, "SECRET") {
		t.Fatalf("template leaked: %s", out)
	}
	if !strings.Contains(out, "USER PIN=1") {
		t.Error("non-biometric line should be kept")
	}
	if got := Redact("ATTPHOTO", []byte{0xff, 0xd8, 0, 1}, 4096); strings.Contains(got, "\xff") {
		t.Error("photo bytes leaked")
	}
	long := Redact("ATTLOG", bytes.Repeat([]byte("a"), 100), 10)
	if !strings.Contains(long, "truncated") {
		t.Error("expected truncation marker")
	}
}

func TestHandshakeResponse(t *testing.T) {
	p := Profile{LineEnding: "\r\n", ErrorDelay: 30, Delay: 10, TransTimes: "00:00;14:05",
		TransInterval: 1, TransFlag: "1111000000", Realtime: true, TimeZone: 8,
		ExtraOptions: "ServerVer=2.4.1; bogus ;"}
	out := p.HandshakeResponse("CQZ1", "", "42")
	for _, want := range []string{"GET OPTION FROM: CQZ1\r\n", "ATTLOGStamp=None\r\n",
		"OPERLOGStamp=42\r\n", "Realtime=1\r\n", "TimeZone=8\r\n", "ServerVer=2.4.1\r\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "bogus") {
		t.Error("invalid extra option should be dropped")
	}
}

func TestAckStyles(t *testing.T) {
	if (Profile{AckStyle: "ok"}).UploadAck(5) != "OK" {
		t.Error("ok style")
	}
	if (Profile{AckStyle: "ok_count"}).UploadAck(5) != "OK: 5" {
		t.Error("ok_count style")
	}
}

func TestParseCommandResults(t *testing.T) {
	res := ParseCommandResults([]byte("ID=12&Return=0&CMD=DATA\nID=x&Return=0\n\nID=13&Return=-1&CMD=INFO"))
	if len(res) != 2 || res[0].ID != 12 || res[1].Return != "-1" {
		t.Fatalf("got %+v", res)
	}
}

func TestFirmwareFromInfo(t *testing.T) {
	q := url.Values{"INFO": {"Ver 6.60 Apr 2 2019,1,0,12,192.168.1.20,10,7,12,0,111"}}
	if got := FirmwareFromInfo(q); got != "Ver 6.60 Apr 2 2019" {
		t.Errorf("got %q", got)
	}
}
