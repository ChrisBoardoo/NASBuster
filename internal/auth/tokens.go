// SPDX-License-Identifier: AGPL-3.0-or-later

package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"strings"
)

// NewToken returns 32 random bytes, base64url-encoded (session and CSRF tokens).
func NewToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand never fails on supported platforms
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// HashToken returns the SHA-256 hex digest stored in place of a session token:
// a leaked database does not leak usable sessions.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// EqualTokens compares two tokens in constant time.
func EqualTokens(a, b string) bool {
	return len(a) > 0 && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// setupAlphabet has no look-alike characters (no 0/O, 1/I/L).
const setupAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"

// NewSetupCode returns a code like "K7QF-92MD" (8 chars, ~39 bits). It only
// needs to resist online guessing, which the rate limiter bounds.
func NewSetupCode() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	out := make([]byte, 0, 9)
	for i, v := range b {
		if i == 4 {
			out = append(out, '-')
		}
		// 256 % 31 leaves a tiny bias; irrelevant for a rate-limited code.
		out = append(out, setupAlphabet[int(v)%len(setupAlphabet)])
	}
	return string(out)
}

// NormalizeSetupCode upper-cases and strips separators typed by the user.
func NormalizeSetupCode(s string) string {
	s = strings.ToUpper(s)
	return strings.Map(func(r rune) rune {
		if r == '-' || r == ' ' {
			return -1
		}
		return r
	}, s)
}

// SetupCodeMatches compares a user input with the expected code.
func SetupCodeMatches(input, expected string) bool {
	return expected != "" && EqualTokens(NormalizeSetupCode(input), NormalizeSetupCode(expected))
}
