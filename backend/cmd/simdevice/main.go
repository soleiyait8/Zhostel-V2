// Command simdevice sends SIMULATED ADMS traffic for development. It mimics
// the documented PUSH flow (handshake, heartbeat, ATTLOG upload). It is not a
// substitute for testing with the physical MB10-VL.
//
//	simdevice -url http://localhost:8081 -sn SIMMB10VL0001 -users 1001,1002,1003,9999
package main

import (
	"flag"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"strings"
	"time"
)

func main() {
	base := flag.String("url", "http://localhost:8081", "ADMS service base URL")
	sn := flag.String("sn", "SIMMB10VL0001", "serial number to present")
	users := flag.String("users", "1001,1002,1003,1004,9999", "device user IDs to punch")
	tz := flag.String("tz", "Asia/Manila", "device wall-clock timezone")
	replay := flag.Bool("replay", true, "resend the batch to demonstrate duplicate handling")
	flag.Parse()

	loc, err := time.LoadLocation(*tz)
	if err != nil {
		fail(err)
	}
	fmt.Println("== SIMULATED DEVICE TRAFFIC — not from real hardware ==")
	call("GET", *base+"/iclock/cdata?SN="+*sn+"&options=all&pushver=2.4.1&language=69", "")
	call("GET", *base+"/iclock/getrequest?SN="+*sn+"&INFO=SIM%201.0,4,0,0,127.0.0.1,0,0,0,0,111", "")

	today := time.Now().In(loc)
	day := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, loc)
	var lines []string
	for i, u := range strings.Split(*users, ",") {
		in := day.Add(7*time.Hour + 45*time.Minute + time.Duration(i*7+rand.Intn(20))*time.Minute)
		lines = append(lines, fmt.Sprintf("%s\t%s\t0\t15\t0\t0\t0", u, in.Format("2006-01-02 15:04:05")))
		if out := day.Add(17*time.Hour + time.Duration(rand.Intn(40))*time.Minute); out.Before(time.Now()) {
			lines = append(lines, fmt.Sprintf("%s\t%s\t1\t15\t0\t0\t0", u, out.Format("2006-01-02 15:04:05")))
		}
	}
	lines = append(lines, "malformed record for rejection testing")
	body := strings.Join(lines, "\r\n") + "\r\n"
	stamp := fmt.Sprint(time.Now().Unix())
	url := *base + "/iclock/cdata?SN=" + *sn + "&table=ATTLOG&Stamp=" + stamp
	call("POST", url, body)
	if *replay {
		fmt.Println("-- replaying the same batch (should be acknowledged, not duplicated)")
		call("POST", url, body)
	}
}

func call(method, url, body string) {
	req, _ := http.NewRequest(method, url, strings.NewReader(body))
	req.Header.Set("Content-Type", "text/plain")
	req.Header.Set("User-Agent", "simdevice (SIMULATED)")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fail(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	fmt.Printf("%s %s\n  -> %d %q\n", method, url, resp.StatusCode, strings.TrimSpace(string(b)))
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
