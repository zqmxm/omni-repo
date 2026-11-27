package main

// types.go —— 批量替换助手的公共数据结构（纯逻辑，无 Windows 依赖）

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Pair 一组替换规则：把 From 全部替换成 To
type Pair struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// ReplaceReq 一次执行请求
type ReplaceReq struct {
	Dir             string `json:"dir"`
	Pairs           []Pair `json:"pairs"`
	Recursive       bool   `json:"recursive"`
	ReplaceContent  bool   `json:"replaceContent"`
	RenameFiles     bool   `json:"renameFiles"`
}

// Item 一个文件/一次改名的处理结果
type Item struct {
	Path    string `json:"path"`
	Kind    string `json:"kind"` // docx | xlsx | legacy | rename | skip | error
	Status  string `json:"status"` // ok | skipped | failed
	Message string `json:"message"`
	Hits    int    `json:"hits"`
	FromName string `json:"fromName,omitempty"`
	ToName   string `json:"toName,omitempty"`
}

// applyPairs 按规则顺序依次替换（规则链式生效）
func applyPairs(s string, pairs []Pair) (string, int) {
	total := 0
	for _, p := range pairs {
		if p.From == "" {
			continue
		}
		n := strings.Count(s, p.From)
		if n > 0 {
			s = strings.ReplaceAll(s, p.From, p.To)
			total += n
		}
	}
	return s, total
}

// validateReq 校验请求，返回中文错误说明
func validateReq(req *ReplaceReq) error {
	dir := strings.TrimSpace(req.Dir)
	if dir == "" {
		return fmt.Errorf("请先填写文件夹路径")
	}
	req.Dir = dir
	if !req.ReplaceContent && !req.RenameFiles {
		return fmt.Errorf("请至少选择「替换文档内容」或「替换文件名」中的一项")
	}
	clean := make([]Pair, 0, len(req.Pairs))
	for _, p := range req.Pairs {
		p.From = strings.TrimSpace(p.From)
		if p.From == "" {
			continue
		}
		if p.From == p.To {
			continue // 替换成自己，无意义
		}
		clean = append(clean, p)
	}
	if len(clean) == 0 {
		return fmt.Errorf("请至少填写一组有效的替换规则（原内容不能为空，且与原内容不同）")
	}
	req.Pairs = clean
	return nil
}

// kindOfFile 按扩展名判断文件类型
func kindOfFile(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".docx":
		return "docx"
	case ".xlsx":
		return "xlsx"
	case ".doc":
		return "legacy-doc"
	case ".xls":
		return "legacy-xls"
	default:
		return ""
	}
}
