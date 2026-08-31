package cli

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"math/big"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/noderings/cli/internal/config"
)

func TestResolvePeeringAPIServerURLPrefersCertValidHost(t *testing.T) {
	t.Parallel()

	caPEM, serverCert := testCertWithSANs(t, []string{"192.168.1.70", "127.0.0.1"})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()

	_, portStr, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatalf("split host port: %v", err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("atoi: %v", err)
	}

	done := make(chan struct{})
	defer close(done)
	go func() {
		for {
			select {
			case <-done:
				return
			default:
			}
			raw, acceptErr := ln.Accept()
			if acceptErr != nil {
				return
			}
			tlsConn := tls.Server(raw, &tls.Config{
				Certificates: []tls.Certificate{serverCert},
				MinVersion:   tls.VersionTLS12,
			})
			_ = tlsConn.Handshake()
			_ = tlsConn.Close()
		}
	}()

	kubeconfig := testKubeconfigWithCA(t, caPEM, "https://127.0.0.1:"+portStr)
	cfg := &config.Config{
		Mothership: config.MothershipConfig{
			// Dial the local test listener; cluster-internal SAN must not steal insecure fallback.
			Host:       "127.0.0.1",
			K8sAPIHost: "10.101.0.1",
			K8sAPIPort: port,
		},
	}

	gotURL, insecure := resolvePeeringAPIServerURL(kubeconfig, cfg)
	if insecure {
		t.Fatalf("expected secure rewrite when CA validates, got insecure=true url=%q", gotURL)
	}
	want := "https://127.0.0.1:" + portStr
	if gotURL != want {
		t.Fatalf("server URL = %q, want %q", gotURL, want)
	}
}

func TestResolvePeeringAPIServerURLFallbackInsecure(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		Mothership: config.MothershipConfig{
			Host:        "192.168.2.1",
			K8sAPIHost:  "10.101.0.1", // cluster-internal; must not win insecure fallback
			K8sAPIPort:  16443,
			TLSInsecure: true,
		},
	}

	gotURL, insecure := resolvePeeringAPIServerURL("", cfg)
	if gotURL != "https://192.168.2.1:16443" {
		t.Fatalf("server URL = %q, want mothership.host (not k8s_api_host)", gotURL)
	}
	if !insecure {
		t.Fatal("expected insecure fallback when no CA validates")
	}
}

func TestPeeringAPIServerCandidates(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		Mothership: config.MothershipConfig{
			Host:       "192.168.2.1",
			K8sAPIHost: "192.168.1.70",
		},
	}
	kubeconfig := `apiVersion: v1
clusters:
- cluster:
    server: https://10.0.0.5:6443
  name: c
`

	got := peeringAPIServerCandidates(kubeconfig, cfg)
	if len(got) < 3 {
		t.Fatalf("candidates = %v, want at least 3 entries", got)
	}
	if got[0] != "192.168.2.1" {
		t.Fatalf("first candidate = %q, want mothership.host first", got[0])
	}
	if got[len(got)-1] != "192.168.1.70" {
		t.Fatalf("last candidate = %q, want k8s_api_host last", got[len(got)-1])
	}
}

func testKubeconfigWithCA(t *testing.T, caPEM, server string) string {
	t.Helper()
	block, _ := pem.Decode([]byte(caPEM))
	if block == nil {
		t.Fatal("invalid ca pem")
	}
	b64 := base64.StdEncoding.EncodeToString(block.Bytes)
	return `apiVersion: v1
clusters:
- cluster:
    certificate-authority-data: ` + b64 + `
    server: ` + server + `
  name: remote
contexts:
- context:
    cluster: remote
    user: u
  name: remote
current-context: remote
kind: Config
users:
- name: u
  user:
    token: t
`
}

func testCertWithSANs(t *testing.T, sans []string) (string, tls.Certificate) {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	for _, san := range sans {
		if ip := net.ParseIP(san); ip != nil {
			template.IPAddresses = append(template.IPAddresses, ip)
		}
	}

	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	return string(certPEM), tls.Certificate{
		Certificate: [][]byte{der},
		PrivateKey:  key,
	}
}
