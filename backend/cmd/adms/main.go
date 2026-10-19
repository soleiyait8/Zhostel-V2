// Command adms is the device-facing ADMS protocol service. It runs on its own
// so punches are received even when the dashboard or API is down.
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

	"github.com/your-org/adms/internal/adms"
	"github.com/your-org/adms/internal/config"
	"github.com/your-org/adms/internal/db"
	"github.com/your-org/adms/internal/httpx"
	"github.com/your-org/adms/internal/protocol"
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
	profile := protocol.ProfileFromEnv()
	if !profile.Verified {
		log.Warn("ADMS protocol profile is UNVERIFIED for this device model; capture real traffic and confirm before production use",
			"profile", profile.Name)
	}
	s := &adms.Server{
		DB: conn, Profile: profile, Log: log,
		MaxBody:         int64(config.Int("ADMS_MAX_BODY_BYTES", 2<<20)),
		TrustProxy:      config.Bool("TRUST_PROXY", true),
		EnableCommands:  config.Bool("ADMS_ENABLE_COMMANDS", false),
		LogHeartbeats:   config.Bool("ADMS_LOG_HEARTBEATS", false),
		FutureTolerance: config.Duration("ADMS_FUTURE_TOLERANCE", 24*time.Hour),
		Limiter:         httpx.NewRateLimiter(config.Int("ADMS_RATE_LIMIT_PER_MIN", 300), time.Minute),
		Now:             time.Now,
	}
	go retention(ctx, s, log, config.Int("ADMS_COMM_LOG_RETENTION_DAYS", 30))

	srv := &http.Server{Addr: config.String("ADMS_ADDR", ":8081"), Handler: s.Handler(),
		ReadHeaderTimeout: 15 * time.Second, ReadTimeout: 60 * time.Second, WriteTimeout: 60 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		srv.Shutdown(sctx)
	}()
	log.Info("adms listening", "addr", srv.Addr, "profile", profile.Name, "verified", profile.Verified)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("server", "err", err)
		os.Exit(1)
	}
}

// retention prunes communication logs and marks silent devices offline.
// Raw attendance logs are never pruned.
func retention(ctx context.Context, s *adms.Server, log *slog.Logger, days int) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	last := time.Time{}
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		s.DB.ExecContext(ctx, `UPDATE biometric_devices SET connection_status='offline'
			WHERE connection_status='online' AND last_seen_at < now() - make_interval(secs =>
			  coalesce((SELECT (value #>> '{}')::int FROM app_config WHERE key='device_online_window_seconds'), 300))`)
		if time.Since(last) > time.Hour {
			res, err := s.DB.ExecContext(ctx, `DELETE FROM device_communication_logs WHERE created_at < now() - make_interval(days => $1)`, days)
			if err == nil {
				n, _ := res.RowsAffected()
				log.Info("comm log retention", "deleted", n)
			}
			last = time.Now()
		}
	}
}
