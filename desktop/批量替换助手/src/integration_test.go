
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEndToEnd 端到端：混合格式文件夹一次性批量替换（内容 + 文件名）
func TestEndToEnd(t *testing.T) {
	dir := t.TempDir()

	// 1) docx
	makeZip(t, filepath.Join(dir, "北京报告.docx"), map[string]string{
		"word/document.xml": `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>北京公司</w:t></w:r></w:p></w:body></w:document>`,
	})
	// 2) xlsx
	makeZip(t, filepath.Join(dir, "北京数据.xlsx"), map[string]string{
		"xl/sharedStrings.xml": `<sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><si><t>北京公司</t></si></sst>`,
	})
	// 3) doc（真实样本复制，替换其正文里的 research）
	docSrc, err := os.ReadFile("../testdata/novpapplan.doc")
	if err != nil {
		t.Skip("缺少 doc 样本")
	}
	os.WriteFile(filepath.Join(dir, "北京旧文档.doc"), docSrc, 0644)
	// 4) xls（真实样本复制，替换其 SST 里的 Test）
	xlsSrc, err := os.ReadFile("../testdata/test.xls")
	if err != nil {
		t.Skip("缺少 xls 样本")
	}
	os.WriteFile(filepath.Join(dir, "北京旧表格.xls"), xlsSrc, 0644)

	req := ReplaceReq{
		Dir:            dir,
		Pairs:          []Pair{{From: "北京", To: "上海"}},
		Recursive:      false,
		ReplaceContent: true,
		RenameFiles:    true,
	}
	done, failed := Execute(req, func(it Item) {})
	if failed > 0 {
		t.Errorf("有失败项: failed=%d done=%d", failed, done)
	}
	t.Logf("完成: done=%d failed=%d", done, failed)

	// 验证文件名全部替换
	for _, name := range []string{"上海报告.docx", "上海数据.xlsx", "上海旧文档.doc", "上海旧表格.xls"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("文件未改名: %s", name)
		}
	}
	// 验证 docx/xlsx 内容
	out := readEntry(t, filepath.Join(dir, "上海报告.docx"), "word/document.xml")
	if !strings.Contains(out, "上海公司") {
		t.Errorf("docx 内容未替换")
	}
	out = readEntry(t, filepath.Join(dir, "上海数据.xlsx"), "xl/sharedStrings.xml")
	if !strings.Contains(out, "上海公司") {
		t.Errorf("xlsx 内容未替换")
	}
	// 验证 doc/xls 内容仍可解析
	if _, err := parseDocInfo(filepath.Join(dir, "上海旧文档.doc")); err != nil {
		t.Errorf("doc 替换后损坏: %v", err)
	}
	entries, err := readAllOLEStreams(filepath.Join(dir, "上海旧表格.xls"))
	if err != nil {
		t.Errorf("xls 替换后损坏: %v", err)
	} else {
		for _, e := range entries {
			if e.name == "Workbook" || e.name == "Book" {
				if _, err := parseBIFFRecords(e.data); err != nil {
					t.Errorf("xls BIFF 损坏: %v", err)
				}
			}
		}
	}
}
