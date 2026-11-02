// SPDX-License-Identifier: AGPL-3.0-or-later

package helper

import (
	"errors"
	"io/fs"
	"os"
	"path"
	"sort"
	"sync"
)

// Files abstracts the file changes the helper makes (mount units, mount
// points, mdadm.conf), so pool operations can be tested without root.
type Files interface {
	Exists(path string) bool
	ReadFile(path string) ([]byte, error)
	WriteFile(path string, data []byte, perm fs.FileMode) error // atomic
	MkdirAll(path string, perm fs.FileMode) error
	// MkdirAllIn creates root/rel, never leaving root: not even through a
	// symbolic link (one a container planted in its folder).
	MkdirAllIn(root, rel string, perm fs.FileMode) error
	Remove(path string) error              // file or empty directory
	ReadDir(path string) ([]string, error) // entry names, sorted
}

// OSFiles is the real filesystem.
type OSFiles struct{}

func (OSFiles) Exists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}
func (OSFiles) ReadFile(p string) ([]byte, error) { return os.ReadFile(p) }
func (OSFiles) WriteFile(p string, data []byte, perm fs.FileMode) error {
	return atomicWrite(p, data, perm)
}
func (OSFiles) MkdirAll(p string, perm fs.FileMode) error { return os.MkdirAll(p, perm) }
func (OSFiles) Remove(p string) error                     { return os.Remove(p) }
func (OSFiles) MkdirAllIn(root, rel string, perm fs.FileMode) error {
	r, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer r.Close()
	return r.MkdirAll(rel, perm)
}
func (OSFiles) ReadDir(p string) ([]string, error) {
	entries, err := os.ReadDir(p)
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names, err
}

// MemFiles is an in-memory Files for tests and demo mode. Paths are absolute.
type MemFiles struct {
	mu    sync.Mutex
	files map[string][]byte
	dirs  map[string]bool
}

// NewMemFiles returns an empty MemFiles with the given paths present.
func NewMemFiles(existing ...string) *MemFiles {
	m := &MemFiles{files: map[string][]byte{}, dirs: map[string]bool{"/": true}}
	for _, p := range existing {
		m.files[p] = nil
	}
	return m
}

func (m *MemFiles) Exists(p string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, f := m.files[p]
	return f || m.dirs[p]
}

func (m *MemFiles) ReadFile(p string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.files[p]
	if !ok {
		return nil, fs.ErrNotExist
	}
	return append([]byte(nil), b...), nil
}

func (m *MemFiles) WriteFile(p string, data []byte, _ fs.FileMode) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.dirs[path.Dir(p)] {
		m.dirs[path.Dir(p)] = true // tests do not model every parent
	}
	m.files[p] = append([]byte(nil), data...)
	return nil
}

func (m *MemFiles) MkdirAll(p string, _ fs.FileMode) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for d := p; d != "/" && d != "."; d = path.Dir(d) {
		m.dirs[d] = true
	}
	return nil
}

func (m *MemFiles) MkdirAllIn(root, rel string, perm fs.FileMode) error {
	return m.MkdirAll(path.Join(root, rel), perm)
}

func (m *MemFiles) Remove(p string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.files[p]; ok {
		delete(m.files, p)
		return nil
	}
	if m.dirs[p] {
		for other := range m.files {
			if path.Dir(other) == p {
				return errors.New("directory not empty")
			}
		}
		delete(m.dirs, p)
		return nil
	}
	return fs.ErrNotExist
}

// ReadDir lists the direct children (files and directories) of p.
func (m *MemFiles) ReadDir(p string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.dirs[p] {
		return nil, fs.ErrNotExist
	}
	set := map[string]bool{}
	for f := range m.files {
		if path.Dir(f) == p {
			set[path.Base(f)] = true
		}
	}
	for d := range m.dirs {
		if d != p && path.Dir(d) == p {
			set[path.Base(d)] = true
		}
	}
	out := make([]string, 0, len(set))
	for n := range set {
		out = append(out, n)
	}
	sort.Strings(out)
	return out, nil
}

// Paths lists existing files (sorted), for test assertions.
func (m *MemFiles) Paths() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, 0, len(m.files))
	for p := range m.files {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}
