
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
	Dir            string `json:"dir"`
	Pairs          []Pair `json:"pairs"`
	Recursive      bool   `json:"recursive"`
	ReplaceContent bool   `json:"replaceContent"`
	RenameFiles    bool   `json:"renameFiles"`

	// SaveAs 非空表示「另存」：结果写到该目录，源文件完全不动。
	// 为空表示「原地替换」（默认，即不勾选另存时在原文件上改）。
	SaveAs string `json:"saveAs,omitempty"`

	// DryRun 只预览会改动哪些文件，不写入、不生成撤销记录。
	DryRun bool `json:"dryRun,omitempty"`

	// Overwrite 允许另存时覆盖目标目录里已存在的同名文件（默认跳过不覆盖）。
	Overwrite bool `json:"overwrite,omitempty"`

	// StatePath 指定撤销清单路径；为空则用程序同目录（或用户配置目录）的默认位置。
	// 命令行与测试可显式指定以便隔离。
	StatePath string `json:"statePath,omitempty"`
}

// Item 一个文件/一次改名的处理结果
type Item struct {
	Path     string `json:"path"`
	Kind     string `json:"kind"`   // docx | xlsx | legacy-doc | legacy-xls | rename | saveas | skip | error
	Status   string `json:"status"` // ok | skipped | failed
	Message  string `json:"message"`
	Hits     int    `json:"hits"`
	FromName string `json:"fromName,omitempty"`
	ToName   string `json:"toName,omitempty"`
	Output   string `json:"output,omitempty"`
}

// applyPairs 按规则顺序依次替换（规则链式生效）
func applyPairs(s string, pairs []Pair) (string, int) {
	total := 0
	for _, p := range pairs {
		if p.From == "" || p.From == p.To {
			continue // 空规则 / 替换成自己：不产生任何字节改动
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
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("文件夹路径无法识别：%w", err)
	}
	req.Dir = absDir

	if !req.ReplaceContent && !req.RenameFiles {
		return fmt.Errorf("请至少选择「替换文档内容」或「替换文件名」中的一项")
	}

	if s := strings.TrimSpace(req.SaveAs); s != "" {
		absSave, err := filepath.Abs(s)
		if err != nil {
			return fmt.Errorf("另存目录路径无法识别：%w", err)
		}
		if samePath(absSave, absDir) {
			return fmt.Errorf("另存目录不能与源文件夹相同；不勾选另存即为在原文件上修改")
		}
		req.SaveAs = absSave
	} else {
		req.SaveAs = ""
	}

	clean := make([]Pair, 0, len(req.Pairs))
	for _, p := range req.Pairs {
		p.From = strings.TrimSpace(p.From)
		if p.From == "" {
			continue
		}
		// 「替换为」留空的行不参与替换：与桌面界面 collectReq 保持一致，
		// 避免经由 API/旧 Web 路径把文字静默删除。
		if p.To == "" {
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

// samePath 跨平台比较路径是否指向同一位置（Windows 大小写不敏感）
func samePath(a, b string) bool {
	a = filepath.Clean(a)
	b = filepath.Clean(b)
	return strings.EqualFold(a, b)
}

// kindOfFile 按扩展名判断文件类型
func kindOfFile(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".docx", ".docm", ".dotx", ".dotm":
		return "docx" // 基于 OOXML（ZIP）
	case ".xlsx", ".xlsm", ".xltx", ".xltm":
		return "xlsx" // 基于 OOXML（ZIP）
	case ".doc":
		return "legacy-doc" // OLE2 二进制
	case ".xls":
		return "legacy-xls" // OLE2 二进制
	default:
		return ""
	}
}
