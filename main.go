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
	"github.com/manzanit0/athenaeum/internal/home"
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
	// home is the folder that remembers the libraries. It is also the
	// first library, until another folder is opened in the interface.
	home string
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
	// modelKey is the Anthropic API key books are restored with until
	// one is given in the interface.
	modelKey string
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
		home:         env("ATHENAEUM_HOME", "data"),
		kindlePath:   env("ATHENAEUM_KINDLE_PATH", ""),
		secret:       env("ATHENAEUM_SECRET", ""),
		megaFolder:   env("ATHENAEUM_MEGA_FOLDER", ""),
		ebookConvert: env("ATHENAEUM_EBOOK_CONVERT", "ebook-convert"),
		ebookMeta:    env("ATHENAEUM_EBOOK_META", "ebook-meta"),
		converter:    env("ATHENAEUM_CONVERTER", "native"),
		modelKey:     env("ANTHROPIC_API_KEY", ""),

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

// openLibrary wires together the server of the library in the folder
// dir, which must exist, and reads what is in it. It returns, with the
// server, what closes the library's database.
func (cfg config) openLibrary(dir string) (*api.Server, func(), error) {
	st, err := store.Open(filepath.Join(dir, home.Catalogue))
	if err != nil {
		return nil, nil, err
	}
	srv, err := cfg.newServer(dir, st)
	if err != nil {
		st.Close()
		return nil, nil, err
	}
	found, err := srv.Library.Rescan(context.Background())
	if err != nil {
		st.Close()
		return nil, nil, fmt.Errorf("reading the library folder: %w", err)
	}
	slog.Info("a library was opened", "folder", dir,
		"added", found.Added, "updated", found.Updated, "removed", found.Removed)
	return srv, func() { st.Close() }, nil
}

// newServer builds the server of the library in dir on the database st.
func (cfg config) newServer(dir string, st *store.Store) (*api.Server, error) {
	if err := st.FailRunningJobs(); err != nil {
		return nil, err
	}
	tools := &convert.Tools{
		ConvertBin: cfg.ebookConvert, MetaBin: cfg.ebookMeta,
		PreferCalibre: cfg.converter == "calibre",
	}
	lib, err := library.New(filepath.Join(dir, "library"), filepath.Join(dir, "tmp"), st, tools.ExtractCover)
	if err != nil {
		return nil, err
	}
	secrets, err := vault.Open(st, dir, cfg.secret)
	if err != nil {
		return nil, fmt.Errorf("opening the vault: %w", err)
	}
	accounts := &megasync.Accounts{Service: megaService(), Secrets: secrets}
	srv := &api.Server{
		Store:    st,
		Library:  lib,
		Tools:    tools,
		Syncer:   &megasync.Syncer{Accounts: accounts, Store: st, Library: lib, Trash: filepath.Join(dir, "trash")},
		Previews: &preview.Cache{Dir: filepath.Join(dir, "cache", "preview")},
		Secrets:  secrets,
		Model:    restoreModel(),
		Covers:   coverFinder(),
		ModelKey: cfg.modelKey,
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
	return srv, nil
}

// stopWhenClosed calls stop once r has nothing more to read, which for
// a pipe means that whoever held its other end closed it or exited.
func stopWhenClosed(r io.Reader, stop func()) {
	_, _ = io.Copy(io.Discard, r)
	stop()
}

// serve runs the HTTP server until the process is asked to stop. A
// library that cannot be opened does not stop it: the interface then
// asks which library to open instead.
func serve(cfg config) error {
	known, err := home.Open(cfg.home)
	if err != nil {
		return fmt.Errorf("opening the home folder: %w", err)
	}
	host := &api.Host{Home: known, Open: cfg.openLibrary, UI: web.FS()}
	host.Start()
	defer host.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if cfg.stopOnStdinClose {
		go stopWhenClosed(os.Stdin, stop)
	}
	httpServer := &http.Server{Addr: cfg.addr, Handler: host.Handler(), ReadHeaderTimeout: 10 * time.Second}
	failed := make(chan error, 1)
	go func() {
		failed <- httpServer.ListenAndServe()
	}()
	slog.Info("athenaeum is listening", "addr", cfg.addr, "home", known.Dir(), "version", version)
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
