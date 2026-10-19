// Package auth implements password hashing, server-side sessions and
// role-based access control.
package auth

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Iterations follows current OWASP guidance for PBKDF2-HMAC-SHA256.
var Iterations = 600_000

const keyLen = 32

var ErrWeakPassword = errors.New("password must be at least 12 characters")

// CheckPolicy enforces the minimum password policy.
func CheckPolicy(pw string) error {
	if utf8.RuneCountInString(pw) < 12 || len(pw) > 256 {
		return ErrWeakPassword
	}
	return nil
}

// HashPassword returns "pbkdf2_sha256$<iter>$<salt>$<hash>".
func HashPassword(pw string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	dk, err := pbkdf2.Key(sha256.New, pw, salt, Iterations, keyLen)
	if err != nil {
		return "", err
	}
	enc := base64.RawStdEncoding
	return fmt.Sprintf("pbkdf2_sha256$%d$%s$%s", Iterations, enc.EncodeToString(salt), enc.EncodeToString(dk)), nil
}

// VerifyPassword compares pw to an encoded hash in constant time.
func VerifyPassword(encoded, pw string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2_sha256" {
		return false
	}
	iter, err := strconv.Atoi(parts[1])
	if err != nil || iter < 1 {
		return false
	}
	enc := base64.RawStdEncoding
	salt, err1 := enc.DecodeString(parts[2])
	want, err2 := enc.DecodeString(parts[3])
	if err1 != nil || err2 != nil {
		return false
	}
	got, err := pbkdf2.Key(sha256.New, pw, salt, iter, len(want))
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(got, want) == 1
}

// dummyHash is verified against when the user does not exist so response
// time does not reveal which emails are registered.
var dummyHash, _ = HashPassword("timing-equaliser-not-a-real-password")
