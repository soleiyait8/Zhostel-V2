// Command api serves the authenticated application API for the HR dashboard.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
	_ "time/tzdata" // embed zone data; the runtime image has none

	"github.com/your-org/adms/internal/api"
	"github.com/your-org/adms/internal/auth"
	"github.com/your-org/adms/internal/config"
	"github.com/your-org/adms/internal/db"
	"github.com/your-org/adms/internal/httpx"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	dsn, err := config.Required("DATABASE_URL")
	if err != nil {
		log.Error(err.Error())
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	conn, err := db.Open(ctx, dsn)
	if err != nil {
		log.Error("database", "err", err)
		os.Exit(1)
	}
	trust := config.Bool("TRUST_PROXY", true)
	app := &api.App{
		DB:  conn,
		Log: log,
		Auth: &auth.Service{DB: conn, TTL: config.Duration("SESSION_TTL", 12*time.Hour),
			SecureCookie: config.Bool("COOKIE_SECURE", true), TrustProxy: trust,
			LoginLimiter: httpx.NewRateLimiter(10, 15*time.Minute)},
		TrustProxy:      trust,
		ADMSInternalURL: config.String("ADMS_INTERNAL_URL", "http://adms:8081"),
		ADMSPublicHost:  config.String("ADMS_PUBLIC_HOST", "adms.example.com"),
		ADMSPublicPort:  config.Int("ADMS_PUBLIC_PORT", 443),
		ADMSPublicHTTPS: config.Bool("ADMS_PUBLIC_HTTPS", true),
		HTTPClient:      &http.Client{Timeout: 3 * time.Second},
	}
	srv := &http.Server{Addr: config.String("API_ADDR", ":8080"), Handler: app.Routes(),
		ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 60 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		srv.Shutdown(sctx)
	}()
	log.Info("api listening", "addr", srv.Addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("server", "err", err)
		os.Exit(1)
	}
}
