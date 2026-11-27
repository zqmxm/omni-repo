package main

// engine.go —— 执行引擎：扫描文件夹、替换文档内容、替换文件名（纯逻辑，可 Linux 测试）

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// maxFileSize 单个文档上限 100MB，防止异常文件
const maxFileSize = 100 * 1024 * 1024

// Execute 按请求执行全部替换，通过 onItem 回调逐项汇报进度
func Execute(req ReplaceReq, onItem func(Item)) (done, failed int) {
	files, err := scanFiles(req)
	if err != nil {
		onItem(Item{Path: req.Dir, Kind: "error", Status: "failed", Message: err.Error()})
		return 0, 1
	}

	// 先处理文档内容
	if req.ReplaceContent {
		for _, path := range files {
			kind := kindOfFile(path)
			if kind == "" {
				continue
			}
			item := replaceFileContent(path, kind, req.Pairs)
			onItem(item)
			if item.Status == "ok" {
				done++
			} else if item.Status == "failed" {
				failed++
			}
		}
	}

	// 再处理文件名（按路径深度倒序，避免父子目录改名导致路径失效）
	if req.RenameFiles {
		sort.Slice(files, func(i, j int) bool {
			return strings.Count(files[i], string(os.PathSeparator)) > strings.Count(files[j], string(os.PathSeparator))
		})
		for _, path := range files {
			item := renameFile(path, req.Pairs)
			if item.Status == "skipped" && item.Message == "" {
				continue // 文件名无需改动，不刷屏
			}
			onItem(item)
			if item.Status == "ok" {
				done++
			} else if item.Status == "failed" {
				failed++
			}
		}
	}
	return done, failed
}

// scanFiles 扫描文件夹里的所有目标文件
func scanFiles(req ReplaceReq) ([]string, error) {
	info, err := os.Stat(req.Dir)
	if err != nil {
		return nil, fmt.Errorf("文件夹不存在或无法访问：%w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("填写的路径不是文件夹：%s", req.Dir)
	}
	var files []string
	err = filepath.WalkDir(req.Dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // 跳过无法访问的条目
		}
		if d.IsDir() {
			if !req.Recursive && path != req.Dir {
				return filepath.SkipDir
			}
			base := d.Name()
			if strings.HasPrefix(base, ".") && path != req.Dir {
				return filepath.SkipDir // 跳过隐藏目录
			}
			return nil
		}
		if kindOfFile(path) == "" {
			return nil
		}
		if info, e := d.Info(); e == nil && info.Size() > maxFileSize {
			return nil // 超大文件跳过
		}
		files = append(files, path)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
}

// replaceFileContent 替换单个文件的内容
func replaceFileContent(path, kind string, pairs []Pair) Item {
	item := Item{Path: path, Kind: kind}
	switch kind {
	case "docx":
		hits, err := ReplaceDocx(path, path, pairs)
		item.Hits = hits
		if err != nil {
			item.Status, item.Message = "failed", err.Error()
		} else if hits == 0 {
			item.Status, item.Message = "skipped", "没有找到匹配内容"
		} else {
			item.Status, item.Message = "ok", fmt.Sprintf("替换了 %d 处", hits)
		}
	case "xlsx":
		hits, err := ReplaceXlsx(path, path, pairs)
		item.Hits = hits
		if err != nil {
			item.Status, item.Message = "failed", err.Error()
		} else if hits == 0 {
			item.Status, item.Message = "skipped", "没有找到匹配内容"
		} else {
			item.Status, item.Message = "ok", fmt.Sprintf("替换了 %d 处", hits)
		}
	case "legacy-doc", "legacy-xls":
		msg, err := replaceLegacy(path, kind, pairs)
		item.Status, item.Message = "ok", msg
		if err != nil {
			item.Status, item.Message = "skipped", err.Error()
		}
	}
	return item
}

// renameFile 替换文件名（只改主文件名，扩展名不变；目标已存在则跳过）
func renameFile(path string, pairs []Pair) Item {
	base := filepath.Base(path)
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	newStem, hits := applyPairs(stem, pairs)
	if hits == 0 {
		return Item{Path: path, Kind: "rename", Status: "skipped"}
	}
	newName := newStem + ext
	newPath := filepath.Join(filepath.Dir(path), newName)
	if newPath == path {
		return Item{Path: path, Kind: "rename", Status: "skipped"}
	}
	if _, err := os.Stat(newPath); err == nil {
		return Item{Path: path, Kind: "rename", Status: "failed", FromName: base, ToName: newName,
			Message: "目标文件名已存在，跳过（不覆盖）"}
	}
	if err := os.Rename(path, newPath); err != nil {
		return Item{Path: path, Kind: "rename", Status: "failed", FromName: base, ToName: newName,
			Message: "改名失败：" + err.Error()}
	}
	return Item{Path: path, Kind: "rename", Status: "ok", FromName: base, ToName: newName,
		Message: fmt.Sprintf("已改名为 %s", newName)}
}
