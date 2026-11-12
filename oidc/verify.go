package oidc

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"math/big"
	"slices"
	"strings"
	"time"
)

// maxKidLen bounds the key id read out of an unverified token header, so
// nothing unbounded that a stranger wrote reaches a log line or a map lookup.
const maxKidLen = 200

// algSpec is one allowed signature algorithm. Everything outside this table is
// refused, "none" and the HMAC family included: with a JWKS that anyone can
// fetch, an HS256 token would be forgeable by anyone who read it, which is the
// classic algorithm confusion attack on a JWT verifier.
type algSpec struct {
	kty     string
	curve   elliptic.Curve // EC only
	hash    crypto.Hash    // RSA only, for the PKCS #1 digest prefix
	newHash func() hash.Hash
}

var supportedAlgs = map[string]algSpec{
	"RS256": {kty: "RSA", hash: crypto.SHA256, newHash: sha256.New},
	"RS384": {kty: "RSA", hash: crypto.SHA384, newHash: sha512.New384},
	"RS512": {kty: "RSA", hash: crypto.SHA512, newHash: sha512.New},
	"ES256": {kty: "EC", curve: elliptic.P256(), newHash: sha256.New},
	"ES384": {kty: "EC", curve: elliptic.P384(), newHash: sha512.New384},
}

// idClaims are the claims of an ID token this package reads by name. aud is
// kept raw because it is a string or an array of them, and the group claim is
// read separately since its name is configurable.
type idClaims struct {
	Issuer   string          `json:"iss"`
	Subject  string          `json:"sub"`
	Audience json.RawMessage `json:"aud"`
	AZP      string          `json:"azp"`
	Exp      *float64        `json:"exp"`
	Iat      *float64        `json:"iat"`
	Nonce    string          `json:"nonce"`
	Username string          `json:"preferred_username"`
	Name     string          `json:"name"`
	Email    string          `json:"email"`
}

// verifyIDToken checks the signature first and the claims second, and returns
// who the token is for. No error it produces carries the token.
func (p *Provider) verifyIDToken(ctx context.Context, raw, nonce string) (Identity, error) {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return Identity{}, loginProblem(errors.New("id token is not a JWS in compact serialization"))
	}

	headerJSON, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return Identity{}, loginProblem(errors.New("id token header is not base64url"))
	}
	var header struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if json.Unmarshal(headerJSON, &header) != nil {
		return Identity{}, loginProblem(errors.New("id token header is not JSON"))
	}
	// The algorithm is decided by this table, never by the token: the header
	// only selects from what is already allowed here.
	spec, ok := supportedAlgs[header.Alg]
	if !ok {
		return Identity{}, loginProblem(fmt.Errorf("id token alg %q is not allowed", shorten(header.Alg, 20)))
	}
	if header.Kid == "" || len(header.Kid) > maxKidLen {
		return Identity{}, loginProblem(errors.New("id token has no usable kid"))
	}

	key, err := p.keys.key(ctx, header.Kid)
	if err != nil {
		return Identity{}, err
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return Identity{}, loginProblem(errors.New("id token signature is not base64url"))
	}
	h := spec.newHash()
	// The signing input is the two encoded parts exactly as they arrived, not
	// a re-encoding of what was parsed out of them.
	h.Write([]byte(parts[0] + "." + parts[1]))
	if err := verifySignature(spec, key, h.Sum(nil), signature); err != nil {
		return Identity{}, loginProblem(fmt.Errorf("id token signature: %w", err))
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return Identity{}, loginProblem(errors.New("id token payload is not base64url"))
	}
	var claims idClaims
	if json.Unmarshal(payload, &claims) != nil {
		return Identity{}, loginProblem(errors.New("id token payload is not JSON"))
	}
	groups, err := p.groupsOf(payload)
	if err != nil {
		return Identity{}, loginProblem(fmt.Errorf("id token group claim: %w", err))
	}
	return p.checkClaims(claims, groups, raw, nonce)
}

// verifySignature checks the signature against the key the kid named. The
// algorithm has to match the key: an ES256 header over an RSA key is an
// attempt to have this verify something other than what was signed.
func verifySignature(spec algSpec, key crypto.PublicKey, digest, signature []byte) error {
	switch k := key.(type) {
	case *rsa.PublicKey:
		if spec.kty != "RSA" {
			return errors.New("alg is not for an RSA key")
		}
		return rsa.VerifyPKCS1v15(k, spec.hash, digest, signature)
	case *ecdsa.PublicKey:
		if spec.kty != "EC" {
			return errors.New("alg is not for an EC key")
		}
		if k.Curve != spec.curve {
			return fmt.Errorf("alg is not for curve %s", k.Curve.Params().Name)
		}
		// JWS keeps r and s fixed width and concatenated, RFC 7518 section 3.4,
		// rather than in the ASN.1 form ecdsa signs into elsewhere.
		size := (k.Curve.Params().BitSize + 7) / 8
		if len(signature) != 2*size {
			return fmt.Errorf("signature is %d bytes, want %d for %s", len(signature), 2*size, k.Curve.Params().Name)
		}
		r := new(big.Int).SetBytes(signature[:size])
		s := new(big.Int).SetBytes(signature[size:])
		if !ecdsa.Verify(k, digest, r, s) {
			return errors.New("does not verify")
		}
		return nil
	default:
		return errors.New("unsupported key type")
	}
}

// checkClaims validates the claims of a token whose signature already holds.
// Every one of them is a separate way for a valid token meant for something
// else to be accepted here, so none of them is optional.
func (p *Provider) checkClaims(c idClaims, groups []string, raw, nonce string) (Identity, error) {
	if c.Issuer != p.issuer {
		return Identity{}, loginProblem(fmt.Errorf("id token issuer %q, want %q", shorten(c.Issuer, 200), p.issuer))
	}
	if c.Subject == "" {
		return Identity{}, loginProblem(errors.New("id token has no sub"))
	}

	audience, err := stringOrList(c.Audience)
	if err != nil {
		return Identity{}, loginProblem(fmt.Errorf("id token aud: %w", err))
	}
	if !slices.Contains(audience, p.clientID) {
		return Identity{}, loginProblem(errors.New("id token is for another audience"))
	}
	// azp names the client a token was issued to when it has several
	// audiences. It still has to be this one.
	if c.AZP != "" && c.AZP != p.clientID {
		return Identity{}, loginProblem(errors.New("id token was issued to another client"))
	}

	if c.Exp == nil || c.Iat == nil {
		return Identity{}, loginProblem(errors.New("id token has no exp or iat"))
	}
	// Fractional seconds are dropped, which is nothing beside the skew
	// allowance.
	expiresAt := time.Unix(int64(*c.Exp), 0).UTC()
	issuedAt := time.Unix(int64(*c.Iat), 0).UTC()
	now := p.now()
	if !now.Before(expiresAt.Add(ClockSkew)) {
		return Identity{}, loginProblem(fmt.Errorf("id token expired at %s", expiresAt.Format(time.RFC3339)))
	}
	if issuedAt.After(now.Add(ClockSkew)) {
		return Identity{}, loginProblem(fmt.Errorf("id token was issued in the future, at %s", issuedAt.Format(time.RFC3339)))
	}

	// The nonce ties the token to the login this browser started, which is what
	// stops a token obtained somewhere else from being replayed into this
	// callback.
	if c.Nonce != nonce {
		return Identity{}, loginProblem(errors.New("id token nonce does not match the login"))
	}

	return Identity{
		Subject:   c.Subject,
		Username:  c.Username,
		Name:      c.Name,
		Email:     c.Email,
		Groups:    groups,
		IDToken:   raw,
		ExpiresAt: expiresAt,
	}, nil
}

// groupsOf reads the configured group claim out of the verified payload. The
// claim may be missing, null, a single string or a list of them, and a dotted
// claim name walks into nested objects on the way.
//
// A leading slash is dropped, because a provider that models groups as a tree
// sends them as paths and an application comparing against "admins" should not
// have to know that.
func (p *Provider) groupsOf(payload []byte) ([]string, error) {
	raw, err := claimAt(payload, p.groupsPath)
	if err != nil {
		return nil, err
	}
	values, err := stringOrList(raw)
	if err != nil {
		return nil, err
	}

	groups := make([]string, 0, len(values))
	for _, group := range values {
		if group = strings.TrimPrefix(group, "/"); group != "" {
			groups = append(groups, group)
		}
	}
	if len(groups) == 0 {
		return nil, nil
	}
	return groups, nil
}

// claimAt walks a dotted claim name into the payload. A segment that is
// missing is not an error: a provider that sends no groups is a deployment
// without groups, not a broken token.
func claimAt(payload []byte, path []string) (json.RawMessage, error) {
	current := json.RawMessage(payload)
	for _, segment := range path {
		var object map[string]json.RawMessage
		if json.Unmarshal(current, &object) != nil {
			return nil, errors.New("a claim on the way to it is not a JSON object")
		}
		next, ok := object[segment]
		if !ok {
			return nil, nil
		}
		current = next
	}
	return current, nil
}

// stringOrList reads a claim that is a single string or an array of them.
func stringOrList(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var one string
	if json.Unmarshal(raw, &one) == nil {
		return []string{one}, nil
	}
	var many []string
	if json.Unmarshal(raw, &many) == nil {
		return many, nil
	}
	return nil, errors.New("neither a string nor an array of strings")
}
