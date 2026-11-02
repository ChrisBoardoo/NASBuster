// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"net/http"

	"github.com/crosswax/nasbuster/internal/store"
	"github.com/crosswax/nasbuster/internal/tlsutil"
)

// certificateView is what the UI shows about the HTTPS certificate (ADR-0019):
// the fingerprint to compare with the browser's, the names it covers, and
// whether it still matches the host (after a rename it is renewed at the next
// start, or now on request).
type certificateView struct {
	Enabled bool `json:"enabled"`
	tlsutil.Info
	Hostname       string `json:"hostname,omitempty"`
	CoversHostname bool   `json:"coversHostname"`
}

func (s *Server) certificateView() certificateView {
	if s.d.Certificates == nil {
		return certificateView{}
	}
	host := s.d.Prober.Info().Hostname
	return certificateView{
		Enabled:        true,
		Info:           s.d.Certificates.Info(),
		Hostname:       host,
		CoversHostname: s.d.Certificates.Covers(host),
	}
}

func (s *Server) handleCertificate(w http.ResponseWriter, _ *http.Request, _ store.User) {
	writeJSON(w, http.StatusOK, s.certificateView())
}

// handleCertificateRenew renews the certificate for the current name and
// addresses, with the same private key. Browsers warn once more: the UI says
// so before asking.
func (s *Server) handleCertificateRenew(w http.ResponseWriter, r *http.Request, user store.User) {
	if s.d.Certificates == nil {
		writeError(w, http.StatusConflict, "certificate.disabled", "HTTPS is not enabled on this NAS", nil)
		return
	}
	host := s.d.Prober.Info().Hostname
	if err := s.d.Certificates.Renew(host); err != nil {
		s.internalError(w, "renew certificate", err)
		return
	}
	view := s.certificateView()
	s.audit(r, user.Username, "system.certificate_renew", host, "ok", view.Fingerprint)
	writeJSON(w, http.StatusOK, view)
}
