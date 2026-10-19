// Package protocol is the ADMS ("PUSH") wire-format adapter.
//
// It is deliberately free of HTTP servers and databases so it can be tested in
// isolation and replaced or tuned per firmware.
//
// VERIFICATION STATUS
// The behaviour below follows the publicly documented ZKTeco PUSH protocol
// (endpoints under /iclock/, tab-separated ATTLOG records, "GET OPTION FROM"
// handshake). It has NOT yet been verified against an MB10-VL. Everything that
// is known to vary between firmware builds is a field of Profile so it can be
// changed from configuration once real captures are available. See
// docs/PROTOCOL_NOTES.md for the capture-and-verify procedure.
package protocol

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Kind is the type of device request.
type Kind int

const (
	KindUnknown    Kind = iota
	KindHandshake       // GET  /iclock/cdata?SN=…&options=all
	KindUpload          // POST /iclock/cdata?SN=…&table=…
	KindGetRequest      // GET  /iclock/getrequest?SN=…   (heartbeat / command poll)
	KindDeviceCmd       // POST /iclock/devicecmd?SN=…    (command results)
)

func (k Kind) String() string {
	return [...]string{"unknown", "handshake", "upload", "getrequest", "devicecmd"}[k]
}

// Classify maps an HTTP method and path to a request kind. Some firmware
// appends ".aspx" to the endpoint names, so both spellings are accepted.
func Classify(method, path string) Kind {
	p := strings.TrimSuffix(strings.TrimSuffix(path, "/"), ".aspx")
	switch {
	case p == "/iclock/cdata" && method == "GET":
		return KindHandshake
	case p == "/iclock/cdata" && method == "POST":
		return KindUpload
	case p == "/iclock/getrequest" && method == "GET":
		return KindGetRequest
	case p == "/iclock/devicecmd" && method == "POST":
		return KindDeviceCmd
	}
	return KindUnknown
}

var serialRe = regexp.MustCompile(`^[A-Za-z0-9_-]{4,32}$`)
var userIDRe = regexp.MustCompile(`^[A-Za-z0-9]{1,24}$`)

// ValidateSerial checks a device serial number taken from the SN parameter.
func ValidateSerial(sn string) error {
	if !serialRe.MatchString(sn) {
		return errors.New("serial number must be 4–32 characters of A–Z, 0–9, _ or -")
	}
	return nil
}

// ValidUserID reports whether s is a plausible device user ID (PIN).
func ValidUserID(s string) bool { return userIDRe.MatchString(s) }

// FirmwareFromInfo extracts the firmware version from the INFO query
// parameter some firmware sends on getrequest ("Ver 6.60 Apr 2 2019,…").
// Returns "" when absent.
func FirmwareFromInfo(q url.Values) string {
	info := q.Get("INFO")
	if info == "" {
		return ""
	}
	first, _, _ := strings.Cut(info, ",")
	first = strings.TrimSpace(first)
	if len(first) > 64 {
		first = first[:64]
	}
	return first
}

// NormalizeBody strips a UTF-8 byte-order mark and converts CRLF and lone CR
// line endings to LF.
func NormalizeBody(b []byte) string {
	b = bytes.TrimPrefix(b, []byte{0xEF, 0xBB, 0xBF})
	s := strings.ReplaceAll(string(b), "\r\n", "\n")
	return strings.ReplaceAll(s, "\r", "\n")
}

// AttLog is one parsed attendance record.
//
// Field order per documented PUSH ATTLOG format:
//
//	PIN <TAB> YYYY-MM-DD HH:MM:SS <TAB> Status <TAB> Verify <TAB> WorkCode <TAB> Reserved <TAB> Reserved
//
// Status and Verify are kept as raw codes. Their meaning (check-in/out,
// fingerprint/face/card) is firmware- and configuration-dependent and must be
// confirmed on the real device before the attendance engine relies on them.
type AttLog struct {
	UserID   string
	Wall     time.Time // device wall clock, location UTC used only as a container
	Status   string
	Verify   string
	WorkCode string
	Extra    []string
}

// LineResult is the outcome of parsing one non-empty line.
type LineResult struct {
	LineNo int
	Raw    string
	Log    *AttLog // nil when Reason is set
	Reason string  // machine-readable rejection reason
}

const wallLayout = "2006-01-02 15:04:05"

// ParseAttLog parses an ATTLOG upload body. Every non-empty line yields a
// LineResult so rejected lines can be retained for audit and reprocessing.
func ParseAttLog(body []byte) []LineResult {
	var out []LineResult
	for i, line := range strings.Split(NormalizeBody(body), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		out = append(out, ParseAttLogLine(i+1, line))
	}
	return out
}

// ParseAttLogLine parses a single ATTLOG line.
func ParseAttLogLine(lineNo int, line string) LineResult {
	r := LineResult{LineNo: lineNo, Raw: line}
	if len(line) > 512 {
		r.Reason = "line_too_long"
		return r
	}
	f := strings.Split(line, "\t")
	if len(f) < 2 {
		r.Reason = "not_tab_separated"
		return r
	}
	for i := range f {
		f[i] = strings.TrimSpace(f[i])
	}
	if !ValidUserID(f[0]) {
		r.Reason = "invalid_user_id"
		return r
	}
	wall, err := time.ParseInLocation(wallLayout, f[1], time.UTC)
	if err != nil {
		r.Reason = "invalid_timestamp"
		return r
	}
	if wall.Year() < 2000 || wall.Year() > 2099 {
		r.Reason = "timestamp_out_of_range"
		return r
	}
	a := &AttLog{UserID: f[0], Wall: wall}
	get := func(i int) string {
		if i < len(f) {
			return f[i]
		}
		return ""
	}
	a.Status, a.Verify, a.WorkCode = get(2), get(3), get(4)
	if len(f) > 5 {
		a.Extra = f[5:]
	}
	for _, code := range []string{a.Status, a.Verify, a.WorkCode} {
		if len(code) > 16 {
			r.Reason = "field_too_long"
			return r
		}
	}
	r.Log = a
	return r
}

// Instant converts a device wall-clock time to an absolute instant using the
// device's configured timezone.
func Instant(wall time.Time, loc *time.Location) time.Time {
	return time.Date(wall.Year(), wall.Month(), wall.Day(),
		wall.Hour(), wall.Minute(), wall.Second(), 0, loc)
}

// PayloadHash identifies a record for idempotent storage. The same line
// re-sent by the same device always hashes identically.
func PayloadHash(serial, line string) []byte {
	h := sha256.Sum256([]byte(serial + "\n" + strings.TrimRight(line, " \t")))
	return h[:]
}

// DeviceUser is a user record from a USERINFO/OPERLOG upload.
type DeviceUser struct {
	UserID    string
	Name      string
	Privilege string
}

// ParseUserLines extracts "USER PIN=…<TAB>Name=…" records. All other record
// types (OPLOG, FP, FACE, BIODATA, …) are ignored by this function.
func ParseUserLines(body []byte) []DeviceUser {
	var out []DeviceUser
	for _, line := range strings.Split(NormalizeBody(body), "\n") {
		rest, ok := strings.CutPrefix(line, "USER ")
		if !ok {
			continue
		}
		var u DeviceUser
		for _, kv := range strings.Split(rest, "\t") {
			k, v, _ := strings.Cut(kv, "=")
			switch strings.TrimSpace(k) {
			case "PIN":
				u.UserID = strings.TrimSpace(v)
			case "Name":
				u.Name = sanitizeName(v)
			case "Pri":
				u.Privilege = strings.TrimSpace(v)
			}
		}
		if ValidUserID(u.UserID) {
			out = append(out, u)
		}
	}
	return out
}

func sanitizeName(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || r == 0xfffd {
			return -1
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if len([]rune(s)) > 64 {
		s = string([]rune(s)[:64])
	}
	return s
}

// biometricPrefixes are record types that carry fingerprint, face, palm or
// photo data. They are never persisted, including in communication logs.
var biometricPrefixes = []string{"FP ", "FACE ", "BIODATA ", "BIOPHOTO ", "USERPIC ", "FVEIN ", "PALM "}

// Redact returns a copy of an upload body that is safe to store in the
// communication log: biometric template lines and binary tables are replaced
// with a size marker, and the result is truncated to max bytes.
func Redact(table string, body []byte, max int) string {
	switch strings.ToUpper(table) {
	case "ATTPHOTO", "BIOPHOTO", "USERPIC":
		return fmt.Sprintf("<%s binary payload redacted, %d bytes>", table, len(body))
	}
	if !isText(body) {
		return fmt.Sprintf("<binary payload redacted, %d bytes>", len(body))
	}
	lines := strings.Split(NormalizeBody(body), "\n")
	for i, l := range lines {
		for _, p := range biometricPrefixes {
			if strings.HasPrefix(l, p) {
				lines[i] = fmt.Sprintf("<%s record redacted, %d bytes>", strings.TrimSpace(p), len(l))
				break
			}
		}
	}
	s := strings.Join(lines, "\n")
	if len(s) > max {
		s = s[:max] + fmt.Sprintf("\n<truncated, %d bytes total>", len(body))
	}
	return s
}

func isText(b []byte) bool {
	n := len(b)
	if n > 2048 {
		n = 2048
	}
	for _, c := range b[:n] {
		if c == 0 {
			return false
		}
	}
	return true
}

// CommandResult is one line of a devicecmd POST body: ID=…&Return=…&CMD=…
type CommandResult struct {
	ID     int64
	Return string
	Cmd    string
}

// ParseCommandResults parses a devicecmd body.
func ParseCommandResults(body []byte) []CommandResult {
	var out []CommandResult
	for _, line := range strings.Split(NormalizeBody(body), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		q, err := url.ParseQuery(strings.TrimSpace(line))
		if err != nil {
			continue
		}
		id, err := strconv.ParseInt(q.Get("ID"), 10, 64)
		if err != nil {
			continue
		}
		out = append(out, CommandResult{ID: id, Return: q.Get("Return"), Cmd: q.Get("CMD")})
	}
	return out
}
