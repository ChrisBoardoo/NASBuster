// SPDX-License-Identifier: AGPL-3.0-or-later

package demo

import (
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/crosswax/nasbuster/internal/sysexec"
)

// shareSim emulates accounts, groups, subvolumes and Samba so the real
// helper share code runs unchanged in demo mode. A few seconds after shares
// are applied, a Windows PC "connects" — the onboarding's "connection
// detected" moment can be shown without any network.
type shareSim struct {
	sys *System

	mu         sync.Mutex
	users      map[string][]string // name -> groups
	groups     map[string]bool
	subvolumes map[string]bool
	appliedAt  time.Time
}

func newShareSim(s *System) *shareSim {
	return &shareSim{sys: s,
		users:      map[string][]string{"root": {"root"}, "pi": {"pi", "sudo"}},
		groups:     map[string]bool{"root": true, "sudo": true, "video": true, "pi": true},
		subvolumes: map[string]bool{},
	}
}

func (h *shareSim) register(f *sysexec.Fake) {
	f.Handle("id", func(args []string) (sysexec.Result, error) {
		h.mu.Lock()
		defer h.mu.Unlock()
		g, found := h.users[args[len(args)-1]]
		if !found {
			return sysexec.Result{ExitCode: 1, Stderr: []byte("id: no such user")}, nil
		}
		return sysexec.Result{Stdout: []byte(strings.Join(g, " ") + "\n")}, nil
	})
	f.Handle("getent", func(args []string) (sysexec.Result, error) {
		h.mu.Lock()
		defer h.mu.Unlock()
		if len(args) == 2 && args[0] == "group" && h.groups[args[1]] {
			return sysexec.Result{Stdout: []byte(args[1] + ":x:900:\n")}, nil
		}
		return sysexec.Result{ExitCode: 2}, nil
	})
	f.Handle("groupdel", func(args []string) (sysexec.Result, error) {
		h.mu.Lock()
		defer h.mu.Unlock()
		delete(h.groups, args[len(args)-1])
		return done, nil
	})
	f.Handle("groupadd", func(args []string) (sysexec.Result, error) {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.groups[args[len(args)-1]] = true
		return done, nil
	})
	f.Handle("useradd", func(args []string) (sysexec.Result, error) {
		h.mu.Lock()
		defer h.mu.Unlock()
		name := args[len(args)-1]
		if _, exists := h.users[name]; exists {
			return fail("user exists")
		}
		groups := []string{name}
		for i, a := range args {
			if a == "--groups" && i+1 < len(args) {
				groups = append(groups, strings.Split(args[i+1], ",")...)
			}
		}
		h.users[name] = groups
		return done, nil
	})
	f.Handle("userdel", func(args []string) (sysexec.Result, error) {
		h.mu.Lock()
		defer h.mu.Unlock()
		delete(h.users, args[len(args)-1])
		return done, nil
	})
	for _, prog := range []string{"smbpasswd", "chown", "chmod"} {
		f.Handle(prog, func([]string) (sysexec.Result, error) { return done, nil })
	}
	f.Handle("testparm", func(args []string) (sysexec.Result, error) {
		if !h.sys.Files.Exists(args[len(args)-1]) {
			return fail("cannot open " + args[len(args)-1])
		}
		h.mu.Lock()
		h.appliedAt = h.sys.Clock()
		h.mu.Unlock()
		return done, nil
	})
	f.Handle("smbstatus", h.smbstatus)
}

// subvolume handles `btrfs subvolume create|delete <path>`.
func (h *shareSim) subvolume(args []string) (sysexec.Result, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(args) == 5 && args[1] == "snapshot" && args[2] == "-r" { // subvolume snapshot -r <src> <dst>
		if h.subvolumes[args[4]] {
			return fail("target exists")
		}
		h.subvolumes[args[4]] = true
		h.sys.Files.MkdirAll(args[4], 0o755)
		return done, nil
	}
	if len(args) != 3 {
		return fail("unsupported subvolume command")
	}
	switch args[1] {
	case "create":
		if h.subvolumes[args[2]] {
			return fail("target exists")
		}
		h.subvolumes[args[2]] = true
		h.sys.Files.MkdirAll(args[2], 0o2770)
	case "delete":
		delete(h.subvolumes, args[2])
		h.sys.Files.RemoveAll(args[2]) // with everything in it
	}
	return done, nil
}

// smbstatus reports a Windows client connected to the first share with users,
// starting 4 seconds after the configuration was applied.
func (h *shareSim) smbstatus([]string) (sysexec.Result, error) {
	h.mu.Lock()
	applied := h.appliedAt
	h.mu.Unlock()
	empty := `{"sessions": {}, "tcons": {}, "open_files": {}}`
	conf, err := h.sys.Files.ReadFile("/etc/samba/smb.conf")
	if err != nil || applied.IsZero() || h.sys.Clock().Sub(applied) < 4*time.Second {
		return sysexec.Result{Stdout: []byte(empty)}, nil
	}
	share, user := firstGrant(string(conf))
	if share == "" {
		return sysexec.Result{Stdout: []byte(empty)}, nil
	}
	since := applied.Add(4 * time.Second).Format(time.RFC3339)
	out := map[string]any{
		"sessions": map[string]any{"1001": map[string]any{
			"session_id": "1001", "username": user, "remote_machine": "DESKTOP-7Q2K",
			"hostname": "ipv4:192.168.1.20:51234", "session_dialect": "SMB3_11", "creation_time": since,
			"encryption": map[string]string{"cipher": "AES-128-GCM", "degree": "full"},
		}},
		"tcons": map[string]any{"2001": map[string]any{
			"service": share, "machine": "DESKTOP-7Q2K", "connected_at": since, "session_id": "1001",
		}},
		"open_files": map[string]any{},
	}
	b, _ := json.Marshal(out)
	return sysexec.Result{Stdout: b}, nil
}

// firstGrant finds the first share with a "valid users" list in smb.conf.
func firstGrant(conf string) (share, user string) {
	section := ""
	for _, line := range strings.Split(conf, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.Trim(line, "[]")
			continue
		}
		if v, found := strings.CutPrefix(line, "valid users = "); found && section != "global" && !strings.HasPrefix(v, "@") {
			users := strings.Fields(v)
			if len(users) > 0 && !slices.Contains(users, "") {
				return section, users[0]
			}
		}
	}
	return "", ""
}
