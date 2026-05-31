// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package auth_test

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	_ "crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MediaMolder/MediaMolder/internal/auth"
)

// ---- helpers ---------------------------------------------------------------

func makeRSAKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	return k
}

func b64url(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// buildJWT creates a compact JWS (alg=RS256) with the given claims.
func buildJWT(t *testing.T, key *rsa.PrivateKey, kid string, claims map[string]any) string {
	t.Helper()
	hdr, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": kid, "typ": "JWT"})
	pay, _ := json.Marshal(claims)
	msg := b64url(hdr) + "." + b64url(pay)
	h := crypto.SHA256.New()
	h.Write([]byte(msg))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, h.Sum(nil))
	if err != nil {
		t.Fatalf("sign JWT: %v", err)
	}
	return msg + "." + b64url(sig)
}

// startOIDCServer starts a mock OIDC discovery + JWKS server and returns
// (issuer, cleanup).
func startOIDCServer(t *testing.T, key *rsa.PrivateKey, kid string) (issuer string, cleanup func()) {
	t.Helper()
	mux := http.NewServeMux()

	// Use r.Host so we don't need to know the server URL before it starts.
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		json.NewEncoder(w).Encode(map[string]string{ //nolint:errcheck
			"jwks_uri": scheme + "://" + r.Host + "/jwks",
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		nBytes := key.PublicKey.N.Bytes()
		e := big.NewInt(int64(key.PublicKey.E))
		json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
			"keys": []map[string]string{
				{"kty": "RSA", "kid": kid, "alg": "RS256", "n": b64url(nBytes), "e": b64url(e.Bytes())},
			},
		})
	})

	srv := httptest.NewServer(mux)
	return srv.URL, srv.Close
}

// ---- tests -----------------------------------------------------------------

func TestOIDCVerify_valid(t *testing.T) {
	key := makeRSAKey(t)
	issuer, cleanup := startOIDCServer(t, key, "k1")
	defer cleanup()

	v, err := auth.NewOIDCVerifier(issuer, "myapp")
	if err != nil {
		t.Fatalf("NewOIDCVerifier: %v", err)
	}

	token := buildJWT(t, key, "k1", map[string]any{
		"iss": issuer,
		"aud": "myapp",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	if err := v.Verify(token); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestOIDCVerify_expired(t *testing.T) {
	key := makeRSAKey(t)
	issuer, cleanup := startOIDCServer(t, key, "k1")
	defer cleanup()

	v, err := auth.NewOIDCVerifier(issuer, "myapp")
	if err != nil {
		t.Fatalf("NewOIDCVerifier: %v", err)
	}

	token := buildJWT(t, key, "k1", map[string]any{
		"iss": issuer,
		"aud": "myapp",
		"exp": time.Now().Add(-time.Hour).Unix(), // already expired
	})
	if err := v.Verify(token); err == nil {
		t.Fatal("expected error for expired token")
	}
}

func TestOIDCVerify_wrongIssuer(t *testing.T) {
	key := makeRSAKey(t)
	issuer, cleanup := startOIDCServer(t, key, "k1")
	defer cleanup()

	v, err := auth.NewOIDCVerifier(issuer, "myapp")
	if err != nil {
		t.Fatalf("NewOIDCVerifier: %v", err)
	}

	token := buildJWT(t, key, "k1", map[string]any{
		"iss": "https://evil.example.com",
		"aud": "myapp",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	if err := v.Verify(token); err == nil {
		t.Fatal("expected error for wrong issuer")
	}
}

func TestOIDCVerify_wrongAudience(t *testing.T) {
	key := makeRSAKey(t)
	issuer, cleanup := startOIDCServer(t, key, "k1")
	defer cleanup()

	v, err := auth.NewOIDCVerifier(issuer, "myapp")
	if err != nil {
		t.Fatalf("NewOIDCVerifier: %v", err)
	}

	token := buildJWT(t, key, "k1", map[string]any{
		"iss": issuer,
		"aud": "other-client",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	if err := v.Verify(token); err == nil {
		t.Fatal("expected error for wrong audience")
	}
}

func TestOIDCVerify_invalidSignature(t *testing.T) {
	key := makeRSAKey(t)
	issuer, cleanup := startOIDCServer(t, key, "k1")
	defer cleanup()

	v, err := auth.NewOIDCVerifier(issuer, "myapp")
	if err != nil {
		t.Fatalf("NewOIDCVerifier: %v", err)
	}

	token := buildJWT(t, key, "k1", map[string]any{
		"iss": issuer,
		"aud": "myapp",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	// Tamper with signature portion.
	parts := strings.Split(token, ".")
	parts[2] = b64url([]byte("badsig"))
	tampered := strings.Join(parts, ".")
	if err := v.Verify(tampered); err == nil {
		t.Fatal("expected error for tampered signature")
	}
}

func TestOIDCVerify_audAsArray(t *testing.T) {
	key := makeRSAKey(t)
	issuer, cleanup := startOIDCServer(t, key, "k1")
	defer cleanup()

	v, err := auth.NewOIDCVerifier(issuer, "myapp")
	if err != nil {
		t.Fatalf("NewOIDCVerifier: %v", err)
	}

	// aud as JSON array.
	token := buildJWT(t, key, "k1", map[string]any{
		"iss": issuer,
		"aud": []string{"other", "myapp"},
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	if err := v.Verify(token); err != nil {
		t.Fatalf("Verify with aud array: %v", err)
	}
}

func TestOIDCMiddleware(t *testing.T) {
	key := makeRSAKey(t)
	issuer, cleanup := startOIDCServer(t, key, "k1")
	defer cleanup()

	v, err := auth.NewOIDCVerifier(issuer, "myapp")
	if err != nil {
		t.Fatalf("NewOIDCVerifier: %v", err)
	}

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := v.Middleware(inner)

	// Valid token → 200.
	token := buildJWT(t, key, "k1", map[string]any{
		"iss": issuer, "aud": "myapp", "exp": time.Now().Add(time.Hour).Unix(),
	})
	req := httptest.NewRequest(http.MethodGet, "/jobs", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	// No token → 401.
	req2 := httptest.NewRequest(http.MethodGet, "/jobs", nil)
	rr2 := httptest.NewRecorder()
	handler.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr2.Code)
	}

	// /healthz is exempt.
	req3 := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rr3 := httptest.NewRecorder()
	handler.ServeHTTP(rr3, req3)
	if rr3.Code != http.StatusOK {
		t.Fatalf("expected 200 for /healthz, got %d", rr3.Code)
	}
}

func TestNewMTLSTLSConfig(t *testing.T) {
	// Generate a self-signed CA cert for testing.
	caKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("gen CA key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test-ca"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		IsCA:         true,
		KeyUsage:     x509.KeyUsageCertSign,
	}
	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("create CA cert: %v", err)
	}

	// Write PEM to temp file.
	tmpDir := t.TempDir()
	caPath := filepath.Join(tmpDir, "ca.pem")
	f, err := os.Create(caPath)
	if err != nil {
		t.Fatalf("create tmp CA file: %v", err)
	}
	if err := pem.Encode(f, &pem.Block{Type: "CERTIFICATE", Bytes: certDER}); err != nil {
		f.Close()
		t.Fatalf("write CA PEM: %v", err)
	}
	f.Close()

	cfg, err := auth.NewMTLSTLSConfig(caPath)
	if err != nil {
		t.Fatalf("NewMTLSTLSConfig: %v", err)
	}
	if cfg.ClientAuth != tls.RequireAndVerifyClientCert {
		t.Errorf("expected RequireAndVerifyClientCert, got %v", cfg.ClientAuth)
	}
	if cfg.ClientCAs == nil {
		t.Error("expected non-nil ClientCAs pool")
	}
}

func TestNewMTLSTLSConfig_missingFile(t *testing.T) {
	_, err := auth.NewMTLSTLSConfig("/nonexistent/ca.pem")
	if err == nil {
		t.Fatal("expected error for missing CA file")
	}
}
