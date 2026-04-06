package imdsserver

import (
	"testing"
	"time"
)

func TestTokenRoundTrip(t *testing.T) {
	tok := newToken()
	encoded := tok.encode(60 * time.Second)
	if err := tok.validate(string(encoded)); err != nil {
		t.Fatalf("expected valid token, got error: %v", err)
	}
}

func TestTokenValidate_InvalidFormat(t *testing.T) {
	tok := newToken()
	if err := tok.validate("notavalidtoken"); err == nil {
		t.Fatal("expected error for invalid token format")
	}
}

func TestTokenValidate_WrongSecret(t *testing.T) {
	tok1 := newToken()
	tok2 := newToken()
	encoded := tok1.encode(60 * time.Second)
	if err := tok2.validate(string(encoded)); err == nil {
		t.Fatal("expected error for token with wrong secret")
	}
}

func TestTokenValidate_Expired(t *testing.T) {
	tok := newToken()
	// Encode with negative duration to create an already-expired token
	encoded := tok.encode(-1 * time.Second)
	if err := tok.validate(string(encoded)); err == nil {
		t.Fatal("expected error for expired token")
	}
}
