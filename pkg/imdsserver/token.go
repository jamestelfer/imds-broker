package imdsserver

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
	"time"
)

// token handles IMDSv2 token generation and validation.
// Token format: base64url(expiry).base64url(hmac-sha256)
// where expiry is time.Time.MarshalText() (RFC 3339 UTC).
type token struct {
	secret []byte
}

func newToken() *token {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		panic("imdsserver: failed to generate token secret: " + err.Error())
	}
	return &token{secret: secret}
}

func (t *token) encode(ttl time.Duration) []byte {
	expiry := time.Now().UTC().Add(ttl)
	expiryBytes, err := expiry.MarshalText()
	if err != nil {
		panic("imdsserver: failed to marshal token expiry: " + err.Error())
	}
	encodedExpiry := base64.URLEncoding.EncodeToString(expiryBytes)

	mac := hmac.New(sha256.New, t.secret)
	mac.Write(expiryBytes)
	encodedMAC := base64.URLEncoding.EncodeToString(mac.Sum(nil))

	result := make([]byte, 0, len(encodedExpiry)+1+len(encodedMAC))
	result = append(result, encodedExpiry...)
	result = append(result, '.')
	result = append(result, encodedMAC...)
	return result
}

func (t *token) validate(tok string) error {
	parts := strings.SplitN(tok, ".", 2)
	if len(parts) != 2 {
		return errors.New("invalid IMDSv2 token")
	}

	expiryBytes, err := base64.URLEncoding.DecodeString(parts[0])
	if err != nil {
		return errors.New("invalid IMDSv2 token")
	}

	macBytes, err := base64.URLEncoding.DecodeString(parts[1])
	if err != nil {
		return errors.New("invalid IMDSv2 token")
	}

	mac := hmac.New(sha256.New, t.secret)
	mac.Write(expiryBytes)
	if !hmac.Equal(mac.Sum(nil), macBytes) {
		return errors.New("invalid IMDSv2 token")
	}

	var expiry time.Time
	if err := expiry.UnmarshalText(expiryBytes); err != nil {
		return errors.New("invalid IMDSv2 token")
	}

	if time.Now().UTC().After(expiry) {
		return errors.New("IMDSv2 token has expired")
	}

	return nil
}
