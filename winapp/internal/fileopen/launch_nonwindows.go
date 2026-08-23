//go:build !windows

package fileopen

import "errors"

func launchFile(string, bool) error {
	return errors.New("本机文件打开仅支持 Windows")
}
