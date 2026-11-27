package main

// main.go —— 程序入口：启动原生桌面窗口（不再使用浏览器）

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

func main() {
	if err := runApp(); err != nil {
		writeErrorLog(fmt.Sprintf("[%s] 窗口启动失败: %v\r\n", time.Now().Format("2006-01-02 15:04:05"), err))
		showErrorBox("批量替换助手", fmt.Sprintf("启动失败：\n%v\n\n请把这个提示截图发给开发者。", err))
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
