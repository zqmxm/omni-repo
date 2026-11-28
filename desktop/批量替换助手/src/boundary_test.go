
package main

// boundary_test.go —— 边界与回归测试：空目录、非法路径、特殊字符、损坏文件、
// 另存/覆盖/冲突、撤销字节级还原、dry-run 零副作用、CLI 规则解析、状态隔离。

import (
	"bytes"
	"encoding/xml"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	btContentTypes = `<?xml version="1.0" encoding="UTF-8"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`
	btRels         = `<?xml version="1.0" encoding="UTF-8"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`
)

func btWriteDocx(t *testing.T, path, inner, text string) {
	t.Helper()
	doc := `<?xml version="1.0" encoding="UTF-8"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>` +
		inner + `</w:body></w:document>`
	if inner == "" {
		doc = `<?xml version="1.0" encoding="UTF-8"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>` +
			`<w:p><w:r><w:t>` + text + `</w:t></w:r></w:p></w:body></w:document>`
	}
	makeZip(t, path, map[string]string{
		"[Content_Types].xml": btContentTypes,
		"_rels/.rels":         btRels,
		"word/document.xml":   doc,
	})
}

func btRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func btState(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "state", "undo.json")
}

func btReq(dir, state string, pairs ...Pair) ReplaceReq {
	return ReplaceReq{
		Dir: dir, Pairs: pairs, Recursive: true, ReplaceContent: true,
		StatePath: state,
	}
}

func btExec(req ReplaceReq) ([]Item, int, int) {
	var items []Item
	done, failed := Execute(req, func(it Item) { items = append(items, it) })
	return items, done, failed
}

func btValidXML(t *testing.T, s string) {
	t.Helper()
	dec := xml.NewDecoder(strings.NewReader(s))
	for {
		_, err := dec.Token()
		if err == io.EOF {
			return
		}
		if err != nil {
			t.Fatalf("替换后 XML 不再合法：%v\n%s", err, s)
		}
	}
}

// ---------- 参数与扫描边界 ----------

func TestBoundaryEmptyAndInvalidDir(t *testing.T) {
	root := t.TempDir()
	state := btState(t)
	pairs := []Pair{{From: "AAAA", To: "ZZZZ"}}

	empty := filepath.Join(root, "empty")
	if err := os.MkdirAll(empty, 0755); err != nil {
		t.Fatal(err)
	}
	items, done, failed := btExec(btReq(empty, state, pairs...))
	if done != 0 || failed != 0 || len(items) != 0 {
		t.Errorf("空文件夹应无动作：done=%d failed=%d items=%+v", done, failed, items)
	}

	items, _, failed = btExec(btReq(filepath.Join(root, "nope"), state, pairs...))
	if failed != 1 || len(items) == 0 || !strings.Contains(items[0].Message, "不存在") {
		t.Errorf("不存在的路径应明确报错：failed=%d items=%+v", failed, items)
	}

	fp := filepath.Join(root, "a.txt")
	_ = os.WriteFile(fp, []byte("x"), 0644)
	items, _, failed = btExec(btReq(fp, state, pairs...))
	if failed != 1 || len(items) == 0 || !strings.Contains(items[0].Message, "不是文件夹") {
		t.Errorf("文件当目录应明确报错：failed=%d items=%+v", failed, items)
	}

	r := btReq(empty, state, pairs...)
	r.SaveAs = empty
	if err := validateReq(&r); err == nil || !strings.Contains(err.Error(), "不能与源文件夹相同") {
		t.Errorf("另存目录与源目录相同应被拒绝，得到 %v", err)
	}
	r2 := ReplaceReq{Dir: empty, Pairs: pairs}
	if err := validateReq(&r2); err == nil {
		t.Error("内容与文件名都不勾选时应报错")
	}
}

func TestBoundaryValidateRules(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name  string
		pairs []Pair
		want  int
	}{
		{"From 为空", []Pair{{From: "", To: "B"}}, 0},
		{"To 为空不参与", []Pair{{From: "A", To: ""}}, 0},
		{"替换成自己", []Pair{{From: "A", To: "A"}}, 0},
		{"首尾空格被 trim", []Pair{{From: "  ", To: "B"}}, 0},
		{"有效规则", []Pair{{From: "A", To: "B"}}, 1},
		{"混合过滤", []Pair{{From: "", To: "X"}, {From: "A", To: ""}, {From: "B", To: "C"}}, 1},
	}
	for _, c := range cases {
		r := ReplaceReq{Dir: dir, Pairs: c.pairs, ReplaceContent: true}
		err := validateReq(&r)
		if c.want == 0 {
			if err == nil {
				t.Errorf("%s: 应返回错误", c.name)
			}
		} else if err != nil || len(r.Pairs) != c.want {
			t.Errorf("%s: 期望 %d 条有效规则，得到 %d 条，err=%v", c.name, c.want, len(r.Pairs), err)
		}
	}
}

func TestBoundaryScanHiddenAndNested(t *testing.T) {
	dir := t.TempDir()
	btWriteDocx(t, filepath.Join(dir, "a.docx"), "", "AAAA")
	_ = os.MkdirAll(filepath.Join(dir, "sub"), 0755)
	btWriteDocx(t, filepath.Join(dir, "sub", "b.docx"), "", "AAAA")
	_ = os.MkdirAll(filepath.Join(dir, ".hidden"), 0755)
	btWriteDocx(t, filepath.Join(dir, ".hidden", "c.docx"), "", "AAAA")
	_ = os.WriteFile(filepath.Join(dir, "note.txt"), []byte("AAAA"), 0644)

	f1, err := scanFiles(ReplaceReq{Dir: dir, Recursive: false})
	if err != nil || len(f1) != 1 {
		t.Errorf("非递归应只扫到 1 个：%v %v", len(f1), err)
	}
	f2, err := scanFiles(ReplaceReq{Dir: dir, Recursive: true})
	if err != nil || len(f2) != 2 {
		t.Errorf("递归应扫到 2 个（隐藏目录跳过）：%v %v", len(f2), err)
	}
}

func TestBoundarySaveAsInsideSourceSkipped(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "output")
	_ = os.MkdirAll(out, 0755)
	btWriteDocx(t, filepath.Join(dir, "a.docx"), "", "AAAA")
	seed := filepath.Join(out, "seed.docx")
	btWriteDocx(t, seed, "", "AAAA")
	before := btRead(t, seed)

	req := btReq(dir, btState(t), Pair{From: "AAAA", To: "ZZZZ"})
	req.SaveAs = out
	items, _, _ := btExec(req)
	for _, it := range items {
		if it.Path == seed {
			t.Errorf("另存目录内已有文件被重复扫描：%+v", it)
		}
	}
	if !bytes.Equal(btRead(t, seed), before) {
		t.Error("另存目录内已有文件被改动")
	}
	if _, err := os.Stat(filepath.Join(out, "a.docx")); err != nil {
		t.Error("源文件未产出到另存目录")
	}
}

func TestBoundaryApplyPairsChainAndUnicode(t *testing.T) {
	s, n := applyPairs("北京 2025 AAAA 😀", []Pair{
		{From: "北京", To: "上海"}, {From: "上海", To: "南京"},
		{From: "2025", To: "2026"}, {From: "😀", To: "🎉"},
	})
	if s != "南京 2026 AAAA 🎉" || n != 4 {
		t.Errorf("链式/Unicode 替换错误：%q hits=%d", s, n)
	}
	if got, hits := applyPairs("AAAA", []Pair{{From: "", To: "X"}, {From: "A", To: "A"}}); got != "AAAA" || hits != 0 {
		t.Errorf("空 From / 自替换不应产生命中：%q hits=%d", got, hits)
	}
}

// ---------- OOXML 内容边界 ----------

func TestBoundaryXMLSpecialCharacters(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.docx")
	btWriteDocx(t, p, `<w:p><w:r><w:t>AAAA &amp; 已有 &lt;标签&gt;</w:t></w:r></w:p>`, "")

	req := btReq(dir, btState(t), Pair{From: "AAAA", To: "新&值<测试>"})
	_, done, failed := btExec(req)
	if done != 1 || failed != 0 {
		t.Errorf("特殊字符替换失败：done=%d failed=%d", done, failed)
	}
	out := readEntry(t, p, "word/document.xml")
	btValidXML(t, out)
	if !strings.Contains(out, "新&amp;值&lt;测试&gt;") {
		t.Errorf("替换内容未正确 XML 转义：%s", out)
	}
	if strings.Contains(out, "新&值<测试>") {
		t.Error("替换内容含未转义的 & 或 <，会破坏 XML")
	}
}

func TestBoundaryCrossRunAggregation(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.docx")
	btWriteDocx(t, p, `<w:p><w:r><w:t>AA</w:t></w:r><w:r><w:t>AA</w:t></w:r></w:p>`, "")
	req := btReq(dir, btState(t), Pair{From: "AAAA", To: "ZZZZ"})
	_, done, _ := btExec(req)
	if done != 1 {
		t.Errorf("跨 run 拼接后应能命中：done=%d", done)
	}
	out := readEntry(t, p, "word/document.xml")
	btValidXML(t, out)
	if !strings.Contains(out, "ZZZZ") {
		t.Errorf("跨 run 文本未替换：%s", out)
	}
}

func TestBoundaryCorruptedZipKeepsFile(t *testing.T) {
	dir := t.TempDir()
	state := btState(t)
	p := filepath.Join(dir, "bad.docx")
	_ = os.WriteFile(p, []byte("this is not a zip"), 0644)
	before := btRead(t, p)

	items, _, failed := btExec(btReq(dir, state, Pair{From: "AAAA", To: "ZZZZ"}))
	if failed != 1 || len(items) == 0 || items[0].Status != "failed" {
		t.Errorf("损坏文件应记为失败：failed=%d items=%+v", failed, items)
	}
	if !bytes.Equal(btRead(t, p), before) {
		t.Error("损坏文件被意外改动")
	}
	if _, err := os.Stat(state); !os.IsNotExist(err) {
		t.Error("全部失败时不应写入撤销记录")
	}
}

func TestBoundaryNoMatchLeavesBytesUntouched(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.docx")
	btWriteDocx(t, p, "", "AAAA")
	before := btRead(t, p)
	items, done, failed := btExec(btReq(dir, btState(t), Pair{From: "ZZZZ", To: "YYYY"}))
	if done != 0 || failed != 0 || len(items) == 0 || items[0].Status != "skipped" {
		t.Errorf("无命中应为 skipped：done=%d failed=%d items=%+v", done, failed, items)
	}
	if !bytes.Equal(btRead(t, p), before) {
		t.Error("无命中时不应重写文件（字节应完全不变）")
	}
}

// ---------- 另存 / 覆盖 / 冲突 ----------

func TestBoundarySaveAsKeepsSourceBytes(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(t.TempDir(), "out")
	_ = os.MkdirAll(filepath.Join(dir, "sub"), 0755)
	a := filepath.Join(dir, "AAAA_a.docx")
	b := filepath.Join(dir, "sub", "AAAA_b.docx")
	btWriteDocx(t, a, "", "AAAA")
	btWriteDocx(t, b, "", "AAAA")
	beforeA, beforeB := btRead(t, a), btRead(t, b)

	req := btReq(dir, btState(t), Pair{From: "AAAA", To: "ZZZZ"})
	req.SaveAs, req.RenameFiles = out, true
	_, done, failed := btExec(req)
	if failed != 0 || done != 2 {
		t.Errorf("另存应产出 2 个文件：done=%d failed=%d", done, failed)
	}
	if !bytes.Equal(btRead(t, a), beforeA) || !bytes.Equal(btRead(t, b), beforeB) {
		t.Error("另存模式改动了源文件")
	}
	for _, rel := range []string{"ZZZZ_a.docx", filepath.Join("sub", "ZZZZ_b.docx")} {
		p := filepath.Join(out, rel)
		if _, err := os.Stat(p); err != nil {
			t.Errorf("缺少产出文件 %s", rel)
			continue
		}
		if !strings.Contains(readEntry(t, p, "word/document.xml"), "ZZZZ") {
			t.Errorf("产出文件内容未替换 %s", rel)
		}
	}
}

func TestBoundarySaveAsCollisionAndOverwrite(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(t.TempDir(), "out")
	_ = os.MkdirAll(out, 0755)
	btWriteDocx(t, filepath.Join(dir, "a.docx"), "", "AAAA")
	target := filepath.Join(out, "a.docx")
	btWriteDocx(t, target, "", "OLD")
	oldBytes := btRead(t, target)

	req := btReq(dir, btState(t), Pair{From: "AAAA", To: "ZZZZ"})
	req.SaveAs = out
	items, done, failed := btExec(req)
	if failed != 0 || done != 0 || len(items) == 0 || !strings.Contains(items[0].Message, "已存在") {
		t.Errorf("目标已存在应跳过：done=%d failed=%d items=%+v", done, failed, items)
	}
	if !bytes.Equal(btRead(t, target), oldBytes) {
		t.Error("默认不应覆盖已有目标文件")
	}

	req.Overwrite = true
	_, done, failed = btExec(req)
	if failed != 0 || done != 1 {
		t.Errorf("显式覆盖应成功：done=%d failed=%d", done, failed)
	}
	if !strings.Contains(readEntry(t, target, "word/document.xml"), "ZZZZ") {
		t.Error("覆盖后内容未更新")
	}
}

func TestBoundaryRenameConflict(t *testing.T) {
	dir := t.TempDir()
	btWriteDocx(t, filepath.Join(dir, "AAAA_a.docx"), "", "AAAA")
	btWriteDocx(t, filepath.Join(dir, "AAAA_b.docx"), "", "AAAA")
	req := btReq(dir, btState(t),
		Pair{From: "AAAA_a", To: "X"}, Pair{From: "AAAA_b", To: "X"})
	req.RenameFiles = true
	_, done, failed := btExec(req)
	if failed < 1 {
		t.Errorf("改名冲突应有失败项：done=%d failed=%d", done, failed)
	}
	if _, err := os.Stat(filepath.Join(dir, "X.docx")); err != nil {
		t.Error("冲突时应保留一个成功改名的文件")
	}
}

// ---------- 撤销 ----------

func TestBoundaryUndoInplaceByteExact(t *testing.T) {
	dir := t.TempDir()
	state := btState(t)
	p := filepath.Join(dir, "AAAA_报告.docx")
	btWriteDocx(t, p, "", "AAAA")
	before := btRead(t, p)

	req := btReq(dir, state, Pair{From: "AAAA", To: "ZZZZ"})
	req.RenameFiles = true
	btExec(req)
	renamed := filepath.Join(dir, "ZZZZ_报告.docx")
	if _, err := os.Stat(renamed); err != nil {
		t.Fatal("改名未生效")
	}
	res, _, err := UndoLast(state)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) == 0 {
		t.Fatal("撤销结果为空")
	}
	if _, err := os.Stat(renamed); !os.IsNotExist(err) {
		t.Error("撤销后新文件名仍存在")
	}
	if _, err := os.Stat(p); err != nil || !bytes.Equal(btRead(t, p), before) {
		t.Error("撤销后原文件未按字节精确还原")
	}
	if entries, _ := os.ReadDir(backupDirFor(state)); len(entries) != 0 {
		t.Errorf("撤销后备份未清理：%d 个", len(entries))
	}
	if _, err := os.Stat(state); !os.IsNotExist(err) {
		t.Error("撤销栈空后应删除清单文件")
	}
}

func TestBoundaryUndoSaveAsRemovesOutputs(t *testing.T) {
	dir := t.TempDir()
	state := btState(t)
	out := filepath.Join(t.TempDir(), "out")
	p := filepath.Join(dir, "a.docx")
	btWriteDocx(t, p, "", "AAAA")
	before := btRead(t, p)

	req := btReq(dir, state, Pair{From: "AAAA", To: "ZZZZ"})
	req.SaveAs = out
	btExec(req)
	produced := filepath.Join(out, "a.docx")
	if _, err := os.Stat(produced); err != nil {
		t.Fatal("另存文件未生成")
	}
	if _, _, err := UndoLast(state); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(produced); !os.IsNotExist(err) {
		t.Error("撤销后另存文件应被删除")
	}
	if !bytes.Equal(btRead(t, p), before) {
		t.Error("撤销另存不应影响源文件")
	}
}

func TestBoundaryUndoStackMultiStep(t *testing.T) {
	dir := t.TempDir()
	state := btState(t)
	p := filepath.Join(dir, "x.docx")
	btWriteDocx(t, p, "", "AAAA")
	before := btRead(t, p)

	for _, rule := range []Pair{
		{From: "AAAA", To: "BBBB"}, {From: "BBBB", To: "CCCC"}, {From: "CCCC", To: "DDDD"},
	} {
		_, failed := Execute(btReq(dir, state, rule), func(Item) {})
		if failed > 0 {
			t.Fatalf("替换 %v 失败：failed=%d", rule, failed)
		}
	}
	if !HasUndo(state) {
		t.Fatal("多步操作后应可撤销")
	}
	for i := 0; i < 3; i++ {
		if _, _, err := UndoLast(state); err != nil {
			t.Fatalf("第 %d 次撤销失败：%v", i+1, err)
		}
	}
	if !bytes.Equal(btRead(t, p), before) {
		t.Error("连续撤销 3 次后未还原到初始字节")
	}
	if HasUndo(state) {
		t.Error("撤销栈应已清空")
	}
}

func TestBoundaryDryRunNoSideEffects(t *testing.T) {
	dir := t.TempDir()
	state := btState(t)
	out := filepath.Join(t.TempDir(), "out")
	p := filepath.Join(dir, "a.docx")
	btWriteDocx(t, p, "", "AAAA")
	before := btRead(t, p)

	req := btReq(dir, state, Pair{From: "AAAA", To: "ZZZZ"})
	req.SaveAs, req.RenameFiles, req.DryRun = out, true, true
	items, done, failed := btExec(req)
	if done != 0 || failed != 0 {
		t.Errorf("预览不应计入成功/失败：done=%d failed=%d", done, failed)
	}
	found := false
	for _, it := range items {
		if strings.Contains(it.Message, "预览") {
			found = true
		}
	}
	if !found {
		t.Errorf("预览应给出提示：%+v", items)
	}
	if !bytes.Equal(btRead(t, p), before) {
		t.Error("预览改动了源文件")
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Error("预览不应创建另存目录")
	}
	if _, err := os.Stat(state); !os.IsNotExist(err) {
		t.Error("预览不应生成撤销记录")
	}
}

// ---------- CLI 规则解析与状态隔离 ----------

func TestBoundaryCLIRulesParsing(t *testing.T) {
	var r ruleList
	if err := r.Set("A=B"); err != nil || len(r) != 1 || r[0].From != "A" || r[0].To != "B" {
		t.Errorf("--rule 解析错误：%v %+v", err, r)
	}
	if err := r.Set("X=Y=Z"); err != nil || r[1].To != "Y=Z" {
		t.Errorf("替换内容含 = 应保留：%v %+v", err, r)
	}
	if err := r.Set("noequals"); err == nil {
		t.Error("缺少 = 应报错")
	}
	p1, err := parseRulesJSON(`[{"from":"A","to":"B"}]`)
	if err != nil || len(p1) != 1 || p1[0].To != "B" {
		t.Errorf("JSON 数组解析错误：%v %+v", err, p1)
	}
	p2, err := parseRulesJSON(`{"pairs":[{"from":"C","to":"D"}]}`)
	if err != nil || len(p2) != 1 || p2[0].From != "C" {
		t.Errorf("JSON 对象解析错误：%v %+v", err, p2)
	}
	if _, err := parseRulesJSON(`{bad`); err == nil {
		t.Error("非法 JSON 应报错")
	}

	f := filepath.Join(t.TempDir(), "rules.txt")
	_ = os.WriteFile(f, []byte("# 注释\nA=B\n\n// 行注释\nC=D=E\n"), 0644)
	p3, err := loadRulesFile(f)
	if err != nil || len(p3) != 2 || p3[1].To != "D=E" {
		t.Errorf("规则文件解析错误：%v %+v", err, p3)
	}
	fj := filepath.Join(t.TempDir(), "rules.json")
	_ = os.WriteFile(fj, []byte(`[{"from":"E","to":"F"}]`), 0644)
	p4, err := loadRulesFile(fj)
	if err != nil || len(p4) != 1 {
		t.Errorf("JSON 规则文件解析错误：%v %+v", err, p4)
	}
}

func TestBoundaryStateIsolation(t *testing.T) {
	dir := t.TempDir()
	a, b := btState(t), btState(t)
	p := filepath.Join(dir, "x.docx")
	btWriteDocx(t, p, "", "AAAA")

	btExec(btReq(dir, a, Pair{From: "AAAA", To: "BBBB"}))
	if !HasUndo(a) || HasUndo(b) {
		t.Error("不同 --state 的撤销栈应相互隔离")
	}
	if _, _, err := UndoLast(b); err == nil || !strings.Contains(err.Error(), "没有可撤销") {
		t.Errorf("空撤销栈应给出明确提示，得到 %v", err)
	}
	if _, _, err := UndoLast(a); err != nil {
		t.Errorf("有效撤销栈应能撤销：%v", err)
	}
}

// TestNoMatchHintAndSkippedStats 复现真实用户场景：文件名含规则原文、正文不含，
// 且未勾选「同时替换文件名」。此时应给出可操作提示，并把跳过项计入统计。
func TestNoMatchHintAndSkippedStats(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "AAAA-报告.docx")
	btWriteDocx(t, src, "", "正文里没有规则原文")

	var items []Item
	stats := ExecuteEx(btReq(dir, btState(t), Pair{From: "AAAA", To: "1"}),
		func(it Item) { items = append(items, it) })

	if stats.Done != 0 || stats.Failed != 0 || stats.Skipped != 1 {
		t.Fatalf("应为「成功 0 / 跳过 1 / 失败 0」，实际 %+v，items=%+v", stats, items)
	}
	hinted := false
	for _, it := range items {
		if it.Status == "skipped" && strings.Contains(it.Message, "同时替换文件名") {
			hinted = true
		}
	}
	if !hinted {
		t.Fatalf("应提示勾选「同时替换文件名」，实际消息：%+v", items)
	}
	if _, err := os.Stat(src); err != nil {
		t.Fatal("未勾选文件名替换时，原文件名不应被改动")
	}
}

// TestRenameOnlyChangesFileName 勾选「同时替换文件名」后，正文无命中也应完成改名。
func TestRenameOnlyChangesFileName(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "AAAA-报告.docx")
	btWriteDocx(t, src, "", "正文里没有规则原文")

	req := btReq(dir, btState(t), Pair{From: "AAAA", To: "1"})
	req.RenameFiles = true
	stats := ExecuteEx(req, func(Item) {})

	if stats.Done != 1 {
		t.Fatalf("改名应计为成功 1 项，实际 %+v", stats)
	}
	if _, err := os.Stat(filepath.Join(dir, "1-报告.docx")); err != nil {
		t.Fatalf("文件名未替换成功：%v", err)
	}
}
