package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

// Password policy.
const (
	PasswordMinLength = 10
	PasswordMaxLength = 128
)

// argon2id parameters (OWASP-recommended class): 64 MiB, 3 passes, 2 lanes.
const (
	argonMemory  = 64 * 1024
	argonTime    = 3
	argonThreads = 2
	argonKeyLen  = 32
	argonSaltLen = 16
)

// ErrPasswordPolicy is returned by ValidatePassword.
var ErrPasswordPolicy = errors.New("password does not meet the policy")

// ValidatePassword enforces length bounds (no composition rules: length is
// what matters, and users may use passphrases).
func ValidatePassword(pw string) error {
	n := utf8.RuneCountInString(pw)
	switch {
	case n < PasswordMinLength:
		return fmt.Errorf("%w: use at least %d characters", ErrPasswordPolicy, PasswordMinLength)
	case n > PasswordMaxLength:
		return fmt.Errorf("%w: use at most %d characters", ErrPasswordPolicy, PasswordMaxLength)
	case strings.TrimSpace(pw) == "":
		return fmt.Errorf("%w: password cannot be blank", ErrPasswordPolicy)
	}
	return nil
}

// HashPassword returns a PHC-formatted argon2id hash with a random salt:
// $argon2id$v=19$m=65536,t=3,p=2$<salt>$<hash>
func HashPassword(pw string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(pw), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	enc := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, argonMemory, argonTime, argonThreads, enc.EncodeToString(salt), enc.EncodeToString(key)), nil
}

// VerifyPassword reports whether pw matches the PHC hash (constant-time).
func VerifyPassword(hash, pw string) (bool, error) {
	parts := strings.Split(hash, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, errors.New("unsupported password hash")
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false, errors.New("unsupported argon2 version")
	}
	var mem, t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &mem, &t, &p); err != nil {
		return false, errors.New("malformed argon2 parameters")
	}
	enc := base64.RawStdEncoding
	salt, err := enc.DecodeString(parts[4])
	if err != nil {
		return false, err
	}
	want, err := enc.DecodeString(parts[5])
	if err != nil {
		return false, err
	}
	got := argon2.IDKey([]byte(pw), salt, t, mem, p, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}
