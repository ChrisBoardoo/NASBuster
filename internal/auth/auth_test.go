// SPDX-License-Identifier: AGPL-3.0-or-later

package auth

import (
	"strings"
	"testing"
	"time"
)

func TestPasswordRoundTrip(t *testing.T) {
	hash, err := HashPassword("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hash, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Fatalf("hash format = %s", hash)
	}
	if ok, err := VerifyPassword("correct horse battery", hash); !ok || err != nil {
		t.Fatalf("verify good = %v, %v", ok, err)
	}
	if ok, _ := VerifyPassword("wrong", hash); ok {
		t.Fatal("wrong password accepted")
	}
	other, _ := HashPassword("correct horse battery")
	if other == hash {
		t.Fatal("salt not random")
	}
}

func TestVerifyRejectsTamperedHash(t *testing.T) {
	for _, h := range []string{"", "plain", "$argon2i$v=19$m=1,t=1,p=1$c2FsdA$aGFzaA", "$argon2id$v=19$m=999999999,t=1,p=1$c2FsdA$aGFzaA"} {
		if ok, err := VerifyPassword("x", h); ok || err == nil {
			t.Errorf("hash %q: ok=%v err=%v", h, ok, err)
		}
	}
}

func TestSetupCode(t *testing.T) {
	code := NewSetupCode()
	if len(code) != 9 || code[4] != '-' || strings.ContainsAny(code, "01OIL") {
		t.Fatalf("code = %q", code)
	}
	lower := strings.ToLower(strings.ReplaceAll(code, "-", " "))
	if !SetupCodeMatches(lower, code) {
		t.Fatalf("normalized input %q should match %q", lower, code)
	}
	if SetupCodeMatches("", code) || SetupCodeMatches(code, "") || SetupCodeMatches("AAAA-AAAA", code) && code != "AAAA-AAAA" {
		t.Fatal("bad setup code accepted")
	}
}

func TestTokens(t *testing.T) {
	a, b := NewToken(), NewToken()
	if a == b || len(a) != 43 {
		t.Fatalf("tokens %q %q", a, b)
	}
	if HashToken(a) == a || len(HashToken(a)) != 64 {
		t.Fatal("HashToken")
	}
	if !EqualTokens(a, a) || EqualTokens(a, b) || EqualTokens("", "") {
		t.Fatal("EqualTokens")
	}
}

func TestLimiter(t *testing.T) {
	now := time.Unix(1_790_000_000, 0)
	l := &Limiter{FreeAttempts: 2, BaseLock: 30 * time.Second, MaxLock: 2 * time.Minute, Now: func() time.Time { return now }}
	for i := 0; i < 2; i++ {
		if ok, _ := l.Allow("ip"); !ok {
			t.Fatalf("attempt %d blocked", i)
		}
		l.Fail("ip")
	}
	if ok, _ := l.Allow("ip"); !ok {
		t.Fatal("still within free attempts")
	}
	l.Fail("ip") // 3rd failure: 30 s lock
	if ok, wait := l.Allow("ip"); ok || wait != 30*time.Second {
		t.Fatalf("Allow = %v, %v", ok, wait)
	}
	now = now.Add(31 * time.Second)
	l.Fail("ip") // 4th: 60 s
	l.Fail("ip") // 5th: 120 s (cap)
	l.Fail("ip") // 6th: still capped
	if _, wait := l.Allow("ip"); wait != 2*time.Minute {
		t.Fatalf("wait = %v, want cap", wait)
	}
	if ok, _ := l.Allow("other"); !ok {
		t.Fatal("other keys unaffected")
	}
	l.Success("ip")
	if ok, _ := l.Allow("ip"); !ok {
		t.Fatal("success resets")
	}
}
