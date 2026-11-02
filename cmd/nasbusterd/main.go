// SPDX-License-Identifier: AGPL-3.0-or-later

// Command nasbusterd serves the CrossWax NasBuster web UI and API.
// It runs unprivileged; privileged actions go through nbhelper (ADR-0002).
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/crosswax/nasbuster/internal/alerts"
	"github.com/crosswax/nasbuster/internal/api"
	"github.com/crosswax/nasbuster/internal/auth"
	"github.com/crosswax/nasbuster/internal/buildinfo"
	"github.com/crosswax/nasbuster/internal/demo"
	"github.com/crosswax/nasbuster/internal/events"
	"github.com/crosswax/nasbuster/internal/helper"
	"github.com/crosswax/nasbuster/internal/metrics"
	"github.com/crosswax/nasbuster/internal/scheduler"
	"github.com/crosswax/nasbuster/internal/storage"
	"github.com/crosswax/nasbuster/internal/store"
	"github.com/crosswax/nasbuster/internal/sysexec"
	"github.com/crosswax/nasbuster/internal/system"
	"github.com/crosswax/nasbuster/internal/tlsutil"
	"github.com/crosswax/nasbuster/web"
)

type config struct {
	listen       string
	dataDir      string
	helperSocket string
	tls          bool
	demo         bool
	labDir       string
	sysroot      string
	logLevel     string
}

func main() {
	var cfg config
	flag.StringVar(&cfg.listen, "listen", ":8443", "address to listen on")
	flag.StringVar(&cfg.dataDir, "data-dir", "/var/lib/cwnsb", "state directory (database, TLS, setup code)")
	flag.StringVar(&cfg.helperSocket, "helper-socket", "/run/cwnsb/helper.sock", "nbhelper Unix socket")
	flag.BoolVar(&cfg.tls, "tls", true, "serve HTTPS with a self-signed certificate (plain HTTP only allowed on loopback)")
	flag.BoolVar(&cfg.demo, "demo", false, "emulate a Raspberry Pi 4 with fixture data (UI development)")
	flag.StringVar(&cfg.labDir, "lab-dir", "", "lab mode: only loop devices backed by files under this directory are usable")
	flag.StringVar(&cfg.sysroot, "sysroot", "/", "root of /proc, /sys and /etc to read")
	flag.StringVar(&cfg.logLevel, "log-level", "info", "debug, info, warn or error")
	version := flag.Bool("version", false, "print the version and exit")
	printFingerprint := flag.Bool("print-tls-fingerprint", false, "print the SHA-256 fingerprint of the HTTPS certificate in -data-dir and exit")
	resetAdminFlag := flag.Bool("reset-admin", false, "set a new password for an administrator, typed on the terminal, then exit (run with sudo): nasbusterd -reset-admin [name]")
	flag.Parse()

	if *version {
		fmt.Println(buildinfo.Version)
		return
	}
	if *resetAdminFlag {
		if err := resetAdmin(cfg.dataDir, flag.Arg(0)); err != nil {
			fmt.Fprintln(os.Stderr, "reset-admin:", err)
			os.Exit(1)
		}
		return
	}
	if *printFingerprint {
		info, err := tlsutil.FileInfo(filepath.Join(cfg.dataDir, "tls"))
		if err != nil {
			fmt.Fprintln(os.Stderr, "no certificate yet:", err)
			os.Exit(1)
		}
		fmt.Println(info.Fingerprint)
		return
	}
	logger := newLogger(cfg.logLevel)
	if err := run(cfg, logger); err != nil {
		logger.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(cfg config, logger *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	// restartc ends the daemon cleanly with an error, so that systemd
	// (Restart=on-failure) starts it again: used after a configuration restore.
	restartc := make(chan struct{}, 1)
	restart := func() {
		select {
		case restartc <- struct{}{}:
		default:
		}
	}
	defer stop()

	if !cfg.tls && !isLoopback(cfg.listen) {
		return fmt.Errorf("-tls=false is only allowed on a loopback address, not %q", cfg.listen)
	}
	if cfg.demo && cfg.labDir != "" {
		return errors.New("-demo and -lab-dir are mutually exclusive")
	}
	if err := os.MkdirAll(cfg.dataDir, 0o750); err != nil {
		return err
	}

	dbPath := filepath.Join(cfg.dataDir, "cwnsb.db")
	restored, err := store.ApplyPendingRestore(dbPath, filepath.Join(cfg.dataDir, "restore", "config.db"), time.Now())
	if err != nil {
		logger.Error("pending configuration restore not applied", "err", err)
	}
	st, err := store.Open(ctx, dbPath)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	if restored {
		// A restored database must not bring back the sessions of its time.
		if err := st.PurgeSessions(ctx); err != nil {
			return err
		}
		logger.Warn("configuration restored from a backup: every session was closed")
	}
	defer st.Close()

	// The database copy for configuration backups (ADR-0014): the helper
	// reads it at backup.DaemonExport, which is this path in production.
	exportPath := filepath.Join(cfg.dataDir, "backup", "config.db")
	var (
		prober    *system.Prober
		disks     *storage.Discoverer
		helperCl  helper.Client
		demoClock = time.Now
		demoFault func(kind, disk string) error
		bootInfo  func() (string, time.Duration, bool)
	)
	if cfg.demo {
		sys := demo.New()
		demoClock = func() time.Time { return sys.Clock() }
		demoFault = sys.Fault
		prober = system.New(sys.FS, sys.Runner)
		prober.GOARCH = "arm64"
		prober.ClockSynced = func() (bool, bool) { return true, true }
		bootInfo = system.BootInfo(sys.FS)
		disks = &storage.Discoverer{Run: sys.Runner, FS: sys.FS, CacheFor: 5 * time.Second}
		// The helper keeps its own, uncached view of the disks (as in production).
		helperDisks := &storage.Discoverer{Run: sys.Runner, FS: sys.FS}
		helperCl = &helper.LocalClient{Ops: &helper.Ops{
			Run: sys.Runner, FS: sys.FS, ByIDDir: "/dev/disk/by-id",
			DeviceExists: sys.DeviceExists, WriteFile: sys.WriteFile, WriteSysfs: sys.WriteSysfs, Logger: logger,
			Discover: helperDisks.Disks, Files: sys.Files,
			UnitDir: "/etc/systemd/system", MDConf: "/etc/mdadm/mdadm.conf", SMBConf: "/etc/samba/smb.conf", Now: func() time.Time { return sys.Clock() },
			Sleep:            func(time.Duration) {},
			ReadDaemonExport: func() ([]byte, error) { return os.ReadFile(exportPath) },
		}}
		logger.Warn("DEMO MODE: emulated Raspberry Pi, no real disk or system is read or changed")
	} else {
		root := os.DirFS(cfg.sysroot)
		runner := sysexec.OSRunner{}
		prober = system.New(root, runner)
		prober.ClockSynced = system.ClockSynced(root)
		bootInfo = system.BootInfo(root)
		disks = &storage.Discoverer{Run: runner, FS: root, LabDir: cfg.labDir, CacheFor: 5 * time.Second}
		helperCl = &helper.UnixClient{Socket: cfg.helperSocket}
		if cfg.labDir != "" {
			logger.Warn("LAB MODE: only loop devices under the lab directory are usable; every other disk is protected", "lab_dir", cfg.labDir)
		}
	}

	codes, err := newSetupCodes(ctx, st, filepath.Join(cfg.dataDir, "setup-code"), logger)
	if err != nil {
		return err
	}

	sampler := metrics.New(prober)
	go sampler.Run(ctx)
	// Event journal (ADR-0031): the boot, an unclean end of the previous one,
	// then what the scheduler sees. The previous run was last seen working at
	// its last alert evaluation (every 5 minutes).
	journal := &events.Recorder{Store: st, Now: demoClock, Synced: prober.ClockSynced, Boot: bootInfo, Logger: logger}
	var lastAlive int64
	if state, err := scheduler.LoadState(ctx, st); err == nil {
		lastAlive = state.LastAlertEval
	}
	journal.Start(ctx, lastAlive)
	defer journal.Stop(context.Background())
	sched := &scheduler.Scheduler{Store: st, Helper: helperCl, Prober: prober, Notifier: alerts.NewNotifier(), Logger: logger,
		ExportPath: exportPath, Events: journal, Boot: bootInfo}
	if cfg.demo {
		sched.Now = func() time.Time { return demoClock() }
		sched.Notifier = nil // demo: alerts are recorded, never sent outside
	} else {
		// The system disk (the SD card) for its space alert (ADR-0034).
		sched.RootSpace = func() (uint64, uint64, bool) { return system.FilesystemSpace("/") }
	}
	go sched.Run(ctx)
	go func() {
		// Desired state wins at startup (ADR-0005): Samba gets CWNSB's config.
		rctx, cancel := context.WithTimeout(ctx, time.Minute)
		defer cancel()
		if err := api.ReconcileShares(rctx, st, helperCl); err != nil {
			logger.Warn("could not apply the Samba configuration at startup", "err", err)
		} else {
			logger.Info("Samba configuration applied")
		}
		// Existing pools get the current mount unit (effective at the next boot).
		var units helper.PoolUnitsResult
		if err := helperCl.Call(rctx, helper.OpPoolUnitsRefresh, struct{}{}, &units); err != nil {
			logger.Warn("could not refresh the pools' mount units", "err", err)
		} else if len(units.Updated) > 0 {
			logger.Info("pool mount units refreshed", "pools", units.Updated)
		}
		// An initramfs older than mdadm.conf is rebuilt (ADR-0033): existing installs too.
		var initramfs helper.InitramfsResult
		if err := helperCl.Call(rctx, helper.OpInitramfsRefresh, struct{}{}, &initramfs); err != nil {
			logger.Warn("could not check the initramfs", "err", err)
		} else if initramfs.State == "started" {
			logger.Info("initramfs rebuild started: mdadm.conf changed since the last one")
		}
		// Docker without live-restore (ADR-0029), reloaded without stopping the apps.
		var engine helper.EngineRefreshResult
		if err := helperCl.Call(rctx, helper.OpEngineRefresh, struct{}{}, &engine); err != nil {
			logger.Warn("could not refresh Docker's configuration", "err", err)
		} else if engine.Updated {
			logger.Info("Docker configuration refreshed: live-restore off", "reloaded", engine.Reloaded)
		}
		// The system journal kept across reboots, unless turned off (ADR-0031).
		var persist helper.JournalPersistResult
		if set, err := events.LoadSettings(rctx, st); err != nil {
			logger.Warn("could not read the journal settings", "err", err)
		} else if err := helperCl.Call(rctx, helper.OpJournalPersist, helper.JournalPersistArgs{Enabled: set.Persistent}, &persist); err != nil {
			logger.Warn("could not apply the journal settings", "err", err)
		} else if persist.Changed {
			logger.Info("system journal storage changed", "persistent", persist.Persistent, "managed_by", persist.ManagedBy)
		}
	}()
	go purgeSessions(ctx, st, logger)

	// HTTPS certificate (ADR-0019): loaded, or renewed when it no longer covers
	// the host's name or IPv4 addresses, before anything is served.
	var certs *tlsutil.Manager
	if cfg.tls {
		certs = tlsutil.NewManager(filepath.Join(cfg.dataDir, "tls"))
		renewed, err := certs.Load(prober.Info().Hostname)
		if err != nil {
			return fmt.Errorf("tls: %w", err)
		}
		info := certs.Info()
		logger.Info("tls certificate", "renewed", renewed, "fingerprint_sha256", info.Fingerprint,
			"names", strings.Join(info.DNSNames, ","), "ips", strings.Join(info.IPAddresses, ","))
	}

	handler := api.New(api.Deps{
		Store:         st,
		Prober:        prober,
		Disks:         disks,
		Helper:        helperCl,
		Metrics:       sampler,
		Limiter:       auth.NewLimiter(),
		SetupCode:     codes,
		UI:            web.Dist(),
		Logger:        logger,
		Version:       buildinfo.Version,
		SecureCookies: cfg.tls,
		Demo:          cfg.demo,
		DemoFault:     demoFault,
		ExportPath:    exportPath,
		Restart:       restartIf(!cfg.demo, restart), // demo: no restore (nothing restarts it)
		LabMode:       cfg.labDir != "",
		Certificates:  certificatesDep(certs),
		AdminCheck:    adminCheckDep(cfg.demo),
		Now:           demoClock,
		Boot:          bootInfo,
		Notify:        notifyDep(cfg.demo, st, prober, logger),
	})
	srv := &http.Server{
		Addr:              cfg.listen,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       2 * time.Minute,
		// No WriteTimeout: SSE is long-lived; the API sets per-request deadlines.
		ErrorLog: slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
	}

	errc := make(chan error, 1)
	go func() {
		if cfg.tls {
			// Served hot: a renewal from the UI applies to new connections at once.
			srv.TLSConfig = &tls.Config{GetCertificate: certs.GetCertificate}
			logger.Info("listening", "addr", cfg.listen, "scheme", "https", "version", buildinfo.Version)
			errc <- srv.ListenAndServeTLS("", "")
		} else {
			logger.Info("listening", "addr", cfg.listen, "scheme", "http", "version", buildinfo.Version)
			errc <- srv.ListenAndServe()
		}
	}()

	select {
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
		logger.Info("shutting down")
	case <-restartc:
		time.Sleep(time.Second) // let the response reach the browser
		logger.Warn("restarting to load the restored configuration")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
		return errRestart
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

// errRestart makes the process exit with an error: systemd restarts it.
var errRestart = errors.New("restart requested")

func purgeSessions(ctx context.Context, st *store.Store, logger *slog.Logger) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if n, err := st.DeleteExpiredSessions(ctx); err != nil {
				logger.Warn("purge sessions", "err", err)
			} else if n > 0 {
				logger.Debug("purged expired sessions", "count", n)
			}
		}
	}
}

func isLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func newLogger(level string) *slog.Logger {
	var l slog.Level
	switch strings.ToLower(level) {
	case "debug":
		l = slog.LevelDebug
	case "warn":
		l = slog.LevelWarn
	case "error":
		l = slog.LevelError
	default:
		l = slog.LevelInfo
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: l}))
}

func restartIf(ok bool, f func()) func() {
	if !ok {
		return nil
	}
	return f
}

// certificatesDep keeps a nil *tlsutil.Manager out of the interface: without
// TLS (demo on loopback), the API sees no certificate manager at all.
func certificatesDep(m *tlsutil.Manager) api.Certificates {
	if m == nil {
		return nil
	}
	return m
}

// adminCheckDep: in the demo, no emulated application has an admin account
// (nothing real is asked); nil uses the real check.
func adminCheckDep(demo bool) func(context.Context, string, int) (bool, error) {
	if !demo {
		return nil
	}
	return func(context.Context, string, int) (bool, error) { return false, nil }
}

// notifyDep sends one-off notifications (a password reset, ADR-0037) to the
// channels set for alerts, in the admin's language. The demo sends nothing
// outside, as for its alerts.
func notifyDep(demo bool, st *store.Store, prober *system.Prober, logger *slog.Logger) func(context.Context, string, map[string]any) {
	if demo {
		return nil
	}
	notifier := alerts.NewNotifier()
	return func(ctx context.Context, code string, params map[string]any) {
		ch, err := scheduler.LoadChannels(ctx, st)
		if err != nil {
			logger.Warn("notification not sent", "code", code, "err", err)
			return
		}
		m := alerts.Message{NAS: prober.Info().Hostname, Title: alerts.Title(code, st.AdminLanguage(ctx), params),
			Level: alerts.Warn, Code: code, Params: params, Time: time.Now().Unix()}
		for _, res := range notifier.Send(ctx, ch, m) {
			if !res.OK {
				logger.Warn("notification not sent", "code", code, "channel", res.Channel, "reason", res.Code)
			}
		}
	}
}
