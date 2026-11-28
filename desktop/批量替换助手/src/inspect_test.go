
package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"piliangtihuanzhushou/internal/mscfb"
)

// TestInspectDoc 检查真实 .doc 样本的结构（开发期调试用）
func TestInspectDoc(t *testing.T) {
	for _, path := range []string{"../testdata/test.doc", "../testdata/novpapplan.doc"} {
		f, err := os.Open(path)
		if err != nil {
			t.Skipf("无法打开 %s: %v", path, err)
		}
		r, err := mscfb.New(f)
		if err != nil {
			t.Fatalf("mscfb.New(%s): %v", path, err)
		}
		fmt.Printf("\n=== %s ===\n", path)
		for {
			e, err := r.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			fmt.Printf("stream: name=%q size=%d\n", e.Name, e.Size)
			if e.Name == "WordDocument" || e.Name == "0Table" || e.Name == "1Table" {
				data, err := io.ReadAll(e)
				if err != nil {
					t.Fatal(err)
				}
				if len(data) >= 32 {
					fmt.Printf("  wIdent=0x%04X nFib=0x%04X flags=0x%04X fcMin=%d fcMac=%d\n",
						rd16(data, 0), rd16(data, 2), rd16(data, 10), rd32(data, 24), rd32(data, 28))
				}
				if len(data) >= 160 {
					fmt.Printf("  cbRgFcLcb=%d fcClx=%d lcbClx=%d\n",
						rd16(data, 152), rd32(data, 154+33*8), rd32(data, 154+33*8+4))
				}
			}
		}
		f.Close()
	}
}

func rd16(b []byte, off int) uint16 {
	return uint16(b[off]) | uint16(b[off+1])<<8
}

func rd32(b []byte, off int) uint32 {
	return uint32(b[off]) | uint32(b[off+1])<<8 | uint32(b[off+2])<<16 | uint32(b[off+3])<<24
}

func TestExtractDocText(t *testing.T) {
	for _, path := range []string{"../testdata/test.doc", "../testdata/novpapplan.doc"} {
		info, err := parseDocInfo(path)
		if err != nil {
			t.Errorf("%s: %v", path, err)
			continue
		}
		text, err := extractText(info)
		if err != nil {
			t.Errorf("%s extract: %v", path, err)
			continue
		}
		fmt.Printf("\n=== %s: table=%s pieces=%d ccpText=%d text=%d chars ===\n",
			path, info.tableName, len(info.pieces), info.ccpText, len([]rune(text)))
		preview := text
		if len(preview) > 300 {
			preview = preview[:300]
		}
		fmt.Printf("文本预览: %q\n", preview)
	}
}

func TestOleRoundTrip(t *testing.T) {
	for _, path := range []string{"../testdata/test.doc", "../testdata/novpapplan.doc"} {
		entries, err := readAllOLEStreams(path)
		if err != nil {
			t.Fatalf("%s read: %v", path, err)
		}
		t.Logf("%s: %d streams", path, len(entries))
		for _, e := range entries {
			t.Logf("  %q isDir=%v size=%d", e.name, e.isDir, len(e.data))
		}
		out, err := buildOleContainer(entries)
		if err != nil {
			t.Fatalf("%s build: %v", path, err)
		}
		tmp := t.TempDir() + "/roundtrip.doc"
		if err := os.WriteFile(tmp, out, 0644); err != nil {
			t.Fatal(err)
		}
		// 验证：重新读取，对比每个流
		got, err := readAllOLEStreams(tmp)
		if err != nil {
			t.Fatalf("%s reread: %v (len=%d)", path, err, len(out))
		}
		if len(got) != len(entries) {
			t.Errorf("%s: stream count %d != %d", path, len(got), len(entries))
		}
		orig := map[string][]byte{}
		for _, e := range entries {
			orig[e.name] = e.data
		}
		for _, e := range got {
			if !bytes.Equal(orig[e.name], e.data) {
				t.Errorf("%s: stream %q differs (got %d, want %d)", path, e.name, len(e.data), len(orig[e.name]))
			}
		}
		// 验证文档仍可被 parseDocInfo 解析
		if _, err := parseDocInfo(tmp); err != nil {
			t.Errorf("%s: reparsed doc failed: %v", path, err)
		}
	}
}

func TestReplaceDocText(t *testing.T) {
	src := "../testdata/novpapplan.doc"
	info, err := parseDocInfo(src)
	if err != nil {
		t.Fatal(err)
	}
	origText, err := extractText(info)
	if err != nil {
		t.Fatal(err)
	}
	// 选一个原文里确实存在的词
	from := "research"
	if !strings.Contains(origText, from) {
		from = "British"
	}
	if !strings.Contains(origText, from) {
		t.Skip("样本中找不到可替换词")
	}
	to := "【替换测试】"

	tmp := t.TempDir() + "/replaced.doc"
	data, _ := os.ReadFile(src)
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		t.Fatal(err)
	}
	hits, err := replaceDocText(tmp, []Pair{{From: from, To: to}})
	if err != nil {
		t.Fatalf("replaceDocText: %v", err)
	}
	if hits == 0 {
		t.Fatal("未命中")
	}
	// 重新解析并提取
	info2, err := parseDocInfo(tmp)
	if err != nil {
		t.Fatalf("替换后无法解析: %v", err)
	}
	newText, err := extractText(info2)
	if err != nil {
		t.Fatalf("替换后无法提取文本: %v", err)
	}
	if !strings.Contains(newText, to) {
		t.Errorf("替换后文本中找不到 %q", to)
	}
	if strings.Count(newText, from) != strings.Count(origText, from)-hits {
		t.Errorf("原文剩余次数异常: orig=%d hits=%d new=%d",
			strings.Count(origText, from), hits, strings.Count(newText, from))
	}
	fmt.Printf("成功: %s→%s 命中%d处, 替换后文本长度=%d\n", from, to, hits, len([]rune(newText)))
}

func TestReplaceXlsText(t *testing.T) {
	src := "../testdata/test.xls"
	entries, err := readAllOLEStreams(src)
	if err != nil {
		t.Fatal(err)
	}
	var wb []byte
	for _, e := range entries {
		if e.name == "Workbook" || e.name == "Book" {
			wb = e.data
		}
	}
	if wb == nil {
		t.Fatal("无 Workbook 流")
	}
	recs, err := parseBIFFRecords(wb)
	if err != nil {
		t.Fatal(err)
	}
	// 找 SST
	for i, r := range recs {
		if r.id == biffSST {
			sstRecs := [][]byte{r.data}
			for j := i + 1; j < len(recs) && recs[j].id == biffContinue; j++ {
				sstRecs = append(sstRecs, recs[j].data)
			}
			strs, err := parseSST(sstRecs)
			if err != nil {
				t.Fatalf("parseSST: %v", err)
			}
			fmt.Printf("XLS SST: %d 个字符串\n", len(strs))
			for k, s := range strs {
				if k >= 8 {
					break
				}
				fmt.Printf("  [%d] %q\n", k, s.text)
			}
			break
		}
	}
	// 执行替换
	tmp := t.TempDir() + "/replaced.xls"
	data, _ := os.ReadFile(src)
	os.WriteFile(tmp, data, 0644)
	hits, err := ReplaceXls(tmp, []Pair{{From: "Test", To: "测试OK"}})
	if err != nil {
		t.Fatalf("ReplaceXls: %v", err)
	}
	fmt.Printf("替换命中: %d\n", hits)
	// 写回后再读
	entries2, err := readAllOLEStreams(tmp)
	if err != nil {
		t.Fatalf("替换后读取失败: %v", err)
	}
	for _, e := range entries2 {
		if e.name == "Workbook" {
			if _, err := parseBIFFRecords(e.data); err != nil {
				t.Fatalf("替换后 BIFF 损坏: %v", err)
			}
		}
	}
	fmt.Println("替换后 OLE/BIFF 结构有效")
}
