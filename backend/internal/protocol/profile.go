package protocol

import (
	"fmt"
	"strings"

	"github.com/your-org/adms/internal/config"
)

// Profile holds every response detail that is known to differ between
// firmware builds. Defaults follow the generic PUSH documentation and are
// UNVERIFIED for the MB10-VL until confirmed with captures from a real unit.
type Profile struct {
	Name          string
	Verified      bool   // set ADMS_PROFILE_VERIFIED=true only after physical acceptance testing
	LineEnding    string // "\n" or "\r\n"
	AckStyle      string // "ok" → "OK"; "ok_count" → "OK: <n>"
	ErrorDelay    int    // seconds the device waits after a failed request
	Delay         int    // seconds between heartbeats
	TransTimes    string // scheduled bulk-upload times, e.g. "00:00;14:05"
	TransInterval int    // minutes between upload checks
	TransFlag     string // which tables the device should push
	Realtime      bool   // push each punch immediately
	Encrypt       bool
	TimeZone      int    // hours offset sent to the device
	ExtraOptions  string // extra raw "Key=Value" lines separated by ";" for firmware-specific keys
}

// ProfileFromEnv builds the profile from ADMS_* environment variables.
func ProfileFromEnv() Profile {
	le := "\n"
	if config.String("ADMS_LINE_ENDING", "lf") == "crlf" {
		le = "\r\n"
	}
	return Profile{
		Name:          config.String("ADMS_PROFILE_NAME", "zk-push-generic"),
		Verified:      config.Bool("ADMS_PROFILE_VERIFIED", false),
		LineEnding:    le,
		AckStyle:      config.String("ADMS_ACK_STYLE", "ok"),
		ErrorDelay:    config.Int("ADMS_ERROR_DELAY", 30),
		Delay:         config.Int("ADMS_DELAY", 10),
		TransTimes:    config.String("ADMS_TRANS_TIMES", "00:00;14:05"),
		TransInterval: config.Int("ADMS_TRANS_INTERVAL", 1),
		TransFlag:     config.String("ADMS_TRANS_FLAG", "1111000000"),
		Realtime:      config.Bool("ADMS_REALTIME", true),
		Encrypt:       false, // encryption variants are undocumented here; never enable blindly
		TimeZone:      config.Int("ADMS_DEVICE_TZ_HOURS", 8),
		ExtraOptions:  config.String("ADMS_EXTRA_OPTIONS", ""),
	}
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

func stampOr(s string) string {
	if s == "" {
		return "None"
	}
	return s
}

// HandshakeResponse builds the body returned to GET /iclock/cdata?options=all.
// Stamps tell the device which records the server already holds; "None"
// asks it to send everything it has, which is how a newly registered device
// back-fills punches made while it was unknown.
func (p Profile) HandshakeResponse(serial, attlogStamp, operlogStamp string) string {
	lines := []string{
		"GET OPTION FROM: " + serial,
		"ATTLOGStamp=" + stampOr(attlogStamp),
		"OPERLOGStamp=" + stampOr(operlogStamp),
		"ATTPHOTOStamp=None",
		fmt.Sprintf("ErrorDelay=%d", p.ErrorDelay),
		fmt.Sprintf("Delay=%d", p.Delay),
		"TransTimes=" + p.TransTimes,
		fmt.Sprintf("TransInterval=%d", p.TransInterval),
		"TransFlag=" + p.TransFlag,
		fmt.Sprintf("TimeZone=%d", p.TimeZone),
		fmt.Sprintf("Realtime=%d", b2i(p.Realtime)),
		fmt.Sprintf("Encrypt=%d", b2i(p.Encrypt)),
	}
	for _, kv := range strings.Split(p.ExtraOptions, ";") {
		if kv = strings.TrimSpace(kv); strings.Contains(kv, "=") {
			lines = append(lines, kv)
		}
	}
	return strings.Join(lines, p.LineEnding) + p.LineEnding
}

// UploadAck is the body that tells the device a batch was stored. It must
// only be sent after the batch is durably committed.
func (p Profile) UploadAck(n int) string {
	if p.AckStyle == "ok_count" {
		return fmt.Sprintf("OK: %d", n)
	}
	return "OK"
}

// IdleResponse is returned to getrequest when no command is queued.
func (p Profile) IdleResponse() string { return "OK" }

// CommandResponse formats a queued command for getrequest.
func (p Profile) CommandResponse(id int64, cmd string) string {
	return fmt.Sprintf("C:%d:%s", id, cmd) + p.LineEnding
}
