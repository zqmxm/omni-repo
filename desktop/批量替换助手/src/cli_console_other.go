
//go:build !windows

package main

// 非 Windows 平台本来就运行在终端里，无需额外设置控制台。
func setupConsole() {}
