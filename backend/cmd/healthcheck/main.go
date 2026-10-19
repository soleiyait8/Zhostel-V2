// Command healthcheck is used by container health checks in images that have
// no shell or curl. It exits 0 when the URL answers 200.
//
//	healthcheck http://127.0.0.1:8080/healthz
package main

import (
	"net/http"
	"os"
	"time"
)

func main() {
	url := "http://127.0.0.1:8080/healthz"
	if len(os.Args) > 1 {
		url = os.Args[1]
	}
	c := &http.Client{Timeout: 3 * time.Second}
	resp, err := c.Get(url)
	if err != nil || resp.StatusCode != 200 {
		os.Exit(1)
	}
}
