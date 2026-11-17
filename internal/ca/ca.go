// Package ca issues a local root CA and mints per-host leaf certificates so the
// proxy can terminate TLS the same way Burp does.
package ca

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type CA struct {
	cert  *x509.Certificate
	key   *ecdsa.PrivateKey
	pem   []byte
	dir   string
	mu    sync.Mutex
	cache map[string]*tls.Certificate
}

func LoadOrCreate(dir string) (*CA, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	certPath := filepath.Join(dir, "ca.pem")
	keyPath := filepath.Join(dir, "ca-key.pem")

	if certPEM, err := os.ReadFile(certPath); err == nil {
		keyPEM, err := os.ReadFile(keyPath)
		if err != nil {
			return nil, fmt.Errorf("root key missing next to %s: %w", certPath, err)
		}
		cb, _ := pem.Decode(certPEM)
		kb, _ := pem.Decode(keyPEM)
		if cb == nil || kb == nil {
			return nil, fmt.Errorf("malformed CA files in %s", dir)
		}
		cert, err := x509.ParseCertificate(cb.Bytes)
		if err != nil {
			return nil, err
		}
		keyAny, err := x509.ParsePKCS8PrivateKey(kb.Bytes)
		if err != nil {
			return nil, err
		}
		key, ok := keyAny.(*ecdsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("CA key is not ECDSA")
		}
		return &CA{cert: cert, key: key, pem: certPEM, dir: dir, cache: map[string]*tls.Certificate{}}, nil
	}

	// Do not silently mint a new CA when only the certificate is missing: the
	// browser may already trust the previous root, and rotating it would break
	// HTTPS interception until the new CA is imported again.
	if _, err := os.Stat(keyPath); err == nil {
		return nil, fmt.Errorf("root certificate missing at %s (found %s); restore it, or delete both files to create a new CA", certPath, keyPath)
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, err
	}
	tpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "Eko Root CA", Organization: []string{"Eko"}},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(10 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	if err := os.WriteFile(certPath, certPEM, 0o644); err != nil {
		return nil, err
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		return nil, err
	}
	return &CA{cert: cert, key: key, pem: certPEM, dir: dir, cache: map[string]*tls.Certificate{}}, nil
}

func (c *CA) PEM() []byte { return c.pem }
func (c *CA) Dir() string { return c.dir }

// TLSConfig advertises only HTTP/1.1 to the browser, which keeps raw request
// editing and Repeater sane. Upstream can still speak h2.
func (c *CA) TLSConfig(connectHost string) *tls.Config {
	return &tls.Config{
		MinVersion: tls.VersionTLS12,
		NextProtos: []string{"http/1.1"},
		GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
			host := hello.ServerName
			if host == "" {
				host = stripPort(connectHost)
			}
			return c.leaf(host)
		},
	}
}

func stripPort(h string) string {
	if host, _, err := net.SplitHostPort(h); err == nil {
		return host
	}
	return h
}

func (c *CA) leaf(host string) (*tls.Certificate, error) {
	if host == "" {
		host = "localhost"
	}
	c.mu.Lock()
	if cert, ok := c.cache[host]; ok {
		c.mu.Unlock()
		return cert, nil
	}
	c.mu.Unlock()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, err
	}
	tpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: host, Organization: []string{"Eko"}},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(90 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	if ip := net.ParseIP(host); ip != nil {
		tpl.IPAddresses = []net.IP{ip}
	} else {
		tpl.DNSNames = []string{host}
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, c.cert, &key.PublicKey, c.key)
	if err != nil {
		return nil, err
	}
	cert := &tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
	c.mu.Lock()
	c.cache[host] = cert
	c.mu.Unlock()
	return cert, nil
}
