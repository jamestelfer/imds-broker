package containercreds

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// tokenBytes is the entropy of the authorisation token.
const tokenBytes = 32

// newToken returns a hex-encoded random token. Hex keeps the value printable
// ASCII with no whitespace, so it survives being written to a file and sent
// verbatim as an HTTP header.
func newToken() ([]byte, error) {
	raw := make([]byte, tokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return nil, fmt.Errorf("containercreds: generate token: %w", err)
	}
	return []byte(hex.EncodeToString(raw)), nil
}
