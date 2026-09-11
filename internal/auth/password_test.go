package auth

import (
	"strings"
	"testing"
	"time"
)

func TestPasswordHashAndVerify(t *testing.T) {
	h, err := HashPassword("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$m=65536,t=3,p=2$") {
		t.Fatalf("hash format: %s", h)
	}
	if ok, err := VerifyPassword(h, "correct horse battery"); err != nil || !ok {
		t.Fatalf("verify: %v %v", ok, err)
	}
	if ok, _ := VerifyPassword(h, "correct horse battery!"); ok {
		t.Fatal("wrong password accepted")
	}
	h2, _ := HashPassword("correct horse battery")
	if h2 == h {
		t.Fatal("salt not random")
	}
	if _, err := VerifyPassword("$bcrypt$nope", "x"); err == nil {
		t.Fatal("foreign hash accepted")
	}
	for _, bad := range []string{"short", strings.Repeat("x", 129), "          "} {
		if err := ValidatePassword(bad); err == nil {
			t.Fatalf("%q passed policy", bad)
		}
	}
	if err := ValidatePassword("a decent passphrase"); err != nil {
		t.Fatal(err)
	}
}

func TestLimiter(t *testing.T) {
	now := time.Unix(1000, 0)
	l := NewLimiter(3, time.Minute)
	l.now = func() time.Time { return now }
	for i := 0; i < 3; i++ {
		if l.Blocked("k") {
			t.Fatalf("blocked after %d failures", i)
		}
		l.Fail("k")
	}
	if !l.Blocked("k") {
		t.Fatal("not blocked after max failures")
	}
	if l.Blocked("other") {
		t.Fatal("unrelated key blocked")
	}
	now = now.Add(61 * time.Second)
	if l.Blocked("k") {
		t.Fatal("still blocked after the window")
	}
	l.Fail("k")
	l.Reset("k")
	if l.Blocked("k") || len(l.hits) != 0 {
		t.Fatal("reset failed")
	}
}
