//go:build windows

package credentials

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	credentialTypeGeneric         = 1
	credentialPersistLocalMachine = 2
	credentialPrefix              = "PrismPanel"
	credentialBlobLimit           = 5 * 512
)

var (
	advapi32          = windows.NewLazySystemDLL("advapi32.dll")
	procCredWriteW    = advapi32.NewProc("CredWriteW")
	procCredReadW     = advapi32.NewProc("CredReadW")
	procCredEnumerate = advapi32.NewProc("CredEnumerateW")
	procCredDeleteW   = advapi32.NewProc("CredDeleteW")
	procCredFree      = advapi32.NewProc("CredFree")
)

type WindowsStore struct{}

type nativeCredential struct {
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

func NewStore() Store { return WindowsStore{} }

func (WindowsStore) List(panelURL string) ([]Account, error) {
	prefix := accountTargetPrefix(panelURL)
	filter, err := windows.UTF16PtrFromString(prefix + "*")
	if err != nil {
		return nil, err
	}
	var count uint32
	var result **nativeCredential
	ok, _, callErr := procCredEnumerate.Call(
		uintptr(unsafe.Pointer(filter)), 0,
		uintptr(unsafe.Pointer(&count)), uintptr(unsafe.Pointer(&result)),
	)
	if ok == 0 {
		if errors.Is(callErr, windows.ERROR_NOT_FOUND) {
			return []Account{}, nil
		}
		return nil, windowsCallError("enumerate saved accounts", callErr)
	}
	defer procCredFree.Call(uintptr(unsafe.Pointer(result)))
	items := unsafe.Slice(result, count)
	accounts := make([]Account, 0, count)
	for _, item := range items {
		if item == nil {
			continue
		}
		target := windows.UTF16PtrToString(item.TargetName)
		accountID := strings.TrimPrefix(target, prefix)
		if !validAccountID(accountID) {
			continue
		}
		lastLoginAt := parseLastLoginAt(windows.UTF16PtrToString(item.Comment), item.LastWritten)
		accounts = append(accounts, Account{
			ID: accountID, Username: windows.UTF16PtrToString(item.UserName), LastLoginAt: lastLoginAt,
		})
	}
	sort.Slice(accounts, func(left, right int) bool {
		if accounts[left].LastLoginAt.Equal(accounts[right].LastLoginAt) {
			return strings.ToLower(accounts[left].Username) < strings.ToLower(accounts[right].Username)
		}
		return accounts[left].LastLoginAt.After(accounts[right].LastLoginAt)
	})
	return accounts, nil
}

func (WindowsStore) Get(panelURL, accountID string) (Credential, error) {
	if !validAccountID(accountID) {
		return Credential{}, ErrNotFound
	}
	item, err := readCredential(accountTargetPrefix(panelURL) + accountID)
	if err != nil {
		return Credential{}, err
	}
	defer procCredFree.Call(uintptr(unsafe.Pointer(item)))
	password := ""
	if item.CredentialBlobSize > 0 && item.CredentialBlob != nil {
		password = string(append([]byte(nil), unsafe.Slice(item.CredentialBlob, item.CredentialBlobSize)...))
	}
	return Credential{
		Account: Account{
			ID: accountID, Username: windows.UTF16PtrToString(item.UserName),
			LastLoginAt: parseLastLoginAt(windows.UTF16PtrToString(item.Comment), item.LastWritten),
		},
		Password: password,
	}, nil
}

func (WindowsStore) Save(panelURL, username, password string, lastLoginAt time.Time) (Account, error) {
	username = strings.TrimSpace(username)
	if username == "" || password == "" {
		return Account{}, errors.New("username and password are required")
	}
	blob := []byte(password)
	if len(blob) > credentialBlobLimit {
		return Account{}, errors.New("password is too large for Windows Credential Manager")
	}
	accountID := accountID(username)
	if lastLoginAt.IsZero() {
		lastLoginAt = time.Now().UTC()
	} else {
		lastLoginAt = lastLoginAt.UTC()
	}
	if err := writeCredential(
		accountTargetPrefix(panelURL)+accountID, username, blob, lastLoginAt.Format(time.RFC3339Nano),
	); err != nil {
		return Account{}, err
	}
	return Account{ID: accountID, Username: username, LastLoginAt: lastLoginAt}, nil
}

func (WindowsStore) Delete(panelURL, accountID string) error {
	if !validAccountID(accountID) {
		return ErrNotFound
	}
	target, err := windows.UTF16PtrFromString(accountTargetPrefix(panelURL) + accountID)
	if err != nil {
		return err
	}
	ok, _, callErr := procCredDeleteW.Call(
		uintptr(unsafe.Pointer(target)), credentialTypeGeneric, 0,
	)
	if ok == 0 && !errors.Is(callErr, windows.ERROR_NOT_FOUND) {
		return windowsCallError("delete saved account", callErr)
	}
	current, readErr := WindowsStore{}.AutoLoginAccount(panelURL)
	if readErr == nil && current == accountID {
		return WindowsStore{}.SetAutoLoginAccount(panelURL, "")
	}
	return nil
}

// ClearAll 删除指定面板 URL 范围下的全部已保存账号与自动登录标记。
// 仅枚举当前面板 scope（PrismPanel/<scope>/account/* + auto-login），
// 不会触碰其他面板或国际版游戏账号（PrismPanel/game/mc-account）。
func (WindowsStore) ClearAll(panelURL string) error {
	store := WindowsStore{}
	accounts, err := store.List(panelURL)
	if err != nil {
		return err
	}
	var firstErr error
	for _, account := range accounts {
		if err := store.Delete(panelURL, account.ID); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if err := store.SetAutoLoginAccount(panelURL, ""); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

func (WindowsStore) AutoLoginAccount(panelURL string) (string, error) {
	item, err := readCredential(autoLoginTarget(panelURL))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return "", nil
		}
		return "", err
	}
	defer procCredFree.Call(uintptr(unsafe.Pointer(item)))
	if item.CredentialBlobSize == 0 || item.CredentialBlob == nil {
		return "", nil
	}
	accountID := string(append([]byte(nil), unsafe.Slice(item.CredentialBlob, item.CredentialBlobSize)...))
	if !validAccountID(accountID) {
		return "", nil
	}
	return accountID, nil
}

func (WindowsStore) SetAutoLoginAccount(panelURL, accountID string) error {
	if accountID == "" {
		target, err := windows.UTF16PtrFromString(autoLoginTarget(panelURL))
		if err != nil {
			return err
		}
		ok, _, callErr := procCredDeleteW.Call(
			uintptr(unsafe.Pointer(target)), credentialTypeGeneric, 0,
		)
		if ok == 0 && !errors.Is(callErr, windows.ERROR_NOT_FOUND) {
			return windowsCallError("clear automatic login account", callErr)
		}
		return nil
	}
	if !validAccountID(accountID) {
		return ErrNotFound
	}
	return writeCredential(autoLoginTarget(panelURL), "PrismPanel", []byte(accountID), "")
}

func readCredential(targetName string) (*nativeCredential, error) {
	target, err := windows.UTF16PtrFromString(targetName)
	if err != nil {
		return nil, err
	}
	var result *nativeCredential
	ok, _, callErr := procCredReadW.Call(
		uintptr(unsafe.Pointer(target)), credentialTypeGeneric, 0,
		uintptr(unsafe.Pointer(&result)),
	)
	if ok == 0 {
		if errors.Is(callErr, windows.ERROR_NOT_FOUND) {
			return nil, ErrNotFound
		}
		return nil, windowsCallError("read saved account", callErr)
	}
	return result, nil
}

func writeCredential(targetName, username string, blob []byte, comment string) error {
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
	item := nativeCredential{
		Type: credentialTypeGeneric, TargetName: target, Comment: commentPointer,
		CredentialBlobSize: uint32(len(blob)), CredentialBlob: blobPointer,
		Persist: credentialPersistLocalMachine, UserName: user,
	}
	ok, _, callErr := procCredWriteW.Call(uintptr(unsafe.Pointer(&item)), 0)
	if ok == 0 {
		return windowsCallError("write saved account", callErr)
	}
	return nil
}

func accountTargetPrefix(panelURL string) string {
	return credentialPrefix + "/" + panelScope(panelURL) + "/account/"
}

func autoLoginTarget(panelURL string) string {
	return credentialPrefix + "/" + panelScope(panelURL) + "/auto-login"
}

func panelScope(panelURL string) string {
	hash := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(panelURL))))
	return hex.EncodeToString(hash[:])
}

func accountID(username string) string {
	hash := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(username))))
	return hex.EncodeToString(hash[:])
}

func validAccountID(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func parseLastLoginAt(value string, fallback windows.Filetime) time.Time {
	if parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(value)); err == nil {
		return parsed.UTC()
	}
	return time.Unix(0, fallback.Nanoseconds()).UTC()
}

func windowsCallError(operation string, err error) error {
	if err == nil || errors.Is(err, syscall.Errno(0)) {
		return fmt.Errorf("%s failed", operation)
	}
	return fmt.Errorf("%s: %w", operation, err)
}
