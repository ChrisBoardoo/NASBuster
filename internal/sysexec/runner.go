// SPDX-License-Identifier: AGPL-3.0-or-later

// Package sysexec runs external programs. It never goes through a shell:
// the program is resolved in a fixed list of system directories and receives
// its arguments as a vector, so no argument can be interpreted as shell syntax.
package sysexec

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ErrNotFound is returned when the program is not installed.
var ErrNotFound = errors.New("program not found")

// Result is the outcome of a program that ran to completion.
type Result struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

// Runner executes a program with arguments.
//
// A non-zero exit status is reported in Result.ExitCode and is NOT an error:
// tools such as smartctl encode disk state in their exit bits. err is reserved
// for programs that cannot start, time out, or produce too much output.
type Runner interface {
	Run(ctx context.Context, name string, args ...string) (Result, error)
	// RunInput is Run with data on standard input. Secrets (passwords) go
	// this way: arguments are visible to every user in ps(1), stdin is not.
	RunInput(ctx context.Context, input []byte, name string, args ...string) (Result, error)
}

// searchDirs is the only place programs are looked up. PATH is ignored on purpose.
var searchDirs = []string{"/usr/sbin", "/usr/bin", "/sbin", "/bin"}

// maxOutput bounds what we keep from each stream.
const maxOutput = 8 << 20

// OSRunner runs real programs.
type OSRunner struct {
	// Timeout applies when ctx has no deadline. Zero means 30 seconds.
	Timeout time.Duration
}

// Run implements Runner. Standard input is empty (/dev/null).
func (r OSRunner) Run(ctx context.Context, name string, args ...string) (Result, error) {
	return r.run(ctx, nil, name, args...)
}

// RunInput implements Runner.
func (r OSRunner) RunInput(ctx context.Context, input []byte, name string, args ...string) (Result, error) {
	return r.run(ctx, input, name, args...)
}

func (r OSRunner) run(ctx context.Context, input []byte, name string, args ...string) (Result, error) {
	path, err := lookPath(name)
	if err != nil {
		return Result{}, err
	}
	if _, ok := ctx.Deadline(); !ok {
		timeout := r.Timeout
		if timeout == 0 {
			timeout = 30 * time.Second
		}
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = []string{"LC_ALL=C", "LANG=C", "PATH=" + strings.Join(searchDirs, ":")}
	cmd.WaitDelay = 2 * time.Second
	stdout := &limitedBuffer{max: maxOutput}
	stderr := &limitedBuffer{max: maxOutput}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if input != nil {
		cmd.Stdin = bytes.NewReader(input)
	}

	runErr := cmd.Run()
	res := Result{Stdout: stdout.Bytes(), Stderr: stderr.Bytes(), ExitCode: -1}
	if cmd.ProcessState != nil {
		res.ExitCode = cmd.ProcessState.ExitCode()
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return res, fmt.Errorf("%s: %w", name, ctxErr)
	}
	if stdout.overflow || stderr.overflow {
		return res, fmt.Errorf("%s: output exceeds %d bytes", name, maxOutput)
	}
	var exitErr *exec.ExitError
	if runErr != nil && !errors.As(runErr, &exitErr) {
		return res, fmt.Errorf("%s: %w", name, runErr)
	}
	return res, nil
}

// lookPath resolves a bare program name in searchDirs.
func lookPath(name string) (string, error) {
	if name == "" || strings.ContainsAny(name, `/\`) {
		return "", fmt.Errorf("invalid program name %q", name)
	}
	for _, dir := range searchDirs {
		p := filepath.Join(dir, name)
		info, err := os.Stat(p)
		if err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0 {
			return p, nil
		}
	}
	return "", fmt.Errorf("%w: %s", ErrNotFound, name)
}

// limitedBuffer keeps at most max bytes and records overflow.
type limitedBuffer struct {
	buf      bytes.Buffer
	max      int
	overflow bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if room := b.max - b.buf.Len(); room < len(p) {
		b.overflow = true
		if room > 0 {
			b.buf.Write(p[:room])
		}
		return len(p), nil
	}
	return b.buf.Write(p)
}

func (b *limitedBuffer) Bytes() []byte { return b.buf.Bytes() }
