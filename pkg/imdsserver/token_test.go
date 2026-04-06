package imdsserver

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTokenRoundTrip(t *testing.T) {
	tok := newToken()
	encoded := tok.encode(60 * time.Second)
	require.NoError(t, tok.validate(string(encoded)))
}

func TestTokenValidate_InvalidFormat(t *testing.T) {
	tok := newToken()
	assert.Error(t, tok.validate("notavalidtoken"))
}

func TestTokenValidate_WrongSecret(t *testing.T) {
	tok1 := newToken()
	tok2 := newToken()
	encoded := tok1.encode(60 * time.Second)
	assert.Error(t, tok2.validate(string(encoded)))
}

func TestTokenValidate_Expired(t *testing.T) {
	tok := newToken()
	// Encode with negative duration to create an already-expired token.
	encoded := tok.encode(-1 * time.Second)
	assert.Error(t, tok.validate(string(encoded)))
}
