// SPDX-License-Identifier: AGPL-3.0-or-later

package tlsutil

import (
	"crypto/ecdsa"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

type fakeHost struct {
	now   time.Time
	addrs []net.IP
}

func newTestManager(t *testing.T) (*Manager, *fakeHost) {
	t.Helper()
	h := &fakeHost{now: time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC), addrs: []net.IP{net.ParseIP("192.168.1.50").To4()}}
	m := NewManager(filepath.Join(t.TempDir(), "tls"))
	m.Now = func() time.Time { return h.now }
	m.Addrs = func() []net.IP { return h.addrs }
	return m, h
}

func publicKey(t *testing.T, m *Manager) *ecdsa.PublicKey {
	t.Helper()
	c, err := m.GetCertificate(nil)
	if err != nil {
		t.Fatal(err)
	}
	return c.Leaf.PublicKey.(*ecdsa.PublicKey)
}

func TestLoadCreatesThenReuses(t *testing.T) {
	m, _ := newTestManager(t)
	renewed, err := m.Load("rpi5-nas")
	if err != nil || !renewed {
		t.Fatalf("first load: renewed=%v err=%v", renewed, err)
	}
	info := m.Info()
	for _, name := range []string{"rpi5-nas", "rpi5-nas.local", "localhost"} {
		if !slices.Contains(info.DNSNames, name) {
			t.Errorf("DNS SANs %v lack %s", info.DNSNames, name)
		}
	}
	if !slices.Contains(info.IPAddresses, "192.168.1.50") || !slices.Contains(info.IPAddresses, "127.0.0.1") {
		t.Errorf("IP SANs = %v", info.IPAddresses)
	}
	if _, err := os.Stat(m.keyFile()); err != nil {
		t.Errorf("key file: %v", err)
	}

	before, _ := os.ReadFile(m.certFile())
	again := NewManager(m.dir)
	again.Now, again.Addrs = m.Now, m.Addrs
	if renewed, err := again.Load("rpi5-nas"); err != nil || renewed {
		t.Fatalf("second load renewed a valid certificate (%v, %v)", renewed, err)
	}
	after, _ := os.ReadFile(m.certFile())
	if string(before) != string(after) || again.Info().Fingerprint != info.Fingerprint {
		t.Error("certificate rewritten although still valid")
	}
}

func TestLoadRenewsKeepingTheKey(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(h *fakeHost) string // returns the hostname to load
	}{
		{"new hostname", func(h *fakeHost) string { return "nas-maison" }},
		{"new IPv4 address", func(h *fakeHost) string {
			h.addrs = []net.IP{net.ParseIP("192.168.1.77").To4()}
			return "rpi5-nas"
		}},
		{"about to expire", func(h *fakeHost) string {
			h.now = h.now.Add(validity - 10*24*time.Hour)
			return "rpi5-nas"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, h := newTestManager(t)
			if _, err := m.Load("rpi5-nas"); err != nil {
				t.Fatal(err)
			}
			old, oldKey := m.Info(), publicKey(t, m)
			renewed, err := m.Load(tc.change(h))
			if err != nil || !renewed {
				t.Fatalf("renewed=%v err=%v", renewed, err)
			}
			if m.Info().Fingerprint == old.Fingerprint {
				t.Error("same certificate after renewal")
			}
			if !publicKey(t, m).Equal(oldKey) {
				t.Error("renewal changed the private key: the NAS's identity must stay")
			}
		})
	}
}

// The test Pi's first certificate was made for "Rpi5-NAS"; onboarding then
// lower-cased the name. DNS names are case-insensitive: no renewal needed.
func TestCoversIgnoresCase(t *testing.T) {
	m, _ := newTestManager(t)
	if _, err := m.Load("Rpi5-NAS"); err != nil {
		t.Fatal(err)
	}
	if !m.Covers("rpi5-nas") {
		t.Fatal("Rpi5-NAS should cover rpi5-nas")
	}
	if m.Covers("nas-maison") {
		t.Fatal("a certificate must not cover another name")
	}
}

func TestRenewServesAtOnce(t *testing.T) {
	m, _ := newTestManager(t)
	if _, err := m.Load("rpi5-nas"); err != nil {
		t.Fatal(err)
	}
	old := m.Info().Fingerprint
	if err := m.Renew("nas-maison"); err != nil {
		t.Fatal(err)
	}
	c, _ := m.GetCertificate(nil)
	if Fingerprint(c.Certificate[0]) == old || !slices.Contains(c.Leaf.DNSNames, "nas-maison.local") {
		t.Fatal("GetCertificate still serves the previous certificate")
	}
}

func TestFingerprintFormatAndFileInfo(t *testing.T) {
	m, _ := newTestManager(t)
	if _, err := m.Load("rpi5-nas"); err != nil {
		t.Fatal(err)
	}
	fp := m.Info().Fingerprint
	// 32 bytes: 64 hex digits and 31 colons, upper case like browsers and openssl.
	if len(fp) != 95 || strings.Count(fp, ":") != 31 || fp != strings.ToUpper(fp) {
		t.Fatalf("fingerprint %q", fp)
	}
	fromFile, err := FileInfo(m.dir)
	if err != nil || fromFile.Fingerprint != fp {
		t.Fatalf("FileInfo = %q, %v; want %q", fromFile.Fingerprint, err, fp)
	}
}

func TestDockerInterfacesAreLeftOut(t *testing.T) {
	for name, want := range map[string]bool{
		"eth0": true, "end0": true, "wlan0": true, "tailscale0": true, "br0": true, "br-lan": true,
		"docker0": false, "br-dd4a991217fc": false, "veth3f2a1b0": false,
	} {
		if got := reachableInterface(name); got != want {
			t.Errorf("reachableInterface(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestGetCertificateBeforeLoad(t *testing.T) {
	m := NewManager(t.TempDir())
	if _, err := m.GetCertificate(nil); err == nil {
		t.Fatal("served a certificate before Load")
	}
	if m.Info().Fingerprint != "" {
		t.Fatal("info before Load")
	}
}
