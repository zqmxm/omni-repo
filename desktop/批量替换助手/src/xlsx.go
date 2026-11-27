package main

// xlsx.go —— Excel .xlsx 内容替换（纯标准库）
//
// xlsx 文本主要在 xl/sharedStrings.xml 的 <si><t> 里（富文本可能拆成多个 <r><t>），
// 少量在内联字符串 <is><t> 中。数值单元格 <v> 不动，以免破坏公式与数字格式。
// 注意：一个共享字符串可能被多个单元格引用，替换会同时影响所有引用位置。

import (
	"regexp"
	"strings"
)

var (
	reSI      = regexp.MustCompile(`(?s)<si(?:\s[^>]*)?>(.*?)</si>`)
	reIS      = regexp.MustCompile(`(?s)<is(?:\s[^>]*)?>(.*?)</is>`)
	reXlsxTag = regexp.MustCompile(`(?s)(<t(?:\s[^>]*)?>)(.*?)(</t>)`)
)

// ReplaceXlsx 替换 xlsx 中的文字，返回命中次数
func ReplaceXlsx(src, dst string, pairs []Pair) (int, error) {
	return rewriteZipDynamic(src, dst, func(name string) transformFunc {
		n := normalizeEntryName(name)
		if n == "xl/sharedstrings.xml" {
			return func(s string) (string, int) { return transformXlsxContainer(s, pairs, reSI) }
		}
		if strings.HasPrefix(n, "xl/worksheets/") && strings.HasSuffix(n, ".xml") {
			return func(s string) (string, int) { return transformXlsxContainer(s, pairs, reIS) }
		}
		return nil
	})
}

func transformXlsxContainer(content string, pairs []Pair, container *regexp.Regexp) (string, int) {
	total := 0
	var out strings.Builder
	last := 0
	for _, loc := range container.FindAllStringSubmatchIndex(content, -1) {
		wholeStart, wholeEnd := loc[0], loc[1]
		bodyStart, bodyEnd := loc[2], loc[3]
		out.WriteString(content[last:wholeStart])
		newBody, hits := replaceInParagraph(content[bodyStart:bodyEnd], pairs, reXlsxTag, "t")
		total += hits
		out.WriteString(content[wholeStart:bodyStart]) // 保留 <si>/<is> 开标签
		out.WriteString(newBody)
		out.WriteString(content[bodyEnd:wholeEnd])     // 保留闭标签
		last = wholeEnd
	}
	out.WriteString(content[last:])
	return out.String(), total
}
