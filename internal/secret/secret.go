// Package secret generates and hashes credentials. Plaintext secrets are
// shown once at creation; only SHA-256 digests (for high-entropy tokens) or
// argon2id hashes (for passwords) are stored.
package secret

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	APIKeyPrefix     = "sk-sb-"
	AgentTokenPrefix = "sba_"
	OAuthTokenPrefix = "sbo_"
	RefreshPrefix    = "sbr_"
)

// alphabet is base62: URL-safe and shell-safe.
const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

// Random returns n random base62 characters.
func Random(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("secret: crypto/rand unavailable: " + err.Error())
	}
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b)
}

// RandomURLSafe returns a base64url string with n random bytes of entropy.
func RandomURLSafe(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("secret: crypto/rand unavailable: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// Hash returns the hex SHA-256 digest of s. Suitable for high-entropy tokens.
func Hash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// NewToken returns a fresh token with the given prefix plus its hash and a
// display prefix.
func NewToken(prefix string, length int) (plain, hash, display string) {
	plain = prefix + Random(length)
	return plain, Hash(plain), Display(plain)
}

// NewAPIKey mints an OpenAI-style API key.
func NewAPIKey() (plain, hash, display string) {
	return NewToken(APIKeyPrefix, 40)
}

// NewAgentToken mints a static MCP bearer token.
func NewAgentToken() (plain, hash, display string) {
	return NewToken(AgentTokenPrefix, 40)
}

// Display renders a secret for UIs: prefix, a few leading chars, the tail.
func Display(plain string) string {
	if len(plain) <= 14 {
		return plain
	}
	return plain[:10] + "…" + plain[len(plain)-4:]
}

// Equal compares two strings in constant time.
func Equal(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// Argon2id parameters: tuned for interactive logins on a laptop.
const (
	argonTime    = 2
	argonMemory  = 64 * 1024
	argonThreads = 2
	argonKeyLen  = 32
)

// HashPassword returns a PHC-formatted argon2id hash.
func HashPassword(password string) string {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		panic("secret: crypto/rand unavailable: " + err.Error())
	}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s",
		argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key))
}

// VerifyPassword checks password against a hash produced by HashPassword.
func VerifyPassword(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var mem, t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &mem, &t, &p); err != nil {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, t, mem, p, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// ErrInvalid is returned by verifiers when a credential does not match.
var ErrInvalid = errors.New("invalid credential")

// PKCEVerify checks an OAuth PKCE verifier against an S256 challenge.
func PKCEVerify(verifier, challenge, method string) bool {
	switch method {
	case "S256", "":
		sum := sha256.Sum256([]byte(verifier))
		return Equal(base64.RawURLEncoding.EncodeToString(sum[:]), challenge)
	case "plain":
		return Equal(verifier, challenge)
	}
	return false
}
