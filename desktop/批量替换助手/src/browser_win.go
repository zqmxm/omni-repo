
//go:build windows && webui

package main

// browser_win.go —— 用 ShellExecuteW 调用系统默认浏览器，比 rundll32 更可靠。

import (
	"syscall"
	"unsafe"
)

var (
	shell32Browser = syscall.NewLazyDLL("shell32.dll")
	pShellExecuteW = shell32Browser.NewProc("ShellExecuteW")
)

func openBrowserNative(url string) bool {
	u, err := syscall.UTF16PtrFromString(url)
	if err != nil {
		return false
	}
	op, _ := syscall.UTF16PtrFromString("open")
	// SW_SHOWNORMAL = 1
	r, _, _ := pShellExecuteW.Call(
		0,
		uintptr(unsafe.Pointer(op)),
		uintptr(unsafe.Pointer(u)),
		0, 0, 1,
	)
	// 返回值 > 32 表示成功
	return r > 32
}
