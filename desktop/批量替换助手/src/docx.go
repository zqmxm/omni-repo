package main

// docx.go —— Word .docx 内容替换（纯标准库）
//
// docx 本质是 ZIP，正文在 word/document.xml，页眉页脚在 word/header*.xml 等。
// 关键难点：一句话常被 Word 拆成多个 run（<w:r><w:t>），比如「北京分公司」可能是
// 「北京」+「分公司」两个 <w:t>。因此按 <w:p> 段落聚合所有 <w:t> 文本后替换，
// 再把结果写回第一个 <w:t>、清空其余，保留 run 的样式属性。
//
// 实现上用正则定位文本节点、保留原始 XML 字节（不做整体序列化），
// 避免 encoding/xml 重新序列化时破坏命名空间/属性，确保文档无损。

import (
	"fmt"
	"html"
	"regexp"
	"strings"
)

var (
	reParagraph = regexp.MustCompile(`(?s)<w:p(?:\s[^>]*)?>(.*?)</w:p>`)
	reWText     = regexp.MustCompile(`(?s)(<w:t(?:\s[^>]*)?>)(.*?)(</w:t>)`)
)

// ReplaceDocx 替换 docx 文档中的文字，返回命中次数
func ReplaceDocx(src, dst string, pairs []Pair) (int, error) {
	return rewriteZipDynamic(src, dst, func(name string) transformFunc {
		n := normalizeEntryName(name)
		if n == "word/document.xml" || n == "word/footnotes.xml" ||
			n == "word/endnotes.xml" || n == "word/comments.xml" {
			return func(s string) (string, int) { return transformDocxXML(s, pairs) }
		}
		if (strings.HasPrefix(n, "word/header") || strings.HasPrefix(n, "word/footer")) && strings.HasSuffix(n, ".xml") {
			return func(s string) (string, int) { return transformDocxFlat(s, pairs) }
		}
		return nil
	})
}

// transformDocxXML 在单个 XML part 内按段落聚合 w:t 并替换
func transformDocxXML(content string, pairs []Pair) (string, int) {
	total := 0
	var out strings.Builder
	last := 0
	for _, loc := range reParagraph.FindAllStringSubmatchIndex(content, -1) {
		wholeStart, wholeEnd := loc[0], loc[1]
		bodyStart, bodyEnd := loc[2], loc[3]
		out.WriteString(content[last:wholeStart])
		para := content[bodyStart:bodyEnd]
		newPara, hits := replaceInParagraph(para, pairs, reWText, "w:t")
		total += hits
		out.WriteString(content[wholeStart:bodyStart]) // 保留 <w:p ...> 开标签
		out.WriteString(newPara)
		out.WriteString(content[bodyEnd:wholeEnd])     // 保留 </w:p> 闭标签
		last = wholeEnd
	}
	out.WriteString(content[last:])
	return out.String(), total
}

// replaceInParagraph 聚合段落/容器内所有文本节点，替换后写回第一个节点，清空其余
func replaceInParagraph(body string, pairs []Pair, re *regexp.Regexp, tag string) (string, int) {
	locs := re.FindAllStringSubmatchIndex(body, -1)
	if len(locs) == 0 {
		return body, 0
	}
	var texts []string
	for _, loc := range locs {
		texts = append(texts, xmlUnescape(body[loc[4]:loc[5]]))
	}
	combined := strings.Join(texts, "")
	newText, hits := applyPairs(combined, pairs)
	if hits == 0 {
		return body, 0
	}

	var sb strings.Builder
	last := 0
	for i, loc := range locs {
		sb.WriteString(body[last:loc[0]])
		openTag := body[loc[2]:loc[3]]
		if i == 0 {
			// 写回替换后的完整文本，并确保 xml:space="preserve"
			if !strings.Contains(openTag, "xml:space") {
				openTag = strings.TrimSuffix(openTag, ">") + ` xml:space="preserve">`
			}
			sb.WriteString(openTag)
			sb.WriteString(xmlEscape(newText))
		} else {
			// 其余节点清空内容，保留标签与样式
			sb.WriteString(openTag)
		}
		sb.WriteString(body[loc[6]:loc[7]])
		last = loc[1]
	}
	sb.WriteString(body[last:])
	return sb.String(), hits
}

// xmlEscape / xmlUnescape 处理 XML 实体（document 文本里常见 &amp; &lt; 等）
func xmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return r.Replace(s)
}

func xmlUnescape(s string) string {
	// html.UnescapeString 同时处理命名实体与数字字符实体（如 &#21271;）
	return html.UnescapeString(s)
}

var _ = fmt.Sprintf


// transformDocxFlat 无 <w:p> 结构时直接聚合全部 <w:t> 替换（用于页眉页脚样本）
func transformDocxFlat(content string, pairs []Pair) (string, int) {
	locs := reWText.FindAllStringSubmatchIndex(content, -1)
	if len(locs) == 0 { return content, 0 }
	var texts []string
	for _, loc := range locs {
		texts = append(texts, xmlUnescape(content[loc[4]:loc[5]]))
	}
	combined := strings.Join(texts, "")
	newText, hits := applyPairs(combined, pairs)
	if hits == 0 { return content, 0 }
	var out strings.Builder
	last := 0
	for i, loc := range locs {
		out.WriteString(content[last:loc[0]])
		openTag := content[loc[2]:loc[3]]
		if i == 0 {
			if !strings.Contains(openTag, "xml:space") {
				openTag = strings.TrimSuffix(openTag, ">") + ` xml:space="preserve">`
			}
			out.WriteString(openTag)
			out.WriteString(xmlEscape(newText))
		} else {
			out.WriteString(openTag)
		}
		out.WriteString(content[loc[6]:loc[7]])
		last = loc[1]
	}
	out.WriteString(content[last:])
	return out.String(), hits
}
