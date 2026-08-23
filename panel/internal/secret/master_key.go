package secret

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const masterKeySize = 32

func LoadOrCreateMasterKey(path string) ([]byte, bool, error) {
	if encoded := strings.TrimSpace(os.Getenv("PRISM_MASTER_KEY")); encoded != "" {
		key, err := decodeMasterKey(encoded)
		return key, false, err
	}
	contents, err := os.ReadFile(path)
	if err == nil {
		key, decodeErr := decodeMasterKey(strings.TrimSpace(string(contents)))
		return key, false, decodeErr
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, false, fmt.Errorf("read panel master key: %w", err)
	}
	key := make([]byte, masterKeySize)
	if _, err := rand.Read(key); err != nil {
		return nil, false, fmt.Errorf("generate panel master key: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, false, fmt.Errorf("create panel key directory: %w", err)
	}
	encoded := base64.RawURLEncoding.EncodeToString(key) + "\n"
	if err := os.WriteFile(path, []byte(encoded), 0o600); err != nil {
		return nil, false, fmt.Errorf("write panel master key: %w", err)
	}
	return key, true, nil
}

func decodeMasterKey(encoded string) ([]byte, error) {
	key, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(key) != masterKeySize {
		return nil, errors.New("panel master key must be a base64url encoded 32-byte value")
	}
	return key, nil
}
