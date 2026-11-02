// SPDX-License-Identifier: AGPL-3.0-or-later

// Package auth provides password hashing, session tokens, the first-run setup
// code and login rate limiting.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"runtime/debug"
	"strings"
	"sync"

	"golang.org/x/crypto/argon2"
)

// Argon2id parameters: OWASP's "m=19 MiB, t=2, p=1" profile. It keeps a login
// under ~19 MiB of RAM on a Pi; the parameters are stored in each hash, so they
// can be raised later without breaking existing accounts. ⚠️ To time on the Pi.
const (
	argonMemoryKiB = 19 * 1024
	argonTime      = 2
	argonThreads   = 1
	argonKeyLen    = 32
	argonSaltLen   = 16
)

// hashSlots caps concurrent hashes so a burst of logins cannot exhaust RAM.
var hashSlots = make(chan struct{}, 2)

// ErrMalformedHash means a stored hash cannot be parsed.
var ErrMalformedHash = errors.New("malformed password hash")

// HashPassword returns an encoded argon2id hash (PHC string format).
func HashPassword(password string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := derive([]byte(password), salt, argonTime, argonMemoryKiB, argonThreads, argonKeyLen)
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemoryKiB, argonTime, argonThreads, b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

// VerifyPassword checks password against an encoded hash in constant time.
func VerifyPassword(password, encoded string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, ErrMalformedHash
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false, ErrMalformedHash
	}
	var memory, time uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &time, &threads); err != nil {
		return false, ErrMalformedHash
	}
	// Refuse absurd parameters from a tampered database.
	if memory == 0 || memory > 256*1024 || time == 0 || time > 10 || threads == 0 {
		return false, ErrMalformedHash
	}
	b64 := base64.RawStdEncoding
	salt, err := b64.DecodeString(parts[4])
	if err != nil {
		return false, ErrMalformedHash
	}
	want, err := b64.DecodeString(parts[5])
	if err != nil || len(want) == 0 {
		return false, ErrMalformedHash
	}
	got := derive([]byte(password), salt, time, memory, threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

func derive(password, salt []byte, time, memory uint32, threads uint8, keyLen uint32) []byte {
	hashSlots <- struct{}{}
	defer func() { <-hashSlots }()
	key := argon2.IDKey(password, salt, time, memory, threads, keyLen)
	// Argon2 just allocated ~19 MiB that is garbage now. Logins are rare, so
	// give it back to the OS immediately instead of keeping it in the Go heap:
	// it is the single biggest contributor to the daemon's resident memory.
	debug.FreeOSMemory()
	return key
}

// dummyHash is verified when a username does not exist, so that response
// times do not reveal which accounts exist. Computed on first use, not at
// startup, to keep the idle footprint small.
var dummyHash = sync.OnceValue(func() string {
	h, _ := HashPassword("cwnsb-dummy-password")
	return h
})

// VerifyDummy burns the same time as a real verification.
func VerifyDummy(password string) {
	_, _ = VerifyPassword(password, dummyHash())
}
