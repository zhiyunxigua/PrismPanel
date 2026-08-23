//go:build windows

package game

import (
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unsafe"
)

const mcLocalAccountTarget = "PrismPanel/game/mc-account"

type mcLocalStore struct{}

func newMCLocalStore() MCLocalStore { return mcLocalStore{} }

func (mcLocalStore) Load() (MCAccount, error) {
	item, err := readLocalCredential(mcLocalAccountTarget)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return MCAccount{}, ErrMCNone
		}
		return MCAccount{}, err
	}
	defer procCredFree.Call(uintptr(unsafe.Pointer(item)))
	if item.CredentialBlobSize == 0 || item.CredentialBlob == nil {
		return MCAccount{}, ErrMCNone
	}
	payload := append([]byte(nil), unsafe.Slice(item.CredentialBlob, item.CredentialBlobSize)...)
	var account MCAccount
	if err := json.Unmarshal(payload, &account); err != nil {
		return MCAccount{}, err
	}
	return account, nil
}

func (mcLocalStore) Save(account MCAccount) error {
	account.Name = strings.TrimSpace(account.Name)
	account.UUID = strings.TrimSpace(account.UUID)
	if account.Mode == "" {
		account.Mode = MCAuthOffline
	}
	if account.UpdatedAt.IsZero() {
		account.UpdatedAt = time.Now().UTC()
	}
	payload, err := json.Marshal(account)
	if err != nil {
		return err
	}
	if len(payload) > localAccountBlobLimit {
		return errors.New("local account payload is too large")
	}
	return writeLocalCredential(mcLocalAccountTarget, string(account.Mode)+":"+account.Name, payload, account.UpdatedAt.UTC().Format(time.RFC3339Nano))
}

func (mcLocalStore) Delete() error {
	if err := localCredentialDelete(mcLocalAccountTarget); err != nil {
		return err
	}
	return nil
}
