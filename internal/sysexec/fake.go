// SPDX-License-Identifier: AGPL-3.0-or-later

package sysexec

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// Fake is a Runner that replays canned results. It is used by tests and by
// demo mode. Unknown commands fail with ErrNotFound.
type Fake struct {
	mu        sync.Mutex
	responses map[string]Result
	errors    map[string]error
	handlers  map[string]func(args []string) (Result, error)
	inputFns  map[string]func(args []string, input []byte) (Result, error)
	calls     [][]string
	inputs    map[int][]byte // call index -> stdin
}

// HandleInput registers a function that also receives the standard input, for
// commands whose answer depends on it (the demo checks a password this way).
// It answers RunInput calls of program name; Run calls still use Handle.
func (f *Fake) HandleInput(name string, fn func(args []string, input []byte) (Result, error)) *Fake {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.inputFns == nil {
		f.inputFns = map[string]func([]string, []byte) (Result, error){}
	}
	f.inputFns[name] = fn
	return f
}

// NewFake returns an empty Fake.
func NewFake() *Fake {
	return &Fake{
		responses: map[string]Result{},
		errors:    map[string]error{},
		handlers:  map[string]func([]string) (Result, error){},
	}
}

// Handle registers a function answering every invocation of program name
// that has no exact response registered.
func (f *Fake) Handle(name string, fn func(args []string) (Result, error)) *Fake {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.handlers[name] = fn
	return f
}

// Key identifies a command line.
func Key(name string, args ...string) string {
	return strings.Join(append([]string{name}, args...), "\x1f")
}

// Set registers the result returned for this exact command line.
func (f *Fake) Set(res Result, name string, args ...string) *Fake {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.responses[Key(name, args...)] = res
	return f
}

// SetError registers an error returned for this exact command line.
func (f *Fake) SetError(err error, name string, args ...string) *Fake {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.errors[Key(name, args...)] = err
	return f
}

// Calls returns every command line run so far.
func (f *Fake) Calls() [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([][]string, len(f.calls))
	copy(out, f.calls)
	return out
}

// Input returns the stdin given to call number i (as in Calls), or nil.
func (f *Fake) Input(i int) []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.inputs[i]
}

// RunInput implements Runner. The input is recorded, never matched.
func (f *Fake) RunInput(ctx context.Context, input []byte, name string, args ...string) (Result, error) {
	f.mu.Lock()
	if f.inputs == nil {
		f.inputs = map[int][]byte{}
	}
	f.inputs[len(f.calls)] = append([]byte(nil), input...)
	fn := f.inputFns[name]
	if fn != nil {
		f.calls = append(f.calls, append([]string{name}, args...))
		f.mu.Unlock()
		return fn(append([]string(nil), args...), append([]byte(nil), input...))
	}
	f.mu.Unlock()
	return f.Run(ctx, name, args...)
}

// Run implements Runner.
func (f *Fake) Run(_ context.Context, name string, args ...string) (Result, error) {
	f.mu.Lock()
	f.calls = append(f.calls, append([]string{name}, args...))
	key := Key(name, args...)
	if err, ok := f.errors[key]; ok {
		f.mu.Unlock()
		return Result{}, err
	}
	if res, ok := f.responses[key]; ok {
		f.mu.Unlock()
		return res, nil
	}
	handler := f.handlers[name]
	f.mu.Unlock()
	if handler != nil {
		return handler(append([]string(nil), args...))
	}
	return Result{}, fmt.Errorf("%w: fake has no response for %q", ErrNotFound, strings.ReplaceAll(key, "\x1f", " "))
}
