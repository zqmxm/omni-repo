
//go:build !windows

package main

// ui_other.go —— 非 Windows 平台的空实现，保证 Linux 下能编译、跑单元测试。
// 真正的原生窗口在 ui_win.go（//go:build windows）。

func runApp() error {
	return nil
}
