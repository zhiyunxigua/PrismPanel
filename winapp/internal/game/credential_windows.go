//go:build windows

package game

import (
	"errors"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// 共享 Windows 凭据（DPAPI Credential Manager）读写助手，
// 供本地账号存储（国际版 mc-account / 其他）复用。

const (
	localAccountType      = 1
	localAccountBlobLimit = 5 * 1024
)

// ErrNotFound 表示本地凭据中不存在目标条目。
var ErrNotFound = errors.New("local account credential not found")

var (
	advapi32        = windows.NewLazySystemDLL("advapi32.dll")
	procCredWriteW  = advapi32.NewProc("CredWriteW")
	procCredReadW   = advapi32.NewProc("CredReadW")
	procCredDeleteW = advapi32.NewProc("CredDeleteW")
	procCredFree    = advapi32.NewProc("CredFree")
)

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

func localCredentialDelete(targetName string) error {
	target, err := windows.UTF16PtrFromString(targetName)
	if err != nil {
		return err
	}
	ok, _, callErr := procCredDeleteW.Call(uintptr(unsafe.Pointer(target)), localAccountType, 0)
	if ok == 0 && !errors.Is(callErr, windows.ERROR_NOT_FOUND) {
		return windowsCallError("delete local account credential", callErr)
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
		return nil, windowsCallError("read local account credential", callErr)
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
		return windowsCallError("write local account credential", callErr)
	}
	return nil
}

func windowsCallError(operation string, err error) error {
	if err == nil || errors.Is(err, windows.ERROR_NOT_FOUND) {
		return fmt.Errorf("%s failed", operation)
	}
	return fmt.Errorf("%s: %w", operation, err)
}
