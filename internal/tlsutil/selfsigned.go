// SPDX-License-Identifier: AGPL-3.0-or-later

// Package tlsutil manages the self-signed certificate used for HTTPS on the LAN
// (ADR-0007, ADR-0019): created at first start, renewed at startup when it no
// longer covers the host's name or IPv4 addresses (or is about to expire),
// always with the same private key, and served hot so it can be swapped
// without restarting the daemon.
package tlsutil

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
)

// Validity of a generated certificate. Browsers warn anyway (self-signed); what
// matters is that it does not change often, so users accept it once.
const validity = 5 * 365 * 24 * time.Hour

// renewBefore renews a certificate this long before it expires.
const renewBefore = 30 * 24 * time.Hour

// Info describes the certificate being served, for the UI and the logs.
type Info struct {
	// Fingerprint is the SHA-256 of the certificate, as browsers and openssl
	// show it ("8E:8F:69:…"): the value a user compares to trust the NAS.
	Fingerprint string    `json:"fingerprint"`
	DNSNames    []string  `json:"dnsNames"`
	IPAddresses []string  `json:"ipAddresses"`
	NotBefore   time.Time `json:"notBefore"`
	NotAfter    time.Time `json:"notAfter"`
}

// Manager owns the certificate and key files in one directory.
type Manager struct {
	dir string
	// Now and Addrs are replaceable in tests.
	Now   func() time.Time
	Addrs func() []net.IP

	mu   sync.RWMutex
	cert *tls.Certificate
}

// NewManager returns a manager for dir (created on first write, mode 0700).
func NewManager(dir string) *Manager {
	return &Manager{dir: dir, Now: time.Now, Addrs: hostIPv4s}
}

func (m *Manager) certFile() string { return filepath.Join(m.dir, "cert.pem") }
func (m *Manager) keyFile() string  { return filepath.Join(m.dir, "key.pem") }

// Load serves the existing certificate, or renews it first when it is missing,
// expires within renewBefore, or does not cover hostname or one of the host's
// IPv4 addresses. It reports whether a new certificate was written.
func (m *Manager) Load(hostname string) (renewed bool, err error) {
	pair, err := tls.LoadX509KeyPair(m.certFile(), m.keyFile())
	if err == nil && pair.Leaf != nil && !m.needsRenewal(pair.Leaf, hostname) {
		m.set(&pair)
		return false, nil
	}
	if err := m.Renew(hostname); err != nil {
		return false, err
	}
	return true, nil
}

// Renew writes a new certificate for hostname and the host's addresses, signed
// by the existing private key when there is one (a new key otherwise), and
// serves it at once. Connections already open keep the previous one.
func (m *Manager) Renew(hostname string) error {
	key, err := m.loadOrCreateKey()
	if err != nil {
		return err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return err
	}
	now := m.Now()
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: firstNonEmpty(hostname, "nasbuster"), Organization: []string{"CrossWax NasBuster"}},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(validity),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              dnsNames(hostname),
		IPAddresses:           append([]net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback}, m.Addrs()...),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, key.Public(), key)
	if err != nil {
		return err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := writeFile(m.certFile(), certPEM, 0o644); err != nil {
		return err
	}
	pair, err := tls.LoadX509KeyPair(m.certFile(), m.keyFile())
	if err != nil {
		return err
	}
	m.set(&pair)
	return nil
}

// GetCertificate implements tls.Config.GetCertificate.
func (m *Manager) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.cert == nil {
		return nil, errors.New("tls: no certificate loaded")
	}
	return m.cert, nil
}

// Info describes the certificate being served (zero value before Load).
func (m *Manager) Info() Info {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.cert == nil || m.cert.Leaf == nil {
		return Info{}
	}
	return describe(m.cert.Leaf)
}

// Covers reports whether the certificate being served names hostname and every
// IPv4 address of the host, i.e. whether a renewal would change nothing.
func (m *Manager) Covers(hostname string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cert != nil && m.cert.Leaf != nil && m.covers(m.cert.Leaf, hostname)
}

func (m *Manager) set(pair *tls.Certificate) {
	m.mu.Lock()
	m.cert = pair
	m.mu.Unlock()
}

func (m *Manager) needsRenewal(leaf *x509.Certificate, hostname string) bool {
	return m.Now().Add(renewBefore).After(leaf.NotAfter) || !m.covers(leaf, hostname)
}

func (m *Manager) covers(leaf *x509.Certificate, hostname string) bool {
	for _, name := range dnsNames(hostname) {
		if !slices.ContainsFunc(leaf.DNSNames, func(n string) bool { return strings.EqualFold(n, name) }) {
			return false
		}
	}
	for _, ip := range m.Addrs() {
		if !slices.ContainsFunc(leaf.IPAddresses, ip.Equal) {
			return false
		}
	}
	return true
}

// loadOrCreateKey keeps the NAS's identity across renewals: only a missing or
// unreadable key is replaced.
func (m *Manager) loadOrCreateKey() (crypto.Signer, error) {
	if b, err := os.ReadFile(m.keyFile()); err == nil {
		if block, _ := pem.Decode(b); block != nil {
			if k, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
				if signer, ok := k.(crypto.Signer); ok {
					return signer, nil
				}
			}
		}
	}
	if err := os.MkdirAll(m.dir, 0o700); err != nil {
		return nil, err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, err
	}
	if err := writeFile(m.keyFile(), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		return nil, err
	}
	return key, nil
}

// FileInfo reads the certificate in dir without serving it (for the
// -print-tls-fingerprint flag used by the installer).
func FileInfo(dir string) (Info, error) {
	b, err := os.ReadFile(filepath.Join(dir, "cert.pem"))
	if err != nil {
		return Info{}, err
	}
	block, _ := pem.Decode(b)
	if block == nil || block.Type != "CERTIFICATE" {
		return Info{}, errors.New("tls: no certificate in cert.pem")
	}
	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return Info{}, err
	}
	return describe(leaf), nil
}

// Fingerprint formats the SHA-256 of a DER certificate as browsers show it.
func Fingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	parts := make([]string, len(sum))
	for i, b := range sum {
		parts[i] = fmt.Sprintf("%02X", b)
	}
	return strings.Join(parts, ":")
}

func describe(leaf *x509.Certificate) Info {
	ips := make([]string, 0, len(leaf.IPAddresses))
	for _, ip := range leaf.IPAddresses {
		ips = append(ips, ip.String())
	}
	return Info{
		Fingerprint: Fingerprint(leaf.Raw),
		DNSNames:    slices.Clone(leaf.DNSNames),
		IPAddresses: ips,
		NotBefore:   leaf.NotBefore,
		NotAfter:    leaf.NotAfter,
	}
}

func dnsNames(hostname string) []string {
	if hostname == "" || hostname == "localhost" {
		return []string{"localhost"}
	}
	return []string{hostname, hostname + ".local", "localhost"}
}

// hostIPv4s lists the host's IPv4 addresses (no loopback, no link-local).
// IPv6 global addresses are left out on purpose: privacy addresses rotate
// every day, and the certificate would be renewed at every restart.
func hostIPv4s() []net.IP {
	var ips []net.IP
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	for _, iface := range ifaces {
		if !reachableInterface(iface.Name) {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			if ipnet, ok := a.(*net.IPNet); ok {
				if ip4 := ipnet.IP.To4(); ip4 != nil && !ip4.IsLoopback() && !ip4.IsLinkLocalUnicast() {
					ips = append(ips, ip4)
				}
			}
		}
	}
	return ips
}

// reachableInterface leaves out Docker's own interfaces: nobody browses to the
// NAS through them, and every Compose network adds a bridge, which would renew
// the certificate (and bring the browser warning back) at the next start.
// Seen on the Pi 5: 172.17.0.1, 172.18.0.1 and 172.19.0.1 in the certificate.
func reachableInterface(name string) bool {
	return name != "docker0" && !dockerBridgeRe.MatchString(name) && !strings.HasPrefix(name, "veth")
}

// dockerBridgeRe: Docker names a network's bridge "br-" + the first 12 hex
// digits of its id. A bridge the user made (br0, br-lan) stays in.
var dockerBridgeRe = regexp.MustCompile(`^br-[0-9a-f]{12}$`)

func writeFile(path string, data []byte, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, perm); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return os.Rename(tmp, path)
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
