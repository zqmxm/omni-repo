
//go:build !windows

package main

// 非 Windows 平台不需要错误弹窗，空实现便于在 Linux 上跑单元测试
func showErrorBox(title, text string) {}
