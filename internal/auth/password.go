package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

const (
	MinPassword = 12
	MaxPassword = 256
	keyLen      = 32
	saltLen     = 16
)

// Params are the argon2id cost for new hashes. Each hash records its own, so raising them never breaks old ones.
var Params = struct{ Memory, Time uint32 }{Memory: 64 * 1024, Time: 2}

// Hashing is memory hungry; this keeps a burst of logins from exhausting the container.
var slots = make(chan struct{}, 4)

// ErrBusy means too many passwords are being hashed to wait any longer. Waiting without a limit would let a burst of logins
// hold requests open for as long as it lasts.
var ErrBusy = errors.New("too many passwords are being checked right now")

// SlotWait is how long a hash waits its turn. Tests shorten it.
var SlotWait = 3 * time.Second

func derive(password string, salt []byte, memory, cost uint32) ([]byte, error) {
	wait := time.NewTimer(SlotWait)
	defer wait.Stop()
	select {
	case slots <- struct{}{}:
	case <-wait.C:
		return nil, ErrBusy
	}
	defer func() { <-slots }()
	return argon2.IDKey([]byte(password), salt, cost, memory, 1, keyLen), nil
}

// ValidatePassword enforces the length rules. It judges length only: no composition rules, so passphrases work.
func ValidatePassword(password string) error {
	if utf8.RuneCountInString(password) < MinPassword {
		return fmt.Errorf("the password needs at least %d characters", MinPassword)
	}
	if len(password) > MaxPassword {
		return fmt.Errorf("the password can be at most %d bytes", MaxPassword)
	}
	return nil
}

// Hash returns a PHC string: $argon2id$v=19$m=..,t=..,p=..$salt$key.
func Hash(password string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key, err := derive(password, salt, Params.Memory, Params.Time)
	if err != nil {
		return "", err
	}
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=1$%s$%s", argon2.Version, Params.Memory, Params.Time, b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

// Verify compares in constant time. A malformed or oversized-cost hash never matches. The error is ErrBusy, and says nothing
// about the password.
func Verify(encoded, password string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" || parts[2] != fmt.Sprintf("v=%d", argon2.Version) {
		return false, nil
	}
	var memory, cost, threads uint32
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &cost, &threads); err != nil || threads != 1 || memory < 8 || memory > 1<<20 || cost < 1 || cost > 20 {
		return false, nil
	}
	salt, err1 := base64.RawStdEncoding.DecodeString(parts[4])
	want, err2 := base64.RawStdEncoding.DecodeString(parts[5])
	if err1 != nil || err2 != nil || len(want) != keyLen {
		return false, nil
	}
	got, err := derive(password, salt, memory, cost)
	if err != nil {
		return false, err
	}
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

var (
	dummyMu sync.Mutex
	dummy   string
)

// SpendTime does the work of a verification against nobody, so an unknown username costs the same as a wrong password.
func SpendTime(password string) error {
	dummyMu.Lock()
	var err error
	if dummy == "" {
		dummy, err = Hash("not a real password")
	}
	hash := dummy
	dummyMu.Unlock()
	if err != nil {
		return err
	}
	_, err = Verify(hash, password)
	return err
}

// GeneratePassword returns a random password that passes ValidatePassword.
func GeneratePassword() (string, error) {
	b := make([]byte, 18)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
