// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"net"
	"net/http"
	"path/filepath"
	"slices"
	"testing"

	"github.com/crosswax/nasbuster/internal/tlsutil"
)

func TestCertificateWithoutTLS(t *testing.T) {
	e := newEnv(t)
	e.setup()
	var view certificateView
	e.do("GET", "/api/v1/system/certificate", nil, http.StatusOK, &view)
	if view.Enabled || view.Fingerprint != "" {
		t.Fatalf("view = %+v", view)
	}
	if c := errCode(t, e, "POST", "/api/v1/system/certificate/renew", nil, http.StatusConflict); c != "certificate.disabled" {
		t.Fatalf("renew without TLS: %s", c)
	}
}

func TestCertificateViewAndRenew(t *testing.T) {
	certs := tlsutil.NewManager(filepath.Join(t.TempDir(), "tls"))
	certs.Addrs = func() []net.IP { return []net.IP{net.ParseIP("192.168.1.50").To4()} }
	// The certificate was made for an older name: the demo Pi is "raspberrypi".
	if _, err := certs.Load("old-name"); err != nil {
		t.Fatal(err)
	}
	e := newEnvWith(t, func(d *Deps) { d.Certificates = certs })
	e.setup()

	var view certificateView
	e.do("GET", "/api/v1/system/certificate", nil, http.StatusOK, &view)
	if !view.Enabled || view.CoversHostname || view.Hostname != "raspberrypi" || len(view.Fingerprint) != 95 {
		t.Fatalf("before renewal: %+v", view)
	}

	var renewed certificateView
	e.do("POST", "/api/v1/system/certificate/renew", nil, http.StatusOK, &renewed)
	if !renewed.CoversHostname || renewed.Fingerprint == view.Fingerprint || !slices.Contains(renewed.DNSNames, "raspberrypi.local") {
		t.Fatalf("after renewal: %+v", renewed)
	}
	if certs.Info().Fingerprint != renewed.Fingerprint {
		t.Fatal("the renewed certificate is not the one served")
	}
}
