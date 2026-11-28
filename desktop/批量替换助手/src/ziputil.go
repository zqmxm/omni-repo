
package main

// ziputil.go —— ZIP（docx/xlsx 本质）保真复制与选择性重写（纯标准库）

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// transformFunc 接收条目原始文本，返回替换后的文本与命中次数
type transformFunc func(content string) (string, int)

// rewriteZipDynamic 与 rewriteZip 类似，但变换函数由条目名动态决定（返回 nil 表示原样复制）
func rewriteZipDynamic(src, dst string, picker func(name string) transformFunc) (int, error) {
	r, err := zip.OpenReader(src)
	if err != nil {
		return 0, fmt.Errorf("打开文档失败（可能损坏或加密）：%w", err)
	}
	defer r.Close()

	dir := filepath.Dir(dst)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return 0, err
	}
	tmp, err := os.CreateTemp(dir, ".nf-replace-*.tmp")
	if err != nil {
		return 0, err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	zw := zip.NewWriter(tmp)
	total := 0
	for _, f := range r.File {
		tf := picker(f.Name)
		if tf == nil {
			if err := zw.Copy(f); err != nil {
				tmp.Close()
				return 0, fmt.Errorf("复制文档条目 %s 失败：%w", f.Name, err)
			}
			continue
		}
		rc, err := f.Open()
		if err != nil {
			tmp.Close()
			return 0, fmt.Errorf("读取文档条目 %s 失败：%w", f.Name, err)
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			tmp.Close()
			return 0, err
		}
		newData, hits := tf(string(data))
		total += hits
		hdr := f.FileHeader
		hdr.Method = f.Method
		w, err := zw.CreateHeader(&hdr)
		if err != nil {
			tmp.Close()
			return 0, err
		}
		if _, err := w.Write([]byte(newData)); err != nil {
			tmp.Close()
			return 0, err
		}
	}
	if err := zw.Close(); err != nil {
		tmp.Close()
		return 0, fmt.Errorf("打包文档失败：%w", err)
	}
	if err := tmp.Close(); err != nil {
		return 0, err
	}
	// 写回前必须先关闭源 zip reader：Windows 不允许重命名/删除仍被打开的文件，
	// 否则 os.Rename 会报 "Access is denied"（Linux 允许，故仅在 Windows 暴露）。
	// 下方 defer r.Close() 仍保留作为错误路径兜底，重复 Close 是安全的。
	_ = r.Close()
	if info, err := os.Stat(src); err == nil {
		_ = os.Chmod(tmpPath, info.Mode())
	}
	// 安全写回：先把原文件挪到 .bak，再让新文件就位；只有新文件就位成功
	// 才删除备份。任何一步失败都能把原文件挪回，避免"删了原文件却写不进新文件"
	// 导致文档永久丢失。
	if _, err := os.Stat(dst); err == nil {
		bak := dst + ".nf-bak"
		_ = os.Remove(bak) // 清理上次异常退出可能残留的备份
		if err := os.Rename(dst, bak); err != nil {
			return 0, fmt.Errorf("备份原文档失败：%w", err)
		}
		if err := os.Rename(tmpPath, dst); err != nil {
			_ = os.Rename(bak, dst) // 回滚：恢复原文件
			return 0, fmt.Errorf("写回文档失败：%w", err)
		}
		_ = os.Remove(bak)
	} else {
		if err := os.Rename(tmpPath, dst); err != nil {
			return 0, fmt.Errorf("写回文档失败：%w", err)
		}
	}
	return total, nil
}

// normalizeEntryName 统一条目名：去掉大小写差异，便于匹配
func normalizeEntryName(name string) string {
	return strings.ToLower(strings.TrimPrefix(name, "/"))
}
