// Command tidyfleet-server runs the Tidyfleet ingest and dashboard API.
//
//	tidyfleet-server serve                     run the API (default)
//	tidyfleet-server migrate                   apply database migrations and exit
//	tidyfleet-server create-org -name N -admin-email E [-admin-password P]
package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tidyfleet/tidyfleet/server/internal/alerts"
	"github.com/tidyfleet/tidyfleet/server/internal/api"
	"github.com/tidyfleet/tidyfleet/server/internal/db"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cmd, args := "serve", os.Args[1:]
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	var err error
	switch cmd {
	case "serve":
		err = serve(ctx, logger)
	case "migrate":
		var pool *pgxpool.Pool
		if pool, err = open(ctx); err == nil {
			pool.Close()
			fmt.Println("Migrations applied.")
		}
	case "create-org":
		err = createOrg(ctx, args)
	default:
		err = fmt.Errorf("unknown command %q (serve, migrate, create-org)", cmd)
	}
	if err != nil {
		logger.Error(cmd+" failed", "err", err)
		os.Exit(1)
	}
}

func open(ctx context.Context) (*pgxpool.Pool, error) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		return nil, errors.New("DATABASE_URL is not set")
	}
	pool, err := db.Connect(ctx, url)
	if err != nil {
		return nil, err
	}
	if err := db.Migrate(ctx, pool); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}

func serve(ctx context.Context, logger *slog.Logger) error {
	pool, err := open(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()

	engine := &alerts.Engine{
		DB:       pool,
		Log:      logger,
		HTTP:     &http.Client{Timeout: 10 * time.Second},
		LatestOS: parseLatestOS(os.Getenv("TIDYFLEET_LATEST_OS")),
	}
	srv := &api.Server{DB: pool, Alerts: engine, Log: logger, TrustProxy: os.Getenv("TIDYFLEET_TRUST_PROXY") == "1"}

	go engine.Run(ctx, 5*time.Minute)
	go maintain(ctx, pool, logger, envInt("TIDYFLEET_RETENTION_DAYS", 365))

	addr := os.Getenv("LISTEN_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	hs := &http.Server{
		Addr:              addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	errc := make(chan error, 1)
	go func() { errc <- hs.ListenAndServe() }()
	logger.Info("listening", "addr", addr)
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return hs.Shutdown(shutdownCtx)
}

// maintain removes expired sessions and snapshots past the retention period.
func maintain(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger, retentionDays int) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		if _, err := pool.Exec(ctx, `DELETE FROM sessions WHERE expires_at < now()`); err != nil && ctx.Err() == nil {
			logger.Error("session cleanup", "err", err)
		}
		if retentionDays > 0 {
			tag, err := pool.Exec(ctx, `DELETE FROM device_snapshots WHERE taken_at < now() - make_interval(days => $1)`, retentionDays)
			if err != nil && ctx.Err() == nil {
				logger.Error("snapshot retention", "err", err)
			} else if n := tag.RowsAffected(); n > 0 {
				logger.Info("snapshot retention", "deleted", n)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// parseLatestOS reads "macos=26,windows=11".
func parseLatestOS(s string) map[string]int {
	out := map[string]int{}
	for _, part := range strings.Split(s, ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if n, err := strconv.Atoi(v); ok && err == nil {
			out[strings.TrimSpace(k)] = n
		}
	}
	return out
}

func envInt(key string, def int) int {
	if n, err := strconv.Atoi(os.Getenv(key)); err == nil {
		return n
	}
	return def
}

func createOrg(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("create-org", flag.ContinueOnError)
	name := fs.String("name", "", "organization name")
	email := fs.String("admin-email", "", "email of the first admin")
	password := fs.String("admin-password", os.Getenv("TIDYFLEET_ADMIN_PASSWORD"), "admin password (default: $TIDYFLEET_ADMIN_PASSWORD, or generated)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	generated := *password == ""
	if generated {
		b := make([]byte, 12)
		if _, err := rand.Read(b); err != nil {
			return err
		}
		*password = base64.RawURLEncoding.EncodeToString(b)
	}
	pool, err := open(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()
	orgID, code, err := api.CreateOrg(ctx, pool, *name, *email, *password)
	if err != nil {
		return err
	}
	fmt.Printf("Created organization %q (%s)\n", *name, orgID)
	fmt.Printf("  Admin login:      %s\n", *email)
	if generated {
		fmt.Printf("  Admin password:   %s   (shown once; store it safely)\n", *password)
	}
	fmt.Printf("  Enrollment code:  %s\n\n", code)
	fmt.Printf("Enroll a laptop with:\n  tidyfleet enroll <server-url> %s\n", code)
	return nil
}
