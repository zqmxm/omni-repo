
//go:build windows && webui

package main

// pickfolder_win.go —— Web 控制台的「浏览…」按钮调用原生文件夹选择对话框。
// 关键：这里没有 Win32 窗口过程，模态对话框不会重入 Go 回调，
// 但仍放在 LockOSThread 的独立线程，避免阻塞 HTTP 处理线程。

import "runtime"

func pickFolderDialog() (string, error) {
	type result struct {
		path string
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		// owner 传 0：独立线程上不跨线程持有主窗口句柄
		p, e := pickFolder(0)
		ch <- result{path: p, err: e}
	}()
	r := <-ch
	return r.path, r.err
}
