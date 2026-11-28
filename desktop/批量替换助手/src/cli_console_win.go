
//go:build windows

package main

// cli_console_win.go —— 让同一个 exe 在命令行参数模式下也能显示中文输出。
// GUI 版用 -H=windowsgui 编译，本身没有控制台；带参数运行时附加/新建控制台，
// 并把代码页切到 UTF-8，避免中文乱码。

import (
	"os"
	"syscall"
)

var (
	cliK32                 = syscall.NewLazyDLL("kernel32.dll")
	pAttachConsole         = cliK32.NewProc("AttachConsole")
	pAllocConsole          = cliK32.NewProc("AllocConsole")
	pSetConsoleOutputCP    = cliK32.NewProc("SetConsoleOutputCP")
	pSetConsoleCP          = cliK32.NewProc("SetConsoleCP")
)

func setupConsole() {
	// ATTACH_PARENT_PROCESS = (DWORD)-1：从 cmd/PowerShell 启动时复用父控制台
	if r, _, _ := pAttachConsole.Call(^uintptr(0)); r == 0 {
		pAllocConsole.Call() // 双击等方式启动时自己开一个
	}
	pSetConsoleOutputCP.Call(65001)
	pSetConsoleCP.Call(65001)
	// 只在标准输出确实不可用（GUI 版无控制台）时才改指向 CONOUT$。
	// 控制台版若也强行改向，输出会绕过管道/重定向，导致脚本捕获不到内容。
	if _, err := os.Stdout.Stat(); err != nil {
		if f, e := os.OpenFile("CONOUT$", os.O_WRONLY, 0); e == nil {
			os.Stdout = f
		}
	}
	if _, err := os.Stderr.Stat(); err != nil {
		if f, e := os.OpenFile("CONOUT$", os.O_WRONLY, 0); e == nil {
			os.Stderr = f
		}
	}
}
