
//go:build !windows && webui

package main

// 非 Windows 平台无原生文件夹对话框，返回空路径让前端提示手动填写。

func pickFolderDialog() (string, error) {
	return "", nil
}
