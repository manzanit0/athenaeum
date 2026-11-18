// Command athenaeum runs the ebook library server: a JSON API with an
// embedded web interface.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/manzanit0/athenaeum/internal/api"
	"github.com/manzanit0/athenaeum/internal/convert"
	"github.com/manzanit0/athenaeum/internal/device"
	"github.com/manzanit0/athenaeum/internal/library"
	"github.com/manzanit0/athenaeum/internal/megasync"
	"github.com/manzanit0/athenaeum/internal/preview"
	"github.com/manzanit0/athenaeum/internal/store"
	"github.com/manzanit0/athenaeum/internal/vault"
	"github.com/manzanit0/athenaeum/web"
)

// version identifies the build and is set at link time.
var version = "dev"

// shutdownTimeout bounds how long in-flight requests may take to finish
// when the server stops.
const shutdownTimeout = 10 * time.Second

// config holds the settings read from the environment.
type config struct {
	// addr is the address the HTTP server listens on.
	addr string
	// dataDir holds the database and the library folder.
	dataDir string
	// kindlePath is the default Kindle mount point; empty means detect it.
	kindlePath string
	// secret is the passphrase that protects what is kept encrypted in
	// the database; empty means a key file in the data folder is used.
	secret string
	// megaFolder is the default MEGA folder to sync with.
	megaFolder string
	// ebookConvert is the calibre ebook-convert binary.
	ebookConvert string
	// ebookMeta is the calibre ebook-meta binary.
	ebookMeta string
	// converter is "native" to use the built in converters where they
	// apply, or "calibre" to prefer calibre whenever it is installed.
	converter string
	// stopOnStdinClose stops the server once its standard input closes,
	// so a program that starts it, such as the macOS app, takes it down
	// when it exits, however it exits.
	stopOnStdinClose bool
}

// env returns the value of the environment variable key, or fallback when
// it is unset or empty.
func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

// loadConfig reads the configuration from the environment.
func loadConfig() config {
	return config{
		addr:         env("ATHENAEUM_ADDR", ":8080"),
		dataDir:      env("ATHENAEUM_DATA_DIR", "data"),
		kindlePath:   env("ATHENAEUM_KINDLE_PATH", ""),
		secret:       env("ATHENAEUM_SECRET", ""),
		megaFolder:   env("ATHENAEUM_MEGA_FOLDER", ""),
		ebookConvert: env("ATHENAEUM_EBOOK_CONVERT", "ebook-convert"),
		ebookMeta:    env("ATHENAEUM_EBOOK_META", "ebook-meta"),
		converter:    env("ATHENAEUM_CONVERTER", "native"),

		stopOnStdinClose: env("ATHENAEUM_STOP_ON_STDIN_CLOSE", "") == "1",
	}
}

// main starts the server, or checks a running one with -healthcheck.
func main() {
	healthcheck := flag.Bool("healthcheck", false, "check that the server is healthy and exit")
	flag.Parse()
	cfg := loadConfig()
	run := serve
	if *healthcheck {
		run = checkHealth
	}
	if err := run(cfg); err != nil {
		slog.Error("athenaeum failed", "error", err)
		os.Exit(1)
	}
}

// checkHealth succeeds when the server listening on cfg.addr is healthy.
func checkHealth(cfg config) error {
	_, port, err := net.SplitHostPort(cfg.addr)
	if err != nil {
		return err
	}
	client := http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://" + net.JoinHostPort("localhost", port) + "/api/health")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unhealthy: %s", resp.Status)
	}
	return nil
}

// newServer wires the application together from cfg.
func newServer(cfg config) (*api.Server, *store.Store, error) {
	if err := os.MkdirAll(cfg.dataDir, 0o755); err != nil {
		return nil, nil, err
	}
	st, err := store.Open(filepath.Join(cfg.dataDir, "athenaeum.db"))
	if err != nil {
		return nil, nil, err
	}
	if err := st.FailRunningJobs(); err != nil {
		st.Close()
		return nil, nil, err
	}
	tools := &convert.Tools{
		ConvertBin: cfg.ebookConvert, MetaBin: cfg.ebookMeta,
		PreferCalibre: cfg.converter == "calibre",
	}
	lib, err := library.New(filepath.Join(cfg.dataDir, "library"), filepath.Join(cfg.dataDir, "tmp"), st, tools.ExtractCover)
	if err != nil {
		st.Close()
		return nil, nil, err
	}
	secrets, err := vault.Open(st, cfg.dataDir, cfg.secret)
	if err != nil {
		st.Close()
		return nil, nil, fmt.Errorf("opening the vault: %w", err)
	}
	accounts := &megasync.Accounts{Service: megaService(), Secrets: secrets}
	srv := &api.Server{
		Store:    st,
		Library:  lib,
		Tools:    tools,
		Syncer:   &megasync.Syncer{Accounts: accounts, Store: st, Library: lib},
		Previews: &preview.Cache{Dir: filepath.Join(cfg.dataDir, "cache", "preview")},
		Defaults: map[string]string{
			api.SettingMegaFolder: megasync.Normalize(cfg.megaFolder),
			api.SettingKindlePath: cfg.kindlePath,
		},
		UI:      web.FS(),
		Version: version,
	}
	srv.Kindle = device.New(func() string {
		path, err := st.Setting(api.SettingKindlePath, cfg.kindlePath)
		if err != nil {
			return cfg.kindlePath
		}
		return path
	})
	return srv, st, nil
}

// stopWhenClosed calls stop once r has nothing more to read, which for
// a pipe means that whoever held its other end closed it or exited.
func stopWhenClosed(r io.Reader, stop func()) {
	_, _ = io.Copy(io.Discard, r)
	stop()
}

// serve runs the HTTP server until the process is asked to stop.
func serve(cfg config) error {
	srv, st, err := newServer(cfg)
	if err != nil {
		return err
	}
	defer st.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if cfg.stopOnStdinClose {
		go stopWhenClosed(os.Stdin, stop)
	}
	found, err := srv.Library.Rescan(ctx)
	if err != nil {
		return fmt.Errorf("reading the library folder: %w", err)
	}
	slog.Info("the library folder was read", "added", found.Added, "updated", found.Updated, "removed", found.Removed)
	httpServer := &http.Server{Addr: cfg.addr, Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second}
	failed := make(chan error, 1)
	go func() {
		failed <- httpServer.ListenAndServe()
	}()
	slog.Info("athenaeum is listening", "addr", cfg.addr, "data", cfg.dataDir, "version", version)
	select {
	case err := <-failed:
		return err
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
