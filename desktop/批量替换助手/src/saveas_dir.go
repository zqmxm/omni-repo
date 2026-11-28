package main

// saveas_dir.go —— 另存目录归一化（v3.1.5）
//
// 用户诉求演进：
//   v3.1.4「输出放文件夹里，和源文件夹一样」→ 自动建 <源文件夹名>_新版
//   v3.1.5「源文件夹也要替换名字」→ 输出文件夹名本身也要过同一套替换规则
//
// 例如源文件夹 BBBBAAAA，规则 AAAA→1111、BBBB→2222（链式），
// 输出文件夹就叫 22221111（源文件夹 BBBBAAAA 本身永远不动）。
// 若规则没命中文件夹名，退化为 BBBBAAAA_新版，避免与源文件夹同名。

import (
	"path/filepath"
	"strings"
)

// winIllegal 把 Windows 文件名非法字符换成下划线，防止用户填的规则里带 / : * ? 等。
var winIllegal = strings.NewReplacer(
	`<`, `_`, `>`, `_`, `:`, `_`, `"`, `_`, `/`, `_`, `\`, `_`, `|`, `_`, `?`, `_`, `*`, `_`,
)

func cleanFolderName(s string) string {
	return strings.TrimSpace(winIllegal.Replace(s))
}

// normalizeSaveAsDir 返回真正使用的另存目录。
// pairs 与文件内容/文件名替换用的是同一套规则，顺序链式生效。
func normalizeSaveAsDir(dir, saveAs string, pairs []Pair) string {
	if strings.TrimSpace(saveAs) == "" {
		return saveAs // 原地模式
	}
	srcDir := filepath.Clean(dir)
	out := filepath.Clean(saveAs)
	if strings.EqualFold(out, srcDir) {
		return out // 交给 validateReq 明确报错
	}
	srcName := filepath.Base(srcDir)
	if srcName == "" || srcName == "." || srcName == string(filepath.Separator) {
		return out
	}

	// 输出文件夹名 = 源文件夹名过一遍同样的规则
	newName, hits := applyPairs(srcName, pairs)
	newName = cleanFolderName(newName)
	if hits == 0 || newName == "" || strings.EqualFold(newName, srcName) {
		newName = srcName + "_新版"
	}

	outName := filepath.Base(out)
	// 用户已经显式指定了一个输出文件夹（名字里带源名 / 带替换后的名字）→
	// 不套娃，但也要把名字里的规则原文替换掉。
	if strings.Contains(strings.ToLower(outName), strings.ToLower(srcName)) ||
		strings.Contains(strings.ToLower(outName), strings.ToLower(newName)) {
		renamed, _ := applyPairs(outName, pairs)
		renamed = cleanFolderName(renamed)
		if renamed == "" {
			renamed = outName
		}
		if strings.EqualFold(renamed, srcName) {
			renamed += "_新版"
		}
		return filepath.Join(filepath.Dir(out), renamed)
	}
	return filepath.Join(out, newName)
}
