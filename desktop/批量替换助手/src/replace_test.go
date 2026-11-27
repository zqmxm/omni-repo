package main

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// makeZip 用给定条目构造一个测试用 zip 文件
func makeZip(t *testing.T, path string, entries map[string]string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for name, content := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	f.Close()
}

// readEntry 读取 zip 中指定条目的文本
func readEntry(t *testing.T, path, name string) string {
	t.Helper()
	r, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	for _, f := range r.File {
		if normalizeEntryName(f.Name) == normalizeEntryName(name) {
			rc, err := f.Open()
			if err != nil {
				t.Fatal(err)
			}
			defer rc.Close()
			buf := make([]byte, 0, 4096)
			tmp := make([]byte, 4096)
			for {
				n, e := rc.Read(tmp)
				if n > 0 {
					buf = append(buf, tmp[:n]...)
				}
				if e != nil {
					break
				}
			}
			return string(buf)
		}
	}
	t.Fatalf("条目 %s 不存在", name)
	return ""
}

func TestApplyPairs(t *testing.T) {
	pairs := []Pair{{From: "北京", To: "上海"}, {From: "上海", To: "南京"}}
	got, hits := applyPairs("北京分公司在上海", pairs)
	if got != "南京分公司在南京" || hits != 3 {
		t.Errorf("链式替换错误: got=%q hits=%d", got, hits)
	}
}

func TestDocxReplaceCrossRun(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "a.docx")
	dst := filepath.Join(dir, "b.docx")
	// 「北京分公司」被拆成两个 run
	doc := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:body><w:p><w:r><w:t>北京</w:t></w:r><w:r><w:rPr><w:b/></w:rPr><w:t>分公司</w:t></w:r></w:p>
<w:p><w:r><w:t>无关内容</w:t></w:r></w:p></w:body></w:document>`
	makeZip(t, src, map[string]string{
		"word/document.xml":         doc,
		"[Content_Types].xml":       `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"/>`,
		"word/header1.xml":          `<w:hdr xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:p><w:r><w:t>北京分公司页眉</w:t></w:r></w:p></w:hdr>`,
	})

	hits, err := ReplaceDocx(src, dst, []Pair{{From: "北京分公司", To: "上海总部"}})
	if err != nil {
		t.Fatal(err)
	}
	if hits != 2 {
		t.Errorf("应命中正文与页眉各 1 次，got %d", hits)
	}
	out := readEntry(t, dst, "word/document.xml")
	if !strings.Contains(out, "上海总部") {
		t.Errorf("正文跨 run 替换失败: %s", out)
	}
	if !strings.Contains(out, "<w:p>") || !strings.Contains(out, "</w:p>") {
		t.Errorf("段落结构被破坏: %s", out)
	}
	if strings.Contains(out, "北京") {
		t.Errorf("正文旧文本残留: %s", out)
	}
	if !strings.Contains(out, "<w:b/>") {
		t.Error("run 样式丢失")
	}
	header := readEntry(t, dst, "word/header1.xml")
	if !strings.Contains(header, "上海总部页眉") {
		t.Errorf("页眉替换失败: %s", header)
	}
	ct := readEntry(t, dst, "[Content_Types].xml")
	if !strings.Contains(ct, "Types") {
		t.Error("非目标条目被破坏")
	}
}

func TestXlsxSharedStrings(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "a.xlsx")
	dst := filepath.Join(dir, "b.xlsx")
	ss := `<?xml version="1.0" encoding="UTF-8"?>
<sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" count="2" uniqueCount="2">
<si><t>北京分公司</t></si>
<si><r><t>北京</t></r><r><t>分公司</t></r></si>
</sst>`
	makeZip(t, src, map[string]string{
		"xl/sharedStrings.xml": ss,
		"xl/worksheets/sheet1.xml": `<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData/></worksheet>`,
	})

	hits, err := ReplaceXlsx(src, dst, []Pair{{From: "北京分公司", To: "上海总部"}})
	if err != nil {
		t.Fatal(err)
	}
	if hits != 2 {
		t.Errorf("应命中两个 si，got %d", hits)
	}
	out := readEntry(t, dst, "xl/sharedStrings.xml")
	if strings.Contains(out, "北京") {
		t.Errorf("共享字符串旧文本残留: %s", out)
	}
	if strings.Count(out, "上海总部") != 2 {
		t.Errorf("替换结果不对: %s", out)
	}
	if strings.Count(out, "<si>") != 2 || strings.Count(out, "</si>") != 2 {
		t.Errorf("共享字符串结构被破坏: %s", out)
	}
}

func TestXlsxInlineStringAndNumeric(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "a.xlsx")
	dst := filepath.Join(dir, "b.xlsx")
	sheet := `<?xml version="1.0" encoding="UTF-8"?>
<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">
<sheetData>
  <row><c r="A1" t="inlineStr"><is><t>北京分公司</t></is></c></row>
  <row><c r="B1"><v>2025</v></c></row>
</sheetData></worksheet>`
	makeZip(t, src, map[string]string{"xl/worksheets/sheet1.xml": sheet})

	hits, err := ReplaceXlsx(src, dst, []Pair{{From: "北京", To: "上海"}, {From: "2025", To: "2026"}})
	if err != nil {
		t.Fatal(err)
	}
	if hits != 1 {
		t.Errorf("数值单元格不应被替换，got %d", hits)
	}
	out := readEntry(t, dst, "xl/worksheets/sheet1.xml")
	if !strings.Contains(out, "上海分公司") || !strings.Contains(out, "<v>2025</v>") {
		t.Errorf("内联字符串或数值处理错误: %s", out)
	}
}

func TestZipIntegrity(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "a.docx")
	dst := filepath.Join(dir, "b.docx")
	makeZip(t, src, map[string]string{
		"word/document.xml": `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:p><w:r><w:t>测试</w:t></w:r></w:p></w:document>`,
	})
	if _, err := ReplaceDocx(src, dst, []Pair{{From: "测试", To: "验证"}}); err != nil {
		t.Fatal(err)
	}
	r, err := zip.OpenReader(dst)
	if err != nil {
		t.Fatalf("输出 docx 不是有效 zip: %v", err)
	}
	defer r.Close()
	if len(r.File) != 1 {
		t.Errorf("条目数变化: got %d", len(r.File))
	}
}

func TestValidateReq(t *testing.T) {
	cases := []struct {
		req  ReplaceReq
		wantErr bool
	}{
		{ReplaceReq{Dir: "", Pairs: []Pair{{From: "a", To: "b"}}, ReplaceContent: true}, true},
		{ReplaceReq{Dir: "/tmp", Pairs: nil, ReplaceContent: true}, true},
		{ReplaceReq{Dir: "/tmp", Pairs: []Pair{{From: "", To: "b"}}, ReplaceContent: true}, true},
		{ReplaceReq{Dir: "/tmp", Pairs: []Pair{{From: "a", To: "a"}}, ReplaceContent: true}, true},
		{ReplaceReq{Dir: "/tmp", Pairs: []Pair{{From: "a", To: "b"}}}, true},
		{ReplaceReq{Dir: "/tmp", Pairs: []Pair{{From: "a", To: "b"}}, ReplaceContent: true}, false},
	}
	for i, c := range cases {
		req := c.req
		err := validateReq(&req)
		if (err != nil) != c.wantErr {
			t.Errorf("case %d: err=%v wantErr=%v", i, err, c.wantErr)
		}
	}
}

func TestScanAndRename(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "子目录"), 0755)
	// 造一个有效 docx 用于扫描与文件名替换
	makeZip(t, filepath.Join(dir, "北京报告.docx"), map[string]string{
		"word/document.xml": `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:p><w:r><w:t>北京</w:t></w:r></w:p></w:document>`,
	})
	makeZip(t, filepath.Join(dir, "子目录", "北京表格.xlsx"), map[string]string{
		"xl/sharedStrings.xml": `<sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><si><t>北京</t></si></sst>`,
	})
	os.WriteFile(filepath.Join(dir, "北京说明.txt"), []byte("北京"), 0644)

	// 不递归：只扫到根目录的 docx
	files, err := scanFiles(ReplaceReq{Dir: dir, Recursive: false})
	if err != nil || len(files) != 1 {
		t.Fatalf("非递归扫描错误: %v files=%v", err, files)
	}
	// 递归：扫到 docx 和 xlsx
	files, err = scanFiles(ReplaceReq{Dir: dir, Recursive: true})
	if err != nil || len(files) != 2 {
		t.Fatalf("递归扫描错误: %v files=%v", err, files)
	}

	// 执行：替换内容 + 改名（递归）
	var items []Item
	done, failed := Execute(ReplaceReq{
		Dir: dir, Pairs: []Pair{{From: "北京", To: "上海"}},
		Recursive: true, ReplaceContent: true, RenameFiles: true,
	}, func(it Item) { items = append(items, it) })
	if failed > 0 || done < 4 {
		t.Errorf("执行结果异常: done=%d failed=%d items=%+v", done, failed, items)
	}
	// 验证文档内容
	out := readEntry(t, filepath.Join(dir, "上海报告.docx"), "word/document.xml")
	if !strings.Contains(out, "上海") {
		t.Errorf("文档内容未替换: %s", out)
	}
	// 验证子目录文件也改名且内容替换
	sub := readEntry(t, filepath.Join(dir, "子目录", "上海表格.xlsx"), "xl/sharedStrings.xml")
	if !strings.Contains(sub, "上海") {
		t.Errorf("子目录 xlsx 未替换: %s", sub)
	}
	// 非目标格式不该被改名
	if _, err := os.Stat(filepath.Join(dir, "北京说明.txt")); err != nil {
		t.Error("txt 文件不应被改名")
	}
	// 旧文件应不存在
	if _, err := os.Stat(filepath.Join(dir, "北京报告.docx")); err == nil {
		t.Error("原文件名应已被替换")
	}
}

func TestRenameConflict(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "北京.docx"), []byte("x"), 0644)
	os.WriteFile(filepath.Join(dir, "上海.docx"), []byte("y"), 0644)
	item := renameFile(filepath.Join(dir, "北京.docx"), []Pair{{From: "北京", To: "上海"}})
	if item.Status != "failed" || !strings.Contains(item.Message, "已存在") {
		t.Errorf("重名应跳过: %+v", item)
	}
}

func TestNumericEntities(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "a.xlsx")
	dst := filepath.Join(dir, "b.xlsx")
	// openpyxl 风格：中文写成数字字符实体
	sheet := `<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData><row><c r="A1" t="inlineStr"><is><t>&#21271;&#20140;&#20998;&#20844;&#21496;</t></is></c></row></sheetData></worksheet>`
	makeZip(t, src, map[string]string{"xl/worksheets/sheet1.xml": sheet})
	hits, err := ReplaceXlsx(src, dst, []Pair{{From: "北京分公司", To: "上海总部"}})
	if err != nil || hits != 1 {
		t.Fatalf("数字实体替换失败: hits=%d err=%v", hits, err)
	}
	out := readEntry(t, dst, "xl/worksheets/sheet1.xml")
	if !strings.Contains(out, "上海总部") || strings.Contains(out, "&#21271;") {
		t.Errorf("替换结果错误: %s", out)
	}
}
