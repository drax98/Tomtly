package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
)

func random(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("no system randomness: " + err.Error()) // continuing would mean predictable secrets
	}
	return b
}

// NewSecret returns a 256-bit secret to hand out and the hash to store. Only the hash is ever kept.
func NewSecret(prefix string) (secret, hash string) {
	secret = prefix + base64.RawURLEncoding.EncodeToString(random(32))
	return secret, HashSecret(secret)
}

// HashSecret is how a presented session or token is looked up. The secrets are random, so a fast hash is enough.
func HashSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// NewID returns a random identifier for a user or token.
func NewID() string { return hex.EncodeToString(random(16)) }
