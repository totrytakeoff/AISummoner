package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

type passwordParams struct {
	memory      uint32
	iterations  uint32
	parallelism uint8
	saltLength  uint32
	keyLength   uint32
}

var defaultPasswordParams = passwordParams{
	memory:      64 * 1024,
	iterations:  3,
	parallelism: 2,
	saltLength:  16,
	keyLength:   32,
}

// HashPassword creates the fixed Argon2id PHC hash required by the MVP baseline.
func HashPassword(password string) (string, error) {
	return hashPassword(password, defaultPasswordParams)
}

func hashPassword(password string, params passwordParams) (string, error) {
	if password == "" || len(password) > 1024 {
		return "", errors.New("password must contain between 1 and 1024 bytes")
	}
	salt := make([]byte, params.saltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("read password salt: %w", err)
	}
	hash := argon2.IDKey([]byte(password), salt, params.iterations, params.memory, params.parallelism, params.keyLength)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, params.memory, params.iterations, params.parallelism,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(hash)), nil
}

// VerifyPassword parses a bounded Argon2id PHC string and compares in constant time.
func VerifyPassword(encodedHash, password string) (bool, error) {
	params, salt, expected, err := parsePasswordHash(encodedHash)
	if err != nil {
		return false, err
	}
	actual := argon2.IDKey([]byte(password), salt, params.iterations, params.memory, params.parallelism, uint32(len(expected)))
	return subtle.ConstantTimeCompare(actual, expected) == 1, nil
}

func parsePasswordHash(encoded string) (passwordParams, []byte, []byte, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return passwordParams{}, nil, nil, errors.New("invalid Argon2id PHC hash")
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return passwordParams{}, nil, nil, errors.New("unsupported Argon2id version")
	}
	var params passwordParams
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &params.memory, &params.iterations, &params.parallelism); err != nil {
		return passwordParams{}, nil, nil, errors.New("invalid Argon2id parameters")
	}
	if parts[3] != fmt.Sprintf("m=%d,t=%d,p=%d", params.memory, params.iterations, params.parallelism) {
		return passwordParams{}, nil, nil, errors.New("invalid Argon2id parameter encoding")
	}
	if params.memory < 8*1024 || params.memory > 256*1024 || params.iterations < 1 || params.iterations > 10 || params.parallelism < 1 || params.parallelism > 8 {
		return passwordParams{}, nil, nil, errors.New("Argon2id parameters outside safety bounds")
	}
	salt, err := base64.RawStdEncoding.Strict().DecodeString(parts[4])
	if err != nil || len(salt) < 8 || len(salt) > 64 {
		return passwordParams{}, nil, nil, errors.New("invalid Argon2id salt")
	}
	expected, err := base64.RawStdEncoding.Strict().DecodeString(parts[5])
	if err != nil || len(expected) < 16 || len(expected) > 64 {
		return passwordParams{}, nil, nil, errors.New("invalid Argon2id output")
	}
	params.saltLength = uint32(len(salt))
	params.keyLength = uint32(len(expected))
	return params, salt, expected, nil
}
