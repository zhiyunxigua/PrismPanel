//go:build windows

package fileopen

import (
	"fmt"
	"syscall"
	"unsafe"
)

var shellExecuteW = syscall.NewLazyDLL("shell32.dll").NewProc("ShellExecuteW")

func launchFile(filePath string, chooseApplication bool) error {
	verb := "open"
	if chooseApplication {
		verb = "openas"
	}
	verbPointer, _ := syscall.UTF16PtrFromString(verb)
	pathPointer, _ := syscall.UTF16PtrFromString(filePath)
	result, _, callErr := shellExecuteW.Call(
		0, uintptr(unsafe.Pointer(verbPointer)), uintptr(unsafe.Pointer(pathPointer)), 0, 0, 1,
	)
	if result <= 32 {
		return fmt.Errorf("启动本机应用失败: %v", callErr)
	}
	return nil
}
