// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

// Package auth provides OIDC JWT validation and mTLS helpers for the Tier 2
// API server. It has no external dependencies beyond the Go standard library
// and the crypto primitives already in the runtime.
package auth

import (
	"crypto"
	"crypto/rsa"
	_ "crypto/sha256" // register crypto.SHA256
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// ---- OIDC ------------------------------------------------------------------

// OIDCVerifier fetches the JWKS from the issuer's discovery endpoint and
// validates RS256 JWTs. Keys are cached and refreshed when a token presents
// an unknown kid.
type OIDCVerifier struct {
	issuer   string
	audience string
	client   *http.Client

	mu   sync.RWMutex
	keys map[string]*rsa.PublicKey // kid → key
}

// NewOIDCVerifier creates a verifier for the given issuer and audience.
// It fetches the JWKS eagerly on construction.
func NewOIDCVerifier(issuer, audience string) (*OIDCVerifier, error) {
	if issuer == "" {
		return nil, errors.New("auth: OIDC issuer must not be empty")
	}
	v := &OIDCVerifier{
		issuer:   strings.TrimRight(issuer, "/"),
		audience: audience,
		client:   &http.Client{Timeout: 10 * time.Second},
		keys:     make(map[string]*rsa.PublicKey),
	}
	if err := v.refreshKeys(); err != nil {
		return nil, fmt.Errorf("auth: fetch JWKS: %w", err)
	}
	return v, nil
}

// Verify validates tokenStr (a raw JWT) and returns nil on success.
// It re-fetches the JWKS at most once per unknown kid.
func (v *OIDCVerifier) Verify(tokenStr string) error {
	header, payload, sig, msg, err := splitJWT(tokenStr)
	if err != nil {
		return err
	}

	// Check algorithm.
	if header.Alg != "RS256" {
		return fmt.Errorf("auth: unsupported JWT alg %q", header.Alg)
	}

	// Find key; refresh once if unknown.
	key, err := v.findKey(header.Kid)
	if err != nil {
		return err
	}

	// Verify signature.
	if err := verifyRS256(msg, sig, key); err != nil {
		return fmt.Errorf("auth: signature invalid: %w", err)
	}

	// Validate claims.
	now := time.Now().Unix()
	if payload.Exp > 0 && now > payload.Exp {
		return errors.New("auth: token expired")
	}
	if payload.Iss != v.issuer {
		return fmt.Errorf("auth: iss %q does not match issuer %q", payload.Iss, v.issuer)
	}
	if v.audience != "" {
		if !containsAudience(payload.Aud, v.audience) {
			return fmt.Errorf("auth: aud %v does not contain expected audience %q", payload.Aud, v.audience)
		}
	}
	return nil
}

// Middleware wraps next with OIDC JWT validation. Health probes bypass auth.
func (v *OIDCVerifier) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" || r.URL.Path == "/readyz" {
			next.ServeHTTP(w, r)
			return
		}
		auth := r.Header.Get("Authorization")
		token, ok := strings.CutPrefix(auth, "Bearer ")
		if !ok || token == "" {
			http.Error(w, `{"error":"missing bearer token"}`, http.StatusUnauthorized)
			return
		}
		if err := v.Verify(token); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ---- key management --------------------------------------------------------

func (v *OIDCVerifier) findKey(kid string) (*rsa.PublicKey, error) {
	v.mu.RLock()
	key, ok := v.keys[kid]
	v.mu.RUnlock()
	if ok {
		return key, nil
	}
	// Unknown kid — refresh once.
	if err := v.refreshKeys(); err != nil {
		return nil, fmt.Errorf("auth: refresh JWKS: %w", err)
	}
	v.mu.RLock()
	key, ok = v.keys[kid]
	v.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("auth: unknown key id %q", kid)
	}
	return key, nil
}

func (v *OIDCVerifier) refreshKeys() error {
	// 1. Fetch discovery document.
	discURL := v.issuer + "/.well-known/openid-configuration"
	resp, err := v.client.Get(discURL) //nolint:noctx — no context, deliberately simple
	if err != nil {
		return fmt.Errorf("fetch discovery %s: %w", discURL, err)
	}
	defer resp.Body.Close()
	var disc struct {
		JWKSURI string `json:"jwks_uri"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&disc); err != nil {
		return fmt.Errorf("decode discovery: %w", err)
	}

	// 2. Fetch JWKS.
	resp2, err := v.client.Get(disc.JWKSURI)
	if err != nil {
		return fmt.Errorf("fetch JWKS %s: %w", disc.JWKSURI, err)
	}
	defer resp2.Body.Close()
	var jwks struct {
		Keys []struct {
			Kid string `json:"kid"`
			Kty string `json:"kty"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	if err := json.NewDecoder(resp2.Body).Decode(&jwks); err != nil {
		return fmt.Errorf("decode JWKS: %w", err)
	}

	// 3. Parse RSA keys.
	newKeys := make(map[string]*rsa.PublicKey, len(jwks.Keys))
	for _, k := range jwks.Keys {
		if k.Kty != "RSA" {
			continue
		}
		pub, err := parseRSAKey(k.N, k.E)
		if err != nil {
			return fmt.Errorf("parse RSA key %q: %w", k.Kid, err)
		}
		newKeys[k.Kid] = pub
	}

	v.mu.Lock()
	v.keys = newKeys
	v.mu.Unlock()
	return nil
}

// ---- JWT parsing -----------------------------------------------------------

type jwtHeader struct {
	Alg string `json:"alg"`
	Kid string `json:"kid"`
}

type jwtClaims struct {
	Iss string   `json:"iss"`
	Exp int64    `json:"exp"`
	Aud audClaim `json:"aud"`
}

// audClaim accepts both a single string and an array of strings.
type audClaim []string

func (a *audClaim) UnmarshalJSON(b []byte) error {
	// Try array first.
	var arr []string
	if err := json.Unmarshal(b, &arr); err == nil {
		*a = arr
		return nil
	}
	// Try single string.
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	*a = []string{s}
	return nil
}

// splitJWT parses a compact JWT into header, payload, signature, and the
// signed message (header.payload bytes before signing).
func splitJWT(token string) (jwtHeader, jwtClaims, []byte, []byte, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return jwtHeader{}, jwtClaims{}, nil, nil, errors.New("auth: malformed JWT: expected 3 parts")
	}

	hdrBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return jwtHeader{}, jwtClaims{}, nil, nil, fmt.Errorf("auth: decode JWT header: %w", err)
	}
	var hdr jwtHeader
	if err := json.Unmarshal(hdrBytes, &hdr); err != nil {
		return jwtHeader{}, jwtClaims{}, nil, nil, fmt.Errorf("auth: parse JWT header: %w", err)
	}

	payBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return jwtHeader{}, jwtClaims{}, nil, nil, fmt.Errorf("auth: decode JWT payload: %w", err)
	}
	var pay jwtClaims
	if err := json.Unmarshal(payBytes, &pay); err != nil {
		return jwtHeader{}, jwtClaims{}, nil, nil, fmt.Errorf("auth: parse JWT claims: %w", err)
	}

	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return jwtHeader{}, jwtClaims{}, nil, nil, fmt.Errorf("auth: decode JWT signature: %w", err)
	}

	msg := []byte(parts[0] + "." + parts[1])
	return hdr, pay, sig, msg, nil
}

func containsAudience(aud audClaim, want string) bool {
	for _, a := range aud {
		if subtle.ConstantTimeCompare([]byte(a), []byte(want)) == 1 {
			return true
		}
	}
	return false
}

// ---- RSA key parsing -------------------------------------------------------

func parseRSAKey(nB64, eB64 string) (*rsa.PublicKey, error) {
	nBytes, err := base64.RawURLEncoding.DecodeString(nB64)
	if err != nil {
		return nil, fmt.Errorf("decode n: %w", err)
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(eB64)
	if err != nil {
		return nil, fmt.Errorf("decode e: %w", err)
	}
	n := new(big.Int).SetBytes(nBytes)
	e := new(big.Int).SetBytes(eBytes)
	return &rsa.PublicKey{N: n, E: int(e.Int64())}, nil
}

// verifyRS256 verifies a PKCS1v15 / SHA-256 signature.
func verifyRS256(msg, sig []byte, key *rsa.PublicKey) error {
	h := crypto.SHA256.New()
	h.Write(msg)
	return rsa.VerifyPKCS1v15(key, crypto.SHA256, h.Sum(nil), sig)
}

// ---- mTLS ------------------------------------------------------------------

// NewMTLSTLSConfig returns a *tls.Config that requires and verifies client
// certificates signed by the CA at caCertPath. The returned config should be
// applied to http.Server.TLSConfig after loading the server cert/key.
func NewMTLSTLSConfig(caCertPath string) (*tls.Config, error) {
	pemData, err := os.ReadFile(caCertPath)
	if err != nil {
		return nil, fmt.Errorf("auth: read CA cert %q: %w", caCertPath, err)
	}
	pool := x509.NewCertPool()
	for len(pemData) > 0 {
		var block *pem.Block
		block, pemData = pem.Decode(pemData)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("auth: parse CA cert: %w", err)
		}
		pool.AddCert(cert)
	}
	return &tls.Config{
		ClientAuth: tls.RequireAndVerifyClientCert,
		ClientCAs:  pool,
		MinVersion: tls.VersionTLS13,
	}, nil
}
