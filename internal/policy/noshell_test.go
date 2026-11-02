// SPDX-License-Identifier: AGPL-3.0-or-later

// Package policy holds repository-wide rules enforced as tests.
package policy

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// shellCall matches any attempt to run a command through a shell.
var shellCall = regexp.MustCompile(`"(?:/bin/|/usr/bin/)?(?:ba|da|z|k)?sh"\s*,\s*"-c"|exec\.Command(?:Context)?\([^)]*"(?:/bin/|/usr/bin/)?(?:ba|da|z|k)?sh"`)

// TestNoShellExecution enforces CLAUDE.md: programs are run with an argument
// vector, never through sh -c, so no input can become shell syntax.
func TestNoShellExecution(t *testing.T) {
	root := repoRoot(t)
	self, _ := filepath.Abs("noshell_test.go")
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == "node_modules" || d.Name() == ".git" || d.Name() == "dist") {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || path == self {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(b), "\n") {
			if shellCall.MatchString(line) {
				rel, _ := filepath.Rel(root, path)
				t.Errorf("%s:%d runs a shell: %s", rel, i+1, strings.TrimSpace(line))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestPatternCatchesShell(t *testing.T) {
	for _, bad := range []string{
		`exec.Command("sh", "-c", cmd)`,
		`exec.CommandContext(ctx, "/bin/bash", "-c", x)`,
		`run.Run(ctx, "bash", "-c", "rm -rf /")`,
	} {
		if !shellCall.MatchString(bad) {
			t.Errorf("pattern misses %s", bad)
		}
	}
	for _, ok := range []string{`exec.Command(path, args...)`, `run.Run(ctx, "smartctl", "-j", "-a", p)`} {
		if shellCall.MatchString(ok) {
			t.Errorf("false positive on %s", ok)
		}
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}
