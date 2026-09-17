// Package auth owns accounts, passwords, sessions, and API tokens.
//
// Everything here lives only in the index: losing index.db loses the ability to
// log in, but not a byte of user data. Storage roots are named for the username
// so re-creating an account reattaches it to its files.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strings"
	"sync"

	"golang.org/x/crypto/argon2"
)

// Argon2id parameters: OWASP's low-memory recommendation. This is paid on every
// login by a NAS or a Raspberry Pi, and 19 MiB is where the trade-off stops
// favouring an attacker with better hardware than the server.
const (
	argonTime    = 2
	argonMemory  = 19 * 1024 // KiB
	argonThreads = 1
	argonKeyLen  = 32
	argonSaltLen = 16
)

// HashPassword returns a PHC-format Argon2id hash. The parameters are encoded
// in the string, so raising them later leaves existing hashes verifiable.
func HashPassword(password string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generating password salt: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	b64 := base64.RawStdEncoding.EncodeToString
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads, b64(salt), b64(key)), nil
}

// VerifyPassword reports whether password produced encoded.
//
// A malformed or empty hash is a plain false rather than an error: to anyone
// calling this it is indistinguishable from a wrong password, and it must stay
// that way so a damaged row cannot be probed for.
func VerifyPassword(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false
	}
	var memory, iterations uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &iterations, &threads); err != nil {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(want) == 0 {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, iterations, memory, threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// dummyHash is verified against when the named account does not exist, so a
// login for an unknown user costs what a wrong password costs. Without it the
// response time answers "does this account exist?" whatever the body says.
var dummyHash = sync.OnceValue(func() string {
	h, _ := HashPassword("no account has this password")
	return h
})

func equaliseTiming(password string) { VerifyPassword(dummyHash(), password) }
