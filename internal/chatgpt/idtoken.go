package chatgpt

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"slices"
	"strings"
	"time"
)

// discovery is the part of OpenAI's OpenID configuration werkz uses.
type discovery struct {
	Issuer     string `json:"issuer"`
	JWKS       string `json:"jwks_uri"`
	Revocation string `json:"revocation_endpoint"`
}

func discover(ctx context.Context) (*discovery, error) {
	var d discovery
	if err := getJSON(ctx, AuthBase+"/.well-known/openid-configuration", &d); err != nil {
		return nil, err
	}
	return &d, nil
}

func getJSON(ctx context.Context, u string, v any) error {
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("GET %s: HTTP %d", u, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(v)
}

// idClaims are the ID token claims werkz checks or keeps.
type idClaims struct {
	Issuer   string          `json:"iss"`
	Subject  string          `json:"sub"`
	Audience json.RawMessage `json:"aud"`
	Expiry   float64         `json:"exp"`
	Nonce    string          `json:"nonce"`
	Email    string          `json:"email"`
}

func (c *idClaims) audiences() []string {
	var one string
	if json.Unmarshal(c.Audience, &one) == nil {
		return []string{one}
	}
	var many []string
	_ = json.Unmarshal(c.Audience, &many)
	return many
}

// verifyIDToken checks the ID token's signature against OpenAI's published keys, and its
// issuer, audience (the issued client ID), expiry and nonce.
func verifyIDToken(ctx context.Context, token, clientID, nonce string) (*idClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, errors.New("the ChatGPT sign-in returned no valid ID token")
	}
	var hdr struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if err := decodePart(parts[0], &hdr); err != nil {
		return nil, err
	}
	var claims idClaims
	if err := decodePart(parts[1], &claims); err != nil {
		return nil, err
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, errors.New("malformed ID token signature")
	}

	d, err := discover(ctx)
	if err != nil {
		return nil, fmt.Errorf("can't check the ChatGPT sign-in: %w", err)
	}
	var set struct {
		Keys []jwk `json:"keys"`
	}
	if err := getJSON(ctx, d.JWKS, &set); err != nil {
		return nil, fmt.Errorf("can't check the ChatGPT sign-in: %w", err)
	}
	var key *jwk
	for i := range set.Keys {
		if set.Keys[i].Kid == hdr.Kid {
			key = &set.Keys[i]
		}
	}
	if key == nil {
		return nil, errors.New("the ID token was signed with an unknown key")
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := key.verify(hdr.Alg, digest[:], sig); err != nil {
		return nil, err
	}

	switch {
	case d.Issuer != "" && claims.Issuer != d.Issuer:
		return nil, errors.New("the ID token came from an unexpected issuer")
	case !slices.Contains(claims.audiences(), clientID):
		return nil, errors.New("the ID token wasn't issued to werkz")
	case time.Now().After(time.Unix(int64(claims.Expiry), 0).Add(time.Minute)):
		return nil, errors.New("the ID token has expired; sign in again")
	case claims.Nonce != nonce:
		return nil, errors.New("the ID token doesn't belong to this sign-in (nonce mismatch)")
	case claims.Subject == "":
		return nil, errors.New("the ID token has no account identity")
	}
	return &claims, nil
}

func decodePart(s string, v any) error {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return errors.New("malformed ID token")
	}
	if err := json.Unmarshal(b, v); err != nil {
		return errors.New("malformed ID token")
	}
	return nil
}

// jwk is one public key from a JSON Web Key Set (RSA or P-256).
type jwk struct {
	Kid string `json:"kid"`
	Kty string `json:"kty"`
	N   string `json:"n"`
	E   string `json:"e"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

func (k *jwk) verify(alg string, digest, sig []byte) error {
	bad := errors.New("the ID token signature is invalid")
	switch {
	case alg == "RS256" && k.Kty == "RSA":
		n, e := b64int(k.N), b64int(k.E)
		if n == nil || e == nil {
			return bad
		}
		pub := &rsa.PublicKey{N: n, E: int(e.Int64())}
		if rsa.VerifyPKCS1v15(pub, crypto.SHA256, digest, sig) != nil {
			return bad
		}
		return nil
	case alg == "ES256" && k.Kty == "EC" && k.Crv == "P-256":
		x, y := b64int(k.X), b64int(k.Y)
		if x == nil || y == nil || len(sig) != 64 {
			return bad
		}
		pub := &ecdsa.PublicKey{Curve: elliptic.P256(), X: x, Y: y}
		r, s := new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])
		if !ecdsa.Verify(pub, digest, r, s) {
			return bad
		}
		return nil
	}
	return fmt.Errorf("unsupported ID token signature (%s)", alg)
}

func b64int(s string) *big.Int {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil || len(b) == 0 {
		return nil
	}
	return new(big.Int).SetBytes(b)
}
