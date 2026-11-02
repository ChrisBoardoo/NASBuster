// SPDX-License-Identifier: AGPL-3.0-or-later

package helper

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"time"
)

// ErrUnavailable means the helper could not be reached.
var ErrUnavailable = errors.New("helper unavailable")

// Client calls privileged operations. A failed operation returns *Error.
type Client interface {
	Call(ctx context.Context, op string, args any, result any) error
}

// UnixClient talks to nbhelper over its socket.
type UnixClient struct {
	Socket string
	Actor  string // default actor name for the helper's log
}

// Call implements Client.
func (c *UnixClient) Call(ctx context.Context, op string, args any, result any) error {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
	}
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", c.Socket)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}

	req, err := newRequest(ctx, op, args, c.Actor)
	if err != nil {
		return err
	}
	b, _ := json.Marshal(req)
	if _, err := conn.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	line, err := bufio.NewReader(io.LimitReader(conn, MaxMessage)).ReadBytes('\n')
	if err != nil && !(errors.Is(err, io.EOF) && len(line) > 0) {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	var resp Response
	if err := json.Unmarshal(line, &resp); err != nil {
		return fmt.Errorf("helper: malformed response: %w", err)
	}
	return decodeResponse(resp, result)
}

// LocalClient calls Ops in-process. Used by demo mode and tests; it goes
// through the same JSON encoding and validation as the socket.
type LocalClient struct {
	Ops   *Ops
	Actor string
}

// Call implements Client.
func (c *LocalClient) Call(ctx context.Context, op string, args any, result any) error {
	req, err := newRequest(ctx, op, args, c.Actor)
	if err != nil {
		return err
	}
	out, opErr := c.Ops.Dispatch(ctx, req)
	if opErr != nil {
		return opErr
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return err
	}
	return decodeResponse(Response{OK: true, Result: raw}, result)
}

type actorKey struct{}

// WithActor tags a context with the user on whose behalf the helper is called.
func WithActor(ctx context.Context, actor string) context.Context {
	return context.WithValue(ctx, actorKey{}, actor)
}

func newRequest(ctx context.Context, op string, args any, fallbackActor string) (Request, error) {
	req := Request{Op: op, Actor: fallbackActor}
	if a, ok := ctx.Value(actorKey{}).(string); ok && a != "" {
		req.Actor = a
	}
	if args != nil {
		raw, err := json.Marshal(args)
		if err != nil {
			return req, err
		}
		req.Args = raw
	}
	return req, nil
}

func decodeResponse(resp Response, result any) error {
	if !resp.OK {
		if resp.Error == nil {
			return &Error{Code: CodeFailed, Message: "helper returned no error detail"}
		}
		return resp.Error
	}
	if result == nil || len(resp.Result) == 0 {
		return nil
	}
	return json.Unmarshal(resp.Result, result)
}
