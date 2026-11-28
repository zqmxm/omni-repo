
package main

// xls.go —— Excel .xls (BIFF8) 纯本地文本替换
//
// .xls 是 OLE2 容器，工作簿在 "Workbook" 流，由 BIFF 记录序列组成。
// 文本主要存在 SST（Shared String Table，记录号 0x00FC）里，
// 通过 CONTINUE（0x003C）记录续接。替换 SST 里的字符串即可批量替换单元格文本，
// 且不改变公式、数字、格式等其他记录。

import (
	"encoding/binary"
	"fmt"
	"os"
	"strings"
)

const (
	biffBOF      = 0x0809
	biffSST      = 0x00FC
	biffContinue = 0x003C
	biffEOF      = 0x000A
)

// sstString 表示一个共享字符串
type sstString struct {
	text     string
	richRuns []byte // 富文本格式信息
	extData  []byte // 扩展数据
	hasRich  bool
	hasExt   bool
}

// parseSST 解析 SST 记录及其后续 CONTINUE 记录中的字符串
func parseSST(records [][]byte) ([]sstString, error) {
	if len(records) == 0 {
		return nil, fmt.Errorf("空 SST")
	}
	data := records[0]
	if len(data) < 8 {
		return nil, fmt.Errorf("SST 记录过短")
	}
	total := int(binary.LittleEndian.Uint32(data[0:4]))
	unique := int(binary.LittleEndian.Uint32(data[4:8]))
	if unique > total || unique > 1000000 {
		return nil, fmt.Errorf("SST 计数异常: total=%d unique=%d", total, unique)
	}

	// 把所有 CONTINUE 记录拼成一个大 buffer，同时记录边界（用于正确处理跨记录的字符串）
	// 简化：BIFF8 字符串可能跨 CONTINUE 边界，边界处有 option flag 字节
	type chunk struct{ data []byte }
	chunks := []struct{ data []byte }{{data: data[8:]}}
	for _, r := range records[1:] {
		chunks = append(chunks, struct{ data []byte }{data: r})
	}

	// 用一个读取器处理跨 CONTINUE 的读取
	br := &sstReader{chunks: chunks}
	stringsOut := make([]sstString, 0, unique)
	for i := 0; i < unique; i++ {
		cch, err := br.readU16()
		if err != nil {
			return nil, fmt.Errorf("读取第 %d 个字符串长度失败: %w", i, err)
		}
		flags, err := br.readByte()
		if err != nil {
			return nil, fmt.Errorf("读取第 %d 个字符串标志失败: %w", i, err)
		}
		hasRich := flags&0x08 != 0
		hasExt := flags&0x04 != 0
		isUTF16 := flags&0x01 != 0
		cRun := 0
		if hasRich {
			v, err := br.readU16()
			if err != nil {
				return nil, err
			}
			cRun = int(v)
		}
		cbExt := 0
		if hasExt {
			v, err := br.readU32()
			if err != nil {
				return nil, err
			}
			cbExt = int(v)
		}
		// 读取字符数据（可能跨 CONTINUE；切在字符串之间时不该有 option flag，
		// 所以进入字符段前清掉上一次跨块标记）
		br.inChars = true
		br.charUTF16 = isUTF16
		br.boundaryPending = false
		chars := make([]uint16, 0, cch)
		for j := 0; j < int(cch); j++ {
			v, err := br.readChar(isUTF16)
			if err != nil {
				return nil, fmt.Errorf("读取第 %d 个字符串字符失败: %w", i, err)
			}
			chars = append(chars, v)
		}
		br.inChars = false
		richRuns, err := br.readBytes(cRun * 4)
		if err != nil {
			return nil, err
		}
		ext, err := br.readBytes(cbExt)
		if err != nil {
			return nil, err
		}
		stringsOut = append(stringsOut, sstString{
			text:     decodeUTF16(chars),
			richRuns: richRuns,
			extData:  ext,
			hasRich:  hasRich,
			hasExt:   hasExt,
		})
	}
	return stringsOut, nil
}

// sstReader 跨 CONTINUE 记录读取字节
type sstReader struct {
	chunks   []struct{ data []byte }
	chunkIdx int
	pos      int

	// v3.1.6：BIFF8 规定，字符串跨 CONTINUE 边界时，新记录开头有 1 字节
	// option flag，指明后续字符是压缩(0)还是 UTF-16(1)。旧实现直接拼接，
	// 会把这个 flag 当成字符读进去，导致字符串整体错位。
	inChars         bool
	charUTF16       bool
	boundaryPending bool
}

func (r *sstReader) readByte() (byte, error) {
	for r.chunkIdx < len(r.chunks) {
		c := &r.chunks[r.chunkIdx]
		if r.pos < len(c.data) {
			b := c.data[r.pos]
			r.pos++
			return b, nil
		}
		r.chunkIdx++
		r.pos = 0
		r.boundaryPending = true // 跨到下一个 CONTINUE 了
	}
	return 0, fmt.Errorf("SST 数据结束")
}

func (r *sstReader) readU16() (uint16, error) {
	a, err := r.readByte()
	if err != nil {
		return 0, err
	}
	b, err := r.readByte()
	if err != nil {
		return 0, err
	}
	return uint16(a) | uint16(b)<<8, nil
}

func (r *sstReader) readU32() (uint32, error) {
	a, err := r.readU16()
	if err != nil {
		return 0, err
	}
	b, err := r.readU16()
	if err != nil {
		return 0, err
	}
	return uint32(a) | uint32(b)<<16, nil
}

func (r *sstReader) readChar(isUTF16 bool) (uint16, error) {
	if r.inChars {
		if r.boundaryPending {
			flag, err := r.readByte() // 消费 CONTINUE 开头的 option flag
			if err != nil {
				return 0, err
			}
			r.charUTF16 = flag&0x01 != 0
			r.boundaryPending = false
		}
		isUTF16 = r.charUTF16
	}
	if isUTF16 {
		return r.readU16()
	}
	b, err := r.readByte()
	if err != nil {
		return 0, err
	}
	return uint16(b), nil
}

func (r *sstReader) readBytes(n int) ([]byte, error) {
	out := make([]byte, n)
	for i := 0; i < n; i++ {
		b, err := r.readByte()
		if err != nil {
			return nil, err
		}
		out[i] = b
	}
	return out, nil
}

// encodeOneSSTString 编码单个 XLUnicodeRichExtendedString（不含记录头）
func encodeOneSSTString(s sstString) []byte {
	var out []byte
	u := encodeUTF16(s.text)
	f := byte(0x01) // UTF-16
	if s.hasRich {
		f |= 0x08
	}
	if s.hasExt {
		f |= 0x04
	}
	out = appendU16(out, uint16(len(u)))
	out = append(out, f)
	if s.hasRich {
		out = appendU16(out, uint16(len(s.richRuns)/4))
	}
	if s.hasExt {
		out = appendU32(out, uint32(len(s.extData)))
	}
	for _, ch := range u {
		out = appendU16(out, ch)
	}
	out = append(out, s.richRuns...)
	out = append(out, s.extData...)
	return out
}

// encodeSST 返回 BIFF 记录数据块（第 0 块写进 SST，其余写进 CONTINUE）。
// v3.1.6：切分点只落在「字符串之间」，绝不把一个字符串拦腰截断 ——
// 这样不需要在 CONTINUE 开头写 option flag，Excel 也不会读错位。
// total 用原 SST 的总引用数（不是 unique），否则 Excel 可能提示修复。
func encodeSST(strs []sstString, total int) [][]byte {
	if total < len(strs) {
		total = len(strs)
	}
	cur := make([]byte, 0, 8224)
	cur = appendU32(cur, uint32(total))
	cur = appendU32(cur, uint32(len(strs))) // unique
	var chunks [][]byte
	flush := func() {
		if len(cur) > 0 {
			chunks = append(chunks, cur)
			cur = make([]byte, 0, 8224)
		}
	}
	for _, s := range strs {
		rec := encodeOneSSTString(s)
		// 单个字符串再长也完整放进一块（BIFF 记录长度是 16 位，实际 rarely 触顶）；
		// 只有「加上下一个字符串会超限」才在字符串边界处切。
		if len(cur)+len(rec) > 8224 && len(cur) > 8 {
			flush()
		}
		cur = append(cur, rec...)
	}
	flush()
	return chunks
}

// replaceXlsxSST 替换 xls 工作簿中的共享字符串（流式重写 BIFF 记录）
func replaceXlsSST(stream []byte, pairs []Pair) ([]byte, int, error) {
	records, err := parseBIFFRecords(stream)
	if err != nil {
		return nil, 0, err
	}
	// 找到 SST 记录及其后的 CONTINUE
	sstIdx := -1
	for i, rec := range records {
		if rec.id == biffSST {
			sstIdx = i
			break
		}
	}
	if sstIdx < 0 {
		return nil, 0, nil // 没有 SST，无需替换
	}
	endIdx := sstIdx + 1
	for endIdx < len(records) && records[endIdx].id == biffContinue {
		endIdx++
	}
	// 解析字符串
	sstRecs := make([][]byte, 0, endIdx-sstIdx)
	for i := sstIdx; i < endIdx; i++ {
		sstRecs = append(sstRecs, records[i].data)
	}
	strs, err := parseSST(sstRecs)
	if err != nil {
		return nil, 0, err
	}

	totalHits := 0
	changed := false
	for i := range strs {
		newText, hits := applyPairs(strs[i].text, pairs)
		if hits > 0 {
			strs[i].text = newText
			totalHits += hits
			changed = true
		}
	}
	if !changed {
		return stream, 0, nil
	}

	// v3.1.6：保留原 SST 的 total（单元格引用总数），旧实现把它写成 unique，
	// Excel 读回时可能提示「文件已损坏」。替换只改文本，引用总数不变。
	origTotal := int(binary.LittleEndian.Uint32(sstRecs[0][0:4]))
	newChunks := encodeSST(strs, origTotal)
	var newRecs []biffRecord
	newRecs = append(newRecs, records[:sstIdx]...)
	for i, chunk := range newChunks {
		id := uint16(biffSST)
		if i > 0 {
			id = biffContinue
		}
		newRecs = append(newRecs, biffRecord{id: id, data: chunk})
	}
	newRecs = append(newRecs, records[endIdx:]...)
	return writeBIFFRecords(newRecs), totalHits, nil
}

type biffRecord struct {
	id   uint16
	data []byte
}

func parseBIFFRecords(stream []byte) ([]biffRecord, error) {
	var recs []biffRecord
	for off := 0; off+4 <= len(stream); {
		id := binary.LittleEndian.Uint16(stream[off : off+2])
		lenv := int(binary.LittleEndian.Uint16(stream[off+2 : off+4]))
		off += 4
		if off+lenv > len(stream) {
			return nil, fmt.Errorf("BIFF 记录 %04X 长度越界", id)
		}
		recs = append(recs, biffRecord{id: id, data: stream[off : off+lenv]})
		off += lenv
		if id == biffEOF {
			break
		}
	}
	return recs, nil
}

func writeBIFFRecords(recs []biffRecord) []byte {
	var out []byte
	for _, r := range recs {
		out = appendU16(out, r.id)
		out = appendU16(out, uint16(len(r.data)))
		out = append(out, r.data...)
	}
	return out
}

func appendU16(b []byte, v uint16) []byte {
	return append(b, byte(v), byte(v>>8))
}

func decodeUTF16(u []uint16) string {
	runes := make([]rune, 0, len(u))
	for i := 0; i < len(u); i++ {
		if u[i] >= 0xD800 && u[i] <= 0xDBFF && i+1 < len(u) && u[i+1] >= 0xDC00 && u[i+1] <= 0xDFFF {
			runes = append(runes, rune(0x10000+((uint32(u[i])-0xD800)<<10)+(uint32(u[i+1])-0xDC00)))
			i++
		} else {
			runes = append(runes, rune(u[i]))
		}
	}
	return string(runes)
}

func encodeUTF16(s string) []uint16 {
	var out []uint16
	for _, r := range s {
		if r > 0xFFFF {
			r -= 0x10000
			out = append(out, uint16(0xD800+(r>>10)), uint16(0xDC00+(r&0x3FF)))
		} else {
			out = append(out, uint16(r))
		}
	}
	return out
}

var _ = strings.Contains

// ReplaceXls 替换 .xls 文件中的共享字符串（纯本地，不依赖 Office）
func ReplaceXls(path string, pairs []Pair) (int, error) {
	entries, err := readAllOLEStreams(path)
	if err != nil {
		return 0, err
	}
	var wb *oleEntry
	for _, e := range entries {
		if e.name == "Workbook" || e.name == "Book" {
			wb = e
			break
		}
	}
	if wb == nil {
		return 0, fmt.Errorf("未找到工作簿流")
	}
	newStream, hits, err := replaceXlsSST(wb.data, pairs)
	if err != nil {
		return 0, err
	}
	if hits == 0 {
		return 0, nil
	}
	wb.data = newStream
	out, err := buildOleContainer(entries)
	if err != nil {
		return 0, err
	}
	if err := os.WriteFile(path, out, 0644); err != nil {
		return 0, err
	}
	return hits, nil
}
