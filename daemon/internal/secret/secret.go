package secret

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"PrismPanel-daemon/internal/atomicfile"
)

type File struct {
	Secret    string    `json:"secret"`
	NodeID    string    `json:"node_id"`
	CreatedAt time.Time `json:"created_at"`
}

func LoadOrCreate(path string) (File, bool, error) {
	contents, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		value, err := generate()
		if err != nil {
			return File{}, false, err
		}
		nodeID, err := generateID()
		if err != nil {
			return File{}, false, err
		}
		file := File{Secret: value, NodeID: nodeID, CreatedAt: time.Now().UTC()}
		if err := Save(path, file); err != nil {
			return File{}, false, err
		}
		return file, true, nil
	}
	if err != nil {
		return File{}, false, fmt.Errorf("read secret: %w", err)
	}
	var file File
	if err := json.Unmarshal(contents, &file); err != nil {
		return File{}, false, fmt.Errorf("decode secret: %w", err)
	}
	if len(file.Secret) < 32 {
		return File{}, false, errors.New("stored daemon secret is invalid")
	}
	if file.NodeID == "" {
		file.NodeID, err = generateID()
		if err != nil {
			return File{}, false, err
		}
		if err := Save(path, file); err != nil {
			return File{}, false, err
		}
	}
	return file, false, nil
}

func Reset(path string) (File, error) {
	current, _, err := LoadOrCreate(path)
	if err != nil {
		return File{}, err
	}
	value, err := generate()
	if err != nil {
		return File{}, err
	}
	file := File{Secret: value, NodeID: current.NodeID, CreatedAt: time.Now().UTC()}
	return file, Save(path, file)
}

func Save(path string, file File) error {
	contents, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return fmt.Errorf("encode secret: %w", err)
	}
	contents = append(contents, '\n')
	if err := atomicfile.WriteFile(path, contents, 0o600); err != nil {
		return fmt.Errorf("write secret: %w", err)
	}
	return nil
}

func generate() (string, error) {
	buffer := make([]byte, 32)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("generate secret: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}

func generateID() (string, error) {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("generate node id: %w", err)
	}
	return hex.EncodeToString(buffer), nil
}
