package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

const (
	argonMemory      = 64 * 1024
	argonIterations  = 3
	argonParallelism = 2
	argonSaltLength  = 16
	argonKeyLength   = 32
)

func HashPassword(password string) (string, error) {
	salt := make([]byte, argonSaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate password salt: %w", err)
	}
	hash := argon2.IDKey(
		[]byte(password), salt, argonIterations, argonMemory, argonParallelism, argonKeyLength,
	)
	return fmt.Sprintf(
		"$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s",
		argonMemory,
		argonIterations,
		argonParallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(hash),
	), nil
}

func VerifyPassword(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" || parts[2] != "v=19" {
		return false
	}
	var memory, iterations uint64
	var parallelism uint64
	for _, parameter := range strings.Split(parts[3], ",") {
		name, value, ok := strings.Cut(parameter, "=")
		if !ok {
			return false
		}
		number, err := strconv.ParseUint(value, 10, 32)
		if err != nil {
			return false
		}
		switch name {
		case "m":
			memory = number
		case "t":
			iterations = number
		case "p":
			parallelism = number
		default:
			return false
		}
	}
	if memory == 0 || iterations == 0 || parallelism == 0 || parallelism > 255 {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) < 8 {
		return false
	}
	expected, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(expected) < 16 {
		return false
	}
	actual := argon2.IDKey(
		[]byte(password), salt, uint32(iterations), uint32(memory), uint8(parallelism), uint32(len(expected)),
	)
	return subtle.ConstantTimeCompare(actual, expected) == 1
}

func ValidatePassword(password string) error {
	length := utf8.RuneCountInString(password)
	switch {
	case length < 6:
		return errors.New("密码至少需要 6 个字符")
	case length > 128:
		return errors.New("密码不能超过 128 个字符")
	default:
		return nil
	}
}
