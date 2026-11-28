
package main

// docparse.go —— .doc (Word Binary / MS-DOC) 文本提取与替换
//
// 纯 Go 实现，不依赖 Office/WPS。
// 通过 OLE2 容器读取 WordDocument 流与表格流，解析 FIB 中的 fcClx/lcbClx，
// 再按 CLX piece table 把分片文本拼成完整正文。
//
// 替换策略：文本长度变化会使 piece table 里的绝对 FC 偏移全部失效，
// 因此采用"重写全文 + 重建单一 piece"的方案：
//  1. 提取全文并替换
//  2. 把新文本以 UTF-16LE 追加到 WordDocument 流末尾
//  3. 用单个 piece 覆盖整个正文，重写 CLX
//  4. 更新 FIB 的 fcMin/fcMac/ccpText

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf16"

	"piliangtihuanzhushou/internal/mscfb"
)

type docPiece struct {
	cpStart    uint32
	cpEnd      uint32
	fc         uint32
	compressed bool
}

type docInfo struct {
	word      []byte
	table     []byte
	tableName string
	pieces    []docPiece
	clxOffset uint32
	clxLen    uint32
	fibFlags  uint16
	ccpText   uint32
}

// readOLEStreams 读取 OLE2 容器中的指定流
func readOLEStreams(path string, wanted map[string]bool) (map[string][]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r, err := mscfb.New(f)
	if err != nil {
		return nil, fmt.Errorf("不是有效的 OLE2 文档：%w", err)
	}
	out := make(map[string][]byte)
	for {
		e, err := r.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if wanted[e.Name] {
			data, err := io.ReadAll(e)
			if err != nil {
				return nil, err
			}
			out[e.Name] = data
		}
	}
	return out, nil
}

// parseDocInfo 解析 .doc 的 FIB 与 piece table
func parseDocInfo(path string) (*docInfo, error) {
	streams, err := readOLEStreams(path, map[string]bool{
		"WordDocument": true, "0Table": true, "1Table": true,
	})
	if err != nil {
		return nil, err
	}
	wd, ok := streams["WordDocument"]
	if !ok || len(wd) < 512 {
		return nil, fmt.Errorf("缺少 WordDocument 流")
	}
	if le16(wd, 0) != 0xA5EC {
		return nil, fmt.Errorf("不是 Word Binary 文档（wIdent=0x%04X）", le16(wd, 0))
	}

	info := &docInfo{word: wd}
	info.fibFlags = le16(wd, 10)
	info.ccpText = le32(wd, 64+4) // fibRgLw97[1] = ccpText（cbMac 之后）

	// 选择表格流：优先按 fWhichTblStm 标志，失败时自动探测
	// WPS 常写错该标志，因此用内容探测兜底
	candidates := []string{"1Table", "0Table"}
	if info.fibFlags&0x0200 == 0 {
		candidates = []string{"0Table", "1Table"}
	}
	for _, name := range candidates {
		tb, ok := streams[name]
		if !ok {
			continue
		}
		clxOff := le32(wd, 154+33*8)
		clxLen := le32(wd, 154+33*8+4)
		if clxOff+clxLen > uint32(len(tb)) || clxLen == 0 {
			continue
		}
		pieces, err := parseCLX(tb[clxOff : clxOff+clxLen])
		if err != nil || len(pieces) == 0 {
			continue
		}
		info.table = tb
		info.tableName = name
		info.clxOffset = clxOff
		info.clxLen = clxLen
		info.pieces = pieces
		break
	}
	if info.table == nil {
		return nil, fmt.Errorf("无法定位有效的 CLX 分片表")
	}
	return info, nil
}

// parseCLX 解析 CLX → Pcdt → PlcPcd，返回所有分片
func parseCLX(clx []byte) ([]docPiece, error) {
	pos := 0
	for pos < len(clx) {
		// CLX = RgPrc(clxt=0x01...) + Pcdt(clxt=0x02...)
		if clx[pos] == 0x01 {
			if pos+3 > len(clx) {
				return nil, fmt.Errorf("RgPrc 截断")
			}
			cb := int(int16(le16(clx, pos+1)))
			pos += 3 + cb
			continue
		}
		if clx[pos] != 0x02 {
			return nil, fmt.Errorf("未知 clxt=0x%02X", clx[pos])
		}
		if pos+5 > len(clx) {
			return nil, fmt.Errorf("Pcdt 截断")
		}
		lcb := int(le32(clx, pos+1))
		plc := clx[pos+5 : pos+5+lcb]
		n := (len(plc) - 4) / 12 // (n+1)*4 + n*8
		if n <= 0 {
			return nil, fmt.Errorf("PlcPcd 分片数为 0")
		}
		pieces := make([]docPiece, 0, n)
		for i := 0; i < n; i++ {
			cp0 := le32(plc, i*4)
			cp1 := le32(plc, (i+1)*4)
			pcd := (n+1)*4 + i*8
			fcRaw := le32(plc, pcd+2)
			pieces = append(pieces, docPiece{
				cpStart: cp0, cpEnd: cp1,
				fc:         fcRaw & 0x3FFFFFFF,
				compressed: fcRaw&0x40000000 != 0,
			})
		}
		return pieces, nil
	}
	return nil, fmt.Errorf("未找到 Pcdt")
}

// extractText 按 piece table 提取正文文本（CP 0..ccpText）
func extractText(info *docInfo) (string, error) {
	var b strings.Builder
	for _, p := range info.pieces {
		if p.cpStart >= info.ccpText {
			break
		}
		end := p.cpEnd
		if end > info.ccpText {
			end = info.ccpText
		}
		count := int(end - p.cpStart)
		if count <= 0 {
			continue
		}
		if p.compressed {
			off := int(p.fc / 2)
			if off+count > len(info.word) {
				return "", fmt.Errorf("分片超出 WordDocument 流")
			}
			// cp1252 解码
			b.WriteString(cp1252ToString(info.word[off : off+count]))
		} else {
			off := int(p.fc)
			if off+count*2 > len(info.word) {
				return "", fmt.Errorf("分片超出 WordDocument 流")
			}
			u := make([]uint16, count)
			for i := 0; i < count; i++ {
				u[i] = le16(info.word, off+i*2)
			}
			b.WriteString(string(utf16.Decode(u)))
		}
	}
	return b.String(), nil
}

// cp1252ToString 将 cp1252/ANSI 单字节串转为 Go 字符串
func cp1252ToString(b []byte) string {
	runes := make([]rune, 0, len(b))
	for _, c := range b {
		if c < 0x80 {
			runes = append(runes, rune(c))
		} else {
			runes = append(runes, rune(c)) // 简化：高位字节直映，中文文本通常为 Unicode 分片
		}
	}
	return string(runes)
}

func le16(b []byte, off int) uint16 {
	return binary.LittleEndian.Uint16(b[off : off+2])
}

func le32(b []byte, off int) uint32 {
	return binary.LittleEndian.Uint32(b[off : off+4])
}

// replaceDocText 替换 .doc 正文文本，写回源文件。
// 策略：重写全文并以 UTF-16LE 追加到 WordDocument 流末尾，重建 CLX 单一分片，
// 更新 FIB 的 fcMin/fcMac。原流的其他部分（FIB、FKP、SEPX 等）保留不动。
func replaceDocText(path string, pairs []Pair) (int, error) {
	info, err := parseDocInfo(path)
	if err != nil {
		return 0, err
	}
	text, err := extractText(info)
	if err != nil {
		return 0, err
	}
	newText, hits := applyPairs(text, pairs)
	if hits == 0 {
		return 0, nil
	}

	// 新文本以 UTF-16LE 编码
	var buf strings.Builder
	for _, r := range newText {
		if r > 0xFFFF {
			// 代理对
			r2 := r - 0x10000
			buf.WriteByte(byte((0xD800 + (r2 >> 10)) & 0xFF))
			buf.WriteByte(byte((0xD800 + (r2 >> 10)) >> 8))
			buf.WriteByte(byte((0xDC00 + (r2 & 0x3FF)) & 0xFF))
			buf.WriteByte(byte((0xDC00 + (r2 & 0x3FF)) >> 8))
		} else {
			buf.WriteByte(byte(r & 0xFF))
			buf.WriteByte(byte(r >> 8))
		}
	}
	newBytes := []byte(buf.String())
	utf16Len := len([]rune(newText))

	// 追加到 WordDocument 流末尾（对齐到 2 字节）
	wd := info.word
	appendAt := len(wd)
	if appendAt%2 != 0 {
		wd = append(wd, 0)
		appendAt++
	}
	wd = append(wd, newBytes...)

	// 更新 FIB: fcMin/fcMac（正文区间指向新文本）
	binary.LittleEndian.PutUint32(wd[24:28], uint32(appendAt))
	binary.LittleEndian.PutUint32(wd[28:32], uint32(appendAt+len(newBytes)))
	// ccpText = fibRgLw97[1]，偏移 64+4
	binary.LittleEndian.PutUint32(wd[68:72], uint32(utf16Len))

	// 重建 CLX：清空 RgPrc，仅保留一个 Pcdt + PlcPcd（1 个分片）
	newCLX := buildSinglePieceCLX(uint32(utf16Len), uint32(appendAt))
	// 替换表格流中的 CLX 区域
	tb := append([]byte(nil), info.table...)
	oldEnd := int(info.clxOffset + info.clxLen)
	if oldEnd > len(tb) {
		return 0, fmt.Errorf("CLX 区域越界")
	}
	newTB := make([]byte, 0, len(tb)-len(newCLX)+len(newCLX))
	newTB = append(newTB, tb[:info.clxOffset]...)
	newTB = append(newTB, newCLX...)
	newTB = append(newTB, tb[oldEnd:]...)

	// 组装所有流并写回
	entries, err := readAllOLEStreams(path)
	if err != nil {
		return 0, err
	}
	for _, e := range entries {
		switch e.name {
		case "WordDocument":
			e.data = wd
		case info.tableName:
			e.data = newTB
		}
	}
	data, err := buildOleContainer(entries)
	if err != nil {
		return 0, err
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return 0, err
	}
	return hits, nil
}

// buildSinglePieceCLX 构造覆盖整篇正文的单分片 CLX
func buildSinglePieceCLX(ccp uint32, fc uint32) []byte {
	// Pcdt: clxt(1)=0x02, lcb(4), PlcPcd = cp[0],cp[1] (8B) + PCD(8B)
	plcLen := 4*2 + 8 // 16
	clx := make([]byte, 0, 1+4+plcLen)
	clx = append(clx, 0x02)
	clx = appendU32(clx, uint32(plcLen))
	clx = appendU32(clx, 0)   // cp[0]
	clx = appendU32(clx, ccp) // cp[1]
	// PCD: 2 字节 flags(0) + fc(4, 未压缩 bit30=0) + prm(2)
	clx = append(clx, 0, 0)
	clx = appendU32(clx, fc)
	clx = append(clx, 0, 0)
	return clx
}

func appendU32(b []byte, v uint32) []byte {
	return append(b, byte(v), byte(v>>8), byte(v>>16), byte(v>>24))
}
