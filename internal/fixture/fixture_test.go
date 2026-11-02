// SPDX-License-Identifier: AGPL-3.0-or-later

package fixture

import (
	"io/fs"
	"testing"
)

func TestParse(t *testing.T) {
	fsys := Parse([]byte(`comment ignored
-- proc/loadavg --
0.31 0.25 0.20 1/180 1234
-- sys/devices/platform/soc/soc:firmware/get_throttled --
50000
-- etc/localtime -> ../usr/share/zoneinfo/Europe/Paris --
`))
	got, err := fs.ReadFile(fsys, "proc/loadavg")
	if err != nil || string(got) != "0.31 0.25 0.20 1/180 1234\n" {
		t.Fatalf("loadavg = %q, %v", got, err)
	}
	got, err = fs.ReadFile(fsys, "sys/devices/platform/soc/soc:firmware/get_throttled")
	if err != nil || string(got) != "50000\n" {
		t.Fatalf("get_throttled = %q, %v", got, err)
	}
	target, err := fs.ReadLink(fsys, "etc/localtime")
	if err != nil || target != "../usr/share/zoneinfo/Europe/Paris" {
		t.Fatalf("readlink = %q, %v", target, err)
	}
}
