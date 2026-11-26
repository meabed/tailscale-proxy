package core

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func testCA(t *testing.T) (*localCA, string) {
	t.Helper()
	dir := t.TempDir()
	ca, err := ensureCA(dir)
	if err != nil {
		t.Fatal(err)
	}
	return ca, dir
}

func TestEnsureCA_createsThenReloads(t *testing.T) {
	ca, dir := testCA(t)
	if !ca.cert.IsCA || len(ca.cert.PermittedDNSDomains) != 1 || ca.cert.PermittedDNSDomains[0] != localTLD {
		t.Errorf("CA = IsCA %v, constraints %v; want a CA limited to %s", ca.cert.IsCA, ca.cert.PermittedDNSDomains, localTLD)
	}
	certPath, keyPath := caFiles(dir)
	if ca.certPEM != certPath {
		t.Errorf("certPEM = %q, want %q", ca.certPEM, certPath)
	}
	if runtime.GOOS != "windows" {
		if fi, _ := os.Stat(keyPath); fi.Mode().Perm() != 0o600 {
			t.Errorf("key mode = %v, want 0600", fi.Mode().Perm())
		}
	}
	again, err := ensureCA(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !again.cert.Equal(ca.cert) {
		t.Error("ensureCA created a second CA instead of loading the first")
	}
}

func TestEnsureCA_corrupt(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := caFiles(dir)
	_ = os.MkdirAll(filepath.Dir(certPath), 0o700)
	_ = os.WriteFile(certPath, []byte("not a cert"), 0o644)
	_ = os.WriteFile(keyPath, []byte("not a key"), 0o600)
	if _, err := ensureCA(dir); err == nil {
		t.Fatal("ensureCA accepted a corrupt CA")
	}
}

func TestEnsureCA_redoesAHalfWrittenCA(t *testing.T) {
	// A first start that died after the key, before the certificate: the next one makes a whole CA.
	dir := t.TempDir()
	_, keyPath := caFiles(dir)
	_ = os.MkdirAll(filepath.Dir(keyPath), 0o700)
	_ = os.WriteFile(keyPath, []byte("partial"), 0o600)
	ca, err := ensureCA(dir)
	if err != nil {
		t.Fatal(err)
	}
	again, err := loadCA(caFiles(dir))
	if err != nil || !again.cert.Equal(ca.cert) {
		t.Fatalf("the redone CA does not load back: %v", err)
	}
}

func TestEnsureCA_waitsForAConcurrentCreator(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, "ca"), 0o700)
	release, _, _ := tryLock(filepath.Join(dir, "ca", "ca.lock"))
	go func() {
		time.Sleep(150 * time.Millisecond)
		release()
	}()
	if _, err := ensureCA(dir); err != nil {
		t.Fatalf("ensureCA after the lock was released: %v", err)
	}
}

func pool(ca *localCA) *x509.CertPool {
	p := x509.NewCertPool()
	p.AddCert(ca.cert)
	return p
}

func TestCertIssuer_namesAndChain(t *testing.T) {
	ca, _ := testCA(t)
	iss, err := newCertIssuer(ca)
	if err != nil {
		t.Fatal(err)
	}
	for sni, want := range map[string]string{
		"api.acme.localhost":  "api.acme.localhost",
		"API.ACME.localhost.": "api.acme.localhost",
		"":                    "localhost",
		"localhost":           "localhost",
	} {
		c, err := iss.GetCertificate(&tls.ClientHelloInfo{ServerName: sni})
		if err != nil {
			t.Fatalf("GetCertificate(%q): %v", sni, err)
		}
		if _, err := c.Leaf.Verify(x509.VerifyOptions{DNSName: want, Roots: pool(ca)}); err != nil {
			t.Errorf("leaf for %q does not verify as %q: %v", sni, want, err)
		}
		if len(c.Certificate) != 2 {
			t.Errorf("leaf for %q has %d certs, want leaf + CA", sni, len(c.Certificate))
		}
	}
	for _, sni := range []string{"example.com", "localhost.example.com", "evil-localhost"} {
		if _, err := iss.GetCertificate(&tls.ClientHelloInfo{ServerName: sni}); err == nil {
			t.Errorf("GetCertificate(%q) signed a non-localhost name", sni)
		}
	}
}

func TestCertIssuer_cacheRenewAndBound(t *testing.T) {
	ca, _ := testCA(t)
	iss, _ := newCertIssuer(ca)
	now := time.Now()
	iss.now = func() time.Time { return now }
	a, _ := iss.leaf("a.localhost")
	if b, _ := iss.leaf("a.localhost"); b != a {
		t.Error("a second request for the same name issued a new certificate")
	}
	now = now.Add(leafValidity - leafRenewal + time.Minute)
	if c, _ := iss.leaf("a.localhost"); c == a {
		t.Error("a certificate close to expiry was not re-issued")
	}
	for i := 0; i < leafCacheMax+5; i++ {
		_, _ = iss.leaf(string(rune('a'+i%26)) + string(rune('a'+i/26)) + ".localhost")
	}
	if len(iss.cache) > leafCacheMax {
		t.Errorf("cache holds %d certificates, more than %d", len(iss.cache), leafCacheMax)
	}
}

func TestCA_constraintsCoverEveryNameType(t *testing.T) {
	ca, _ := testCA(t)
	c := ca.cert
	if !c.PermittedDNSDomainsCritical || len(c.PermittedIPRanges) != 2 || len(c.PermittedEmailAddresses) != 1 || len(c.PermittedURIDomains) != 1 {
		t.Fatalf("constraints: critical=%v ip=%v email=%v uri=%v", c.PermittedDNSDomainsCritical, c.PermittedIPRanges, c.PermittedEmailAddresses, c.PermittedURIDomains)
	}
	// A certificate the CA key signs for a public IP is rejected.
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{
		SerialNumber: randomSerial(), Subject: pkix.Name{CommonName: "ip"},
		IPAddresses: []net.IP{net.ParseIP("8.8.8.8")}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, _ := x509.ParseCertificate(der)
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: pool(ca)}); err == nil {
		t.Fatal("a certificate for 8.8.8.8 verified under the localhost-only CA")
	}
}

func TestCA_nameConstraintBlocksOtherDomains(t *testing.T) {
	// Even a certificate the CA key signs for a real domain is rejected by verifiers.
	ca, _ := testCA(t)
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{
		SerialNumber: randomSerial(), Subject: pkix.Name{CommonName: "example.com"},
		DNSNames: []string{"example.com"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, _ := x509.ParseCertificate(der)
	if _, err := leaf.Verify(x509.VerifyOptions{DNSName: "example.com", Roots: pool(ca)}); err == nil {
		t.Fatal("a certificate for example.com verified under the localhost-only CA")
	}
}

func TestCATrusted_freshCAIsNot(t *testing.T) {
	ca, _ := testCA(t)
	if caTrusted(ca) {
		t.Error("a CA created a moment ago is reported as trusted by the OS")
	}
}
