
package main

// main.go —— 纯本地桌面程序入口。
//
// 双击 exe 直接创建原生 Win32 窗口：
//   - 不启动 HTTP 服务、不监听任何端口
//   - 不打开浏览器、不访问网络
//   - 不依赖 Office / WPS / .NET / 任何运行时，单文件即可运行
//
// 界面实现见 ui_win.go（Windows）；非 Windows 平台为空实现，仅用于跑单元测试。

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

func main() {
	// 带任意命令行参数 → 跨平台 CLI 模式；无参数 → Windows 原生桌面窗口。
	if len(os.Args) > 1 {
		setupConsole()
		os.Exit(runCLI(os.Args[1:]))
	}
	if runtime.GOOS != "windows" {
		// Linux / macOS 没有桌面窗口，直接给命令行帮助
		setupConsole()
		os.Exit(runCLI([]string{"--help"}))
	}

	// Win32 硬要求：窗口的创建、消息派发、窗口过程必须在同一个 OS 线程上执行。
	// Go 的 goroutine 默认可能被调度到别的线程，锁死线程可根除偶发的"未响应"。
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	if err := runApp(); err != nil {
		writeErrorLog(fmt.Sprintf("[%s] 启动失败: %v\r\n", time.Now().Format("2006-01-02 15:04:05"), err))
		showErrorBox("批量替换助手", fmt.Sprintf("启动失败：\n%v\n\n请把这个提示截图发给开发者。", err))
		os.Exit(1)
	}
}

func writeErrorLog(msg string) {
	path := "批量替换助手-error.log"
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		path = filepath.Join(os.TempDir(), "批量替换助手-error.log")
		f, err = os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
		if err != nil {
			return
		}
	}
	defer f.Close()
	if fi, e := f.Stat(); e == nil && fi.Size() == 0 {
		f.WriteString("\ufeff")
	}
	f.WriteString(msg)
}
