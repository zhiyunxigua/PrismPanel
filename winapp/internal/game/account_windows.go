//go:build windows

package game

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	localAccountTarget    = "PrismPanel/game/netease-account"
	localAccountType      = 1
	localAccountBlobLimit = 5 * 1024
)

var ErrNotFound = errors.New("local NetEase account not found")


var (
	advapi32        = windows.NewLazySystemDLL("advapi32.dll")
	procCredWriteW  = advapi32.NewProc("CredWriteW")
	procCredReadW   = advapi32.NewProc("CredReadW")
	procCredDeleteW = advapi32.NewProc("CredDeleteW")
	procCredFree    = advapi32.NewProc("CredFree")
)

type localAccountStore struct{}

type localCredential struct {
	Flags              uint32
	Type               uint32
	TargetName         *uint16
	Comment            *uint16
	LastWritten        windows.Filetime
	CredentialBlobSize uint32
	CredentialBlob     *byte
	Persist            uint32
	AttributeCount     uint32
	Attributes         uintptr
	TargetAlias        *uint16
	UserName           *uint16
}

func newLocalAccountStore() LocalAccountStore { return localAccountStore{} }

func (localAccountStore) Load() (AccountState, error) {
	item, err := readLocalCredential(localAccountTarget)
	if err != nil {
		return AccountState{}, err
	}
	defer procCredFree.Call(uintptr(unsafe.Pointer(item)))
	if item.CredentialBlobSize == 0 || item.CredentialBlob == nil {
		return AccountState{}, ErrNotFound
	}
	payload := append([]byte(nil), unsafe.Slice(item.CredentialBlob, item.CredentialBlobSize)...)
	var account AccountState
	if err := json.Unmarshal(payload, &account); err != nil {
		return AccountState{}, err
	}
	return account, nil
}

func (localAccountStore) Save(account AccountState) error {
	account.Email = strings.TrimSpace(account.Email)
	account.Password = strings.TrimSpace(account.Password)
	if account.Email == "" || account.Password == "" {
		return errors.New("email and password are required")
	}
	if account.VerifiedAt == nil {
		now := time.Now().UTC()
		account.VerifiedAt = &now
	}
	payload, err := json.Marshal(account)
	if err != nil {
		return err
	}
	if len(payload) > localAccountBlobLimit {
		return fmt.Errorf("local account payload is too large")
	}
	return writeLocalCredential(localAccountTarget, account.Email, payload, account.VerifiedAt.UTC().Format(time.RFC3339Nano))
}

func (localAccountStore) Delete() error {
	target, err := windows.UTF16PtrFromString(localAccountTarget)
	if err != nil {
		return err
	}
	ok, _, callErr := procCredDeleteW.Call(uintptr(unsafe.Pointer(target)), localAccountType, 0)
	if ok == 0 && !errors.Is(callErr, windows.ERROR_NOT_FOUND) {
		return windowsCallError("delete local NetEase account", callErr)
	}
	return nil
}

func readLocalCredential(targetName string) (*localCredential, error) {
	target, err := windows.UTF16PtrFromString(targetName)
	if err != nil {
		return nil, err
	}
	var result *localCredential
	ok, _, callErr := procCredReadW.Call(uintptr(unsafe.Pointer(target)), localAccountType, 0, uintptr(unsafe.Pointer(&result)))
	if ok == 0 {
		if errors.Is(callErr, windows.ERROR_NOT_FOUND) {
			return nil, ErrNotFound
		}
		return nil, windowsCallError("read local NetEase account", callErr)
	}
	return result, nil
}

func writeLocalCredential(targetName, username string, blob []byte, comment string) error {
	target, err := windows.UTF16PtrFromString(targetName)
	if err != nil {
		return err
	}
	user, err := windows.UTF16PtrFromString(username)
	if err != nil {
		return err
	}
	commentPointer, err := windows.UTF16PtrFromString(comment)
	if err != nil {
		return err
	}
	var blobPointer *byte
	if len(blob) > 0 {
		blobPointer = &blob[0]
	}
	item := localCredential{
		Type: localAccountType, TargetName: target, Comment: commentPointer,
		CredentialBlobSize: uint32(len(blob)), CredentialBlob: blobPointer,
		Persist: 2, UserName: user,
	}
	ok, _, callErr := procCredWriteW.Call(uintptr(unsafe.Pointer(&item)), 0)
	if ok == 0 {
		return windowsCallError("write local NetEase account", callErr)
	}
	return nil
}

func localAccountDigest(value string) string {
	hash := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(value))))
	return hex.EncodeToString(hash[:])
}

func localAccountFingerprint(account AccountState) string {
	return localAccountDigest(account.Email)
}

func sortLocalAccountsByTime(accounts []AccountSummary) {
	sort.Slice(accounts, func(left, right int) bool {
		if accounts[left].UpdatedAt.Equal(accounts[right].UpdatedAt) {
			return strings.ToLower(accounts[left].Email) < strings.ToLower(accounts[right].Email)
		}
		return accounts[left].UpdatedAt.After(accounts[right].UpdatedAt)
	})
}

func windowsCallError(operation string, err error) error {
	if err == nil || errors.Is(err, windows.ERROR_NOT_FOUND) {
		return fmt.Errorf("%s failed", operation)
	}
	return fmt.Errorf("%s: %w", operation, err)
}
