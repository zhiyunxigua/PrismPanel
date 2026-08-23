//go:build !windows

package game

import "errors"

// 非 Windows 平台没有 Credential Manager，本地凭据助手直接报错。

func localCredentialDelete(targetName string) error {
	return errors.New("local credential storage is unavailable on this platform")
}

func readLocalCredential(targetName string) (*localCredential, error) {
	return nil, errors.New("local credential storage is unavailable on this platform")
}

func writeLocalCredential(targetName, username string, blob []byte, comment string) error {
	return errors.New("local credential storage is unavailable on this platform")
}
