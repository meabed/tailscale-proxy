package core

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
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
	"strings"
	"sync"
	"time"
)

// The local CA signs the certificates `tsp proxy` presents for *.localhost. Its
// name constraint limits it to localhost names, so even a leaked key cannot sign
// a certificate for a real domain. Files: ~/.tailscale-proxy/ca/{ca.pem,ca-key.pem}.

const (
	caValidity   = 10 * 365 * 24 * time.Hour
	leafValidity = 90 * 24 * time.Hour
	leafRenewal  = 24 * time.Hour // re-issue a cached leaf this close to expiry
	leafCacheMax = 256            // bound on cached leaf certificates
)

type localCA struct {
	cert    *x509.Certificate
	key     crypto.Signer
	certPEM string // path to ca.pem (NODE_EXTRA_CA_CERTS, trust)
}

func caFiles(dir string) (certPath, keyPath string) {
	return filepath.Join(dir, "ca", "ca.pem"), filepath.Join(dir, "ca", "ca-key.pem")
}

// ensureCA loads the CA from dir, creating it on first use. Creation runs under
// ca/ca.lock, so concurrent first starts make one CA, and a start that died
// half-way (a key without a certificate) is redone instead of breaking later ones.
func ensureCA(dir string) (*localCA, error) {
	certPath, keyPath := caFiles(dir)
	if ca, err := loadCA(certPath, keyPath); err == nil {
		return ca, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(certPath), 0o700); err != nil {
		return nil, err
	}
	for i := 0; ; i++ {
		release, ok, err := tryLock(filepath.Join(filepath.Dir(certPath), "ca.lock"))
		if err != nil {
			return nil, err
		}
		if ok {
			defer release()
			break
		}
		if i == 50 {
			return nil, fmt.Errorf("another tsp is creating the local CA in %s", filepath.Dir(certPath))
		}
		time.Sleep(100 * time.Millisecond)
	}
	if ca, err := loadCA(certPath, keyPath); err == nil {
		return ca, nil // made by the process that held the lock before us
	}
	return createCA(certPath, keyPath)
}

func loadCA(certPath, keyPath string) (*localCA, error) {
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		return nil, err
	}
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, err
	}
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, fmt.Errorf("reading the local CA in %s: %v (delete that folder to create a new one, then run tsp trust)", filepath.Dir(certPath), err)
	}
	cert, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return nil, fmt.Errorf("reading %s: %v", certPath, err)
	}
	signer, ok := pair.PrivateKey.(crypto.Signer)
	if !ok {
		return nil, fmt.Errorf("reading %s: unsupported key", keyPath)
	}
	return &localCA{cert: cert, key: signer, certPEM: certPath}, nil
}

func createCA(certPath, keyPath string) (*localCA, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	host, _ := os.Hostname()
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber:          randomSerial(),
		Subject:               pkix.Name{CommonName: "tailscale-proxy local CA (" + host + ")", Organization: []string{"tailscale-proxy"}},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(caValidity),
		IsCA:                  true,
		BasicConstraintsValid: true,
		MaxPathLenZero:        true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		// Every name type is limited, and the constraint is critical: a verifier that
		// cannot enforce it must reject the CA rather than trust it unconstrained.
		PermittedDNSDomainsCritical: true,
		PermittedDNSDomains:         []string{localTLD},
		PermittedIPRanges:           []*net.IPNet{{IP: net.IPv4(127, 0, 0, 0).To4(), Mask: net.CIDRMask(8, 32)}, {IP: net.IPv6loopback, Mask: net.CIDRMask(128, 128)}},
		PermittedEmailAddresses:     []string{localTLD},
		PermittedURIDomains:         []string{localTLD},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, err
	}
	// Under ensureCA's lock: the key, then the certificate, each written whole (temp + rename).
	if err := writeFileAtomic(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		return nil, err
	}
	if err := writeFileAtomic(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		return nil, err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	return &localCA{cert: cert, key: key, certPEM: certPath}, nil
}

func randomSerial() *big.Int {
	n, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	return n
}

// certIssuer hands out a certificate per requested name (SNI), signed by the CA
// and cached. Only "localhost" and names under .localhost are signed.
type certIssuer struct {
	ca  *localCA
	key *ecdsa.PrivateKey // one key for every leaf
	now func() time.Time

	mu    sync.Mutex
	cache map[string]*tls.Certificate
}

func newCertIssuer(ca *localCA) (*certIssuer, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	return &certIssuer{ca: ca, key: key, now: time.Now, cache: map[string]*tls.Certificate{}}, nil
}

// GetCertificate is the tls.Config hook.
func (i *certIssuer) GetCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	name := strings.TrimSuffix(strings.ToLower(hello.ServerName), ".")
	if name == "" {
		name = localTLD // no SNI (an IP, an old client)
	}
	if name != localTLD && !strings.HasSuffix(name, "."+localTLD) {
		return nil, fmt.Errorf("tsp proxy serves only %s names, not %q", localTLD, name)
	}
	return i.leaf(name)
}

func (i *certIssuer) leaf(name string) (*tls.Certificate, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	now := i.now()
	if c, ok := i.cache[name]; ok && c.Leaf.NotAfter.Sub(now) > leafRenewal {
		return c, nil
	}
	tmpl := &x509.Certificate{
		SerialNumber: randomSerial(),
		Subject:      pkix.Name{CommonName: name},
		DNSNames:     []string{name},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(leafValidity),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, i.ca.cert, &i.key.PublicKey, i.ca.key)
	if err != nil {
		return nil, fmt.Errorf("signing a certificate for %s: %v", name, err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	if len(i.cache) >= leafCacheMax {
		i.cache = map[string]*tls.Certificate{}
	}
	c := &tls.Certificate{Certificate: [][]byte{der, i.ca.cert.Raw}, PrivateKey: i.key, Leaf: leaf}
	i.cache[name] = c
	return c, nil
}

// caTrusted reports whether the OS trust store accepts the CA's certificates
// (macOS: the keychain's trust settings; Linux: the system bundle).
func caTrusted(ca *localCA) bool {
	iss, err := newCertIssuer(ca)
	if err != nil {
		return false
	}
	c, err := iss.leaf(localTLD)
	if err != nil {
		return false
	}
	_, err = c.Leaf.Verify(x509.VerifyOptions{DNSName: localTLD})
	return err == nil
}
