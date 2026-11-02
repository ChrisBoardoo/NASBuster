// SPDX-License-Identifier: AGPL-3.0-or-later

// Command nbhelper is the privileged half of CrossWax NasBuster (ADR-0002).
// It runs as root, listens on a Unix socket (ideally socket-activated by
// systemd so it only runs when needed), accepts calls only from root and the
// allowed service user, and executes a fixed list of validated operations.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"os/user"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/crosswax/nasbuster/internal/buildinfo"
	"github.com/crosswax/nasbuster/internal/helper"
)

func main() {
	socket := flag.String("socket", "/run/cwnsb/helper.sock", "socket path when not socket-activated")
	allowUser := flag.String("allow-user", "cwnsb", "service user allowed to call the helper (root always is)")
	idleExit := flag.Duration("idle-exit", 2*time.Minute, "exit after this long without requests when socket-activated (0 = never)")
	labDir := flag.String("lab-dir", "", "lab mode: only loop devices backed by files under this directory may be used")
	version := flag.Bool("version", false, "print the version and exit")
	flag.Parse()
	if *version {
		fmt.Println(buildinfo.Version)
		return
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := run(*socket, *allowUser, *labDir, *idleExit, logger); err != nil {
		logger.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(socket, allowUser, labDir string, idleExit time.Duration, logger *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if os.Geteuid() != 0 {
		logger.Warn("not running as root: privileged operations will fail")
	}
	allowed := map[uint32]bool{}
	if allowUser != "" {
		u, err := user.Lookup(allowUser)
		if err != nil {
			return fmt.Errorf("allowed user %q: %w", allowUser, err)
		}
		uid, err := strconv.ParseUint(u.Uid, 10, 32)
		if err != nil {
			return err
		}
		allowed[uint32(uid)] = true
	}

	ln, activated, err := listen(socket)
	if err != nil {
		return err
	}
	defer ln.Close()
	if !activated {
		idleExit = 0 // a manually started helper keeps running
	}
	logger.Info("ready", "version", buildinfo.Version, "socket_activated", activated, "allow_user", allowUser, "lab_dir", labDir)
	if labDir != "" {
		logger.Warn("LAB MODE: only loop devices under the lab directory may be used", "lab_dir", labDir)
	}

	srv := &helper.Server{Ops: helper.NewOps(logger, labDir), AllowedUIDs: allowed, IdleExit: idleExit, Logger: logger}
	return srv.Serve(ctx, ln)
}

// listen uses the socket passed by systemd (LISTEN_FDS) or creates one.
func listen(path string) (net.Listener, bool, error) {
	if os.Getenv("LISTEN_PID") == strconv.Itoa(os.Getpid()) && os.Getenv("LISTEN_FDS") == "1" {
		f := os.NewFile(3, "nbhelper.socket") // SD_LISTEN_FDS_START
		ln, err := net.FileListener(f)
		f.Close()
		if err != nil {
			return nil, false, fmt.Errorf("socket activation: %w", err)
		}
		return ln, true, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, false, err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, false, err
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, false, err
	}
	// Only root and the service group may connect; the uid check is the real gate.
	if err := os.Chmod(path, 0o660); err != nil {
		ln.Close()
		return nil, false, err
	}
	return ln, false, nil
}
