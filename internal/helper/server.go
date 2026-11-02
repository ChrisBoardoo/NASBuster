// SPDX-License-Identifier: AGPL-3.0-or-later

package helper

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// Server accepts requests on a Unix socket and dispatches them to Ops.
type Server struct {
	Ops *Ops
	// AllowedUIDs may call the helper. Root (0) is always allowed.
	AllowedUIDs map[uint32]bool
	// PeerUID returns the caller's uid (SO_PEERCRED). Overridable in tests.
	PeerUID func(net.Conn) (uint32, error)
	// IdleExit makes Serve return after this long without any request
	// (systemd socket activation restarts the helper on demand). Zero: never.
	IdleExit time.Duration
	Logger   *slog.Logger
}

// Serve handles connections until ctx is done, the listener fails, or the
// idle timeout fires.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	if s.PeerUID == nil {
		s.PeerUID = peerUID
	}
	if s.Logger == nil {
		s.Logger = slog.Default()
	}
	var active atomic.Int64
	var lastActivity atomic.Int64
	lastActivity.Store(time.Now().UnixNano())

	var wg sync.WaitGroup
	defer wg.Wait()

	stop := make(chan struct{})
	defer close(stop)
	go func() {
		var tick <-chan time.Time
		if s.IdleExit > 0 {
			ticker := time.NewTicker(min(s.IdleExit/4, 5*time.Second))
			defer ticker.Stop()
			tick = ticker.C
		}
		for {
			select {
			case <-ctx.Done():
				ln.Close()
				return
			case <-stop:
				return
			case <-tick:
				idle := time.Since(time.Unix(0, lastActivity.Load()))
				if active.Load() == 0 && idle >= s.IdleExit {
					s.Logger.Info("idle, exiting", "idle", idle.Round(time.Second))
					ln.Close()
					return
				}
			}
		}
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		active.Add(1)
		lastActivity.Store(time.Now().UnixNano())
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer active.Add(-1)
			defer lastActivity.Store(time.Now().UnixNano())
			s.handle(ctx, conn)
		}()
	}
}

func (s *Server) handle(ctx context.Context, conn net.Conn) {
	defer conn.Close()

	uid, err := s.PeerUID(conn)
	if err != nil || (uid != 0 && !s.AllowedUIDs[uid]) {
		s.Logger.Warn("rejected caller", "uid", uid, "err", err)
		s.reply(conn, Response{Error: errorf(CodeForbidden, "caller not allowed")})
		return
	}

	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	line, err := bufio.NewReaderSize(io.LimitReader(conn, MaxMessage), 4096).ReadBytes('\n')
	if err != nil && !(errors.Is(err, io.EOF) && len(line) > 0) {
		s.reply(conn, Response{Error: errorf(CodeInvalidRequest, "unreadable request")})
		return
	}
	var req Request
	dec := json.NewDecoder(bytes.NewReader(line))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		s.reply(conn, Response{Error: errorf(CodeInvalidRequest, "malformed request")})
		return
	}

	start := time.Now()
	result, opErr := s.Ops.Dispatch(ctx, req)
	attrs := []any{"op", req.Op, "actor", req.Actor, "uid", uid, "duration", time.Since(start).Round(time.Millisecond)}
	if opErr != nil {
		s.Logger.Warn("operation failed", append(attrs, "code", opErr.Code, "error", opErr.Message)...)
		s.reply(conn, Response{Error: opErr})
		return
	}
	raw, err := json.Marshal(result)
	if err != nil {
		s.reply(conn, Response{Error: errorf(CodeFailed, "encode result")})
		return
	}
	if req.Op != OpPing {
		s.Logger.Info("operation done", attrs...)
	}
	s.reply(conn, Response{OK: true, Result: raw})
}

func (s *Server) reply(conn net.Conn, resp Response) {
	_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	b, err := json.Marshal(resp)
	if err != nil {
		return
	}
	_, _ = conn.Write(append(b, '\n'))
}
