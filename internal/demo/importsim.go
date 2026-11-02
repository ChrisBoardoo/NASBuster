// SPDX-License-Identifier: AGPL-3.0-or-later

package demo

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/crosswax/nasbuster/internal/importer"
	"github.com/crosswax/nasbuster/internal/sysexec"
)

// Data import from a foreign disk (ADR-0015): a USB key appears, is mounted
// read-only, and its content is "copied" in about 20 seconds.

const (
	simKeyUUID  = "1A2B-3C4D"
	simCopyTime = 20 * time.Second
	simKeyBytes = 3 << 30
)

// usbKey turns a blank demo disk into an exFAT USB key named USB-KEY.
func (p *poolSim) usbKey(k string) {
	node := p.sys.baseNode(k)
	if node == nil {
		return
	}
	size, _ := node["size"].(json.Number).Int64()
	node["pttype"] = "dos"
	node["children"] = []any{map[string]any{"name": k + "1", "kname": k + "1", "path": "/dev/" + k + "1", "type": "part",
		"size": json.Number(fmt.Sprint(size - 1<<20)), "fstype": "exfat", "label": "USB-KEY", "uuid": simKeyUUID,
		"partlabel": nil, "pttype": "dos", "mountpoints": []any{nil}, "pkname": k, "rm": true, "ro": false, "tran": nil,
		"rota": node["rota"], "model": nil, "serial": nil, "wwn": nil, "vendor": nil, "hotplug": node["hotplug"]}}
}

// basePartition finds a partition of the base tree by kernel name.
func (p *poolSim) basePartition(kname string) map[string]any {
	for _, d := range p.sys.lsblk {
		children, _ := d["children"].([]any)
		for _, c := range children {
			if m, ok := c.(map[string]any); ok && m["kname"] == kname {
				return m
			}
		}
	}
	return nil
}

// mountImport handles the read-only mount of a foreign filesystem.
func (p *poolSim) mountImport(args []string) (sysexec.Result, bool) {
	if len(args) != 6 || !strings.HasPrefix(args[5], importer.Root+"/") {
		return sysexec.Result{}, false
	}
	part := p.basePartition(strings.TrimPrefix(args[4], "/dev/"))
	if part == nil {
		r, _ := fail("no such device " + args[4])
		return r, true
	}
	part["mountpoints"] = []any{args[5]}
	for _, d := range []string{"DCIM/100CANON", "Documents", "Music"} {
		p.sys.Files.MkdirAll(args[5]+"/"+d, 0o755)
	}
	return done, true
}

func (p *poolSim) umount(args []string) (sysexec.Result, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(args) != 1 {
		return fail("unsupported umount call")
	}
	for _, d := range p.sys.lsblk {
		children, _ := d["children"].([]any)
		for _, c := range children {
			if m, ok := c.(map[string]any); ok {
				if mp, _ := m["mountpoints"].([]any); len(mp) == 1 && mp[0] == args[0] {
					m["mountpoints"] = []any{nil}
					return done, nil
				}
			}
		}
	}
	return sysexec.Result{ExitCode: 32, Stderr: []byte("umount: " + args[0] + ": not mounted.")}, nil
}

// rsync answers the dry run; the copy itself runs as a transient unit.
func (p *poolSim) rsync(args []string) (sysexec.Result, error) {
	for _, a := range args {
		if a == "--dry-run" {
			return sysexec.Result{Stdout: []byte(fmt.Sprintf("Number of files: 1,204 (reg: 1,180, dir: 24)\nTotal transferred file size: %s bytes\n",
				commas(simKeyBytes)))}, nil
		}
	}
	return fail("unsupported rsync call")
}

func commas(n int64) string {
	s := fmt.Sprint(n)
	var out []string
	for len(s) > 3 {
		out = append([]string{s[len(s)-3:]}, out...)
		s = s[:len(s)-3]
	}
	return strings.Join(append([]string{s}, out...), ",")
}

// importJob simulates `rsync … --log-file=L SRC/ DST/`: the log grows with time.
func (a *appsSim) importJob(cmd []string, now time.Time) *simJob {
	logPath, dest := "", ""
	for _, c := range cmd {
		if v, ok := strings.CutPrefix(c, "--log-file="); ok {
			logPath = v
		}
	}
	if len(cmd) > 0 {
		dest = strings.TrimSuffix(cmd[len(cmd)-1], "/")
	}
	files := []string{"DCIM/100CANON/IMG_0001.JPG", "DCIM/100CANON/IMG_0002.JPG", "Documents/budget.ods", "Music/song.flac"}
	j := &simJob{started: now, length: simCopyTime, log: []string{"sending incremental file list"}}
	j.progress = func(frac float64) {
		n := int(frac * float64(len(files)))
		var b strings.Builder
		b.WriteString("2026/10/02 03:00:00 [4242] building file list\n")
		for i := 0; i < n; i++ {
			fmt.Fprintf(&b, "2026/10/02 03:00:%02d [4242] %d %s\n", i+1, simKeyBytes/int64(len(files)), files[i])
		}
		a.sys.Files.WriteFile(logPath, []byte(b.String()), 0o600)
	}
	j.done = func() {
		j.progress(1)
		for _, f := range files {
			a.sys.Files.WriteFile(dest+"/"+f, nil, 0o660)
		}
	}
	j.progress(0)
	return j
}

// importMount finds the base partition mounted on mnt.
func (p *poolSim) importMount(mnt string) map[string]any {
	for _, d := range p.sys.lsblk {
		children, _ := d["children"].([]any)
		for _, c := range children {
			if m, ok := c.(map[string]any); ok {
				if mp, _ := m["mountpoints"].([]any); len(mp) == 1 && mp[0] == mnt {
					return m
				}
			}
		}
	}
	return nil
}
