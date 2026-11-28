
package main

// ooxml.go —— 通用 OOXML 文本替换引擎（零第三方依赖）
//
// 设计要点：
//  1. 按"容器"聚合文本节点后替换，解决 Office 把一句话拆成多个 run 的问题
//  2. 只改写文本节点、保留其余原始 XML 字节，避免重新序列化破坏命名空间
//  3. 一套引擎支持多种 XML 方言：WordprocessingML、DrawingML、SpreadsheetML、SmartArt
//  4. 容器里若没有匹配内容，原样返回，不产生任何字节改动

import (
	"html"
	"regexp"
	"strings"
)

// textSpec 描述一种 XML 文本方言的容器与文本节点
type textSpec struct {
	container *regexp.Regexp // 容器（段落/单元格/字符串项），第 1 个捕获组是内容
	text      *regexp.Regexp // 文本节点：组2=开标签，组3=文本，组4=闭标签
	label     string         // 文本节点名称，用于 xml:space 处理
}

func newTextSpec(container, text, label string) textSpec {
	return textSpec{
		container: regexp.MustCompile(container),
		text:      regexp.MustCompile(text),
		label:     label,
	}
}

var (
	// WordprocessingML: <w:p><w:r><w:t>…</w:t></w:r></w:p>
	// 正文、表格、文本框(txbxContent)、页眉页脚、批注、脚注尾注都使用 w:p
	specWord = newTextSpec(
		`(?s)<w:p(?:\s[^>]*)?>(.*?)</w:p>`,
		`(?s)(<w:t(?:\s[^>]*)?>)(.*?)(</w:t>)`,
		"w:t",
	)

	// DrawingML: <a:p><a:r><a:t>…</a:t></a:r></a:p>
	// 图表标题/数据标签、形状文本框、主题中的文字
	specDrawing = newTextSpec(
		`(?s)<a:p(?:\s[^>]*)?>(.*?)</a:p>`,
		`(?s)(<a:t(?:\s[^>]*)?>)(.*?)(</a:t>)`,
		"a:t",
	)

	// SpreadsheetML 共享字符串: <si><t>…</t></si>
	specSheetSI = newTextSpec(
		`(?s)<si(?:\s[^>]*)?>(.*?)</si>`,
		`(?s)(<t(?:\s[^>]*)?>)(.*?)(</t>)`,
		"t",
	)

	// SpreadsheetML 内联字符串: <is><t>…</t></is>
	specSheetIS = newTextSpec(
		`(?s)<is(?:\s[^>]*)?>(.*?)</is>`,
		`(?s)(<t(?:\s[^>]*)?>)(.*?)(</t>)`,
		"t",
	)

	// SpreadsheetML 批注: <comment><text><t>…</t></text></comment>
	specSheetComment = newTextSpec(
		`(?s)<comment(?:\s[^>]*)?>(.*?)</comment>`,
		`(?s)(<t(?:\s[^>]*)?>)(.*?)(</t>)`,
		"t",
	)

	// SmartArt: <dgm:pt><dgm:t>…</dgm:t></dgm:pt>
	specDiagram = newTextSpec(
		`(?s)<dgm:pt(?:\s[^>]*)?>(.*?)</dgm:pt>`,
		`(?s)(<dgm:t(?:\s[^>]*)?>)(.*?)(</dgm:t>)`,
		"dgm:t",
	)

	// 文档属性: <dc:title>…</dc:title>、<cp:keywords>…</cp:keywords> 等
	// 这些元素各自独立，不需要段落聚合
	rePropText = regexp.MustCompile(`(?s)(<(?:dc|cp):[A-Za-z]+(?:\s[^>]*)?>)(.*?)(</(?:dc|cp):[A-Za-z]+>)`)
)

// transformSpec 按 spec 的容器结构聚合文本节点并替换
func transformSpec(content string, pairs []Pair, spec textSpec) (string, int) {
	total := 0
	var out strings.Builder
	last := 0
	for _, loc := range spec.container.FindAllStringSubmatchIndex(content, -1) {
		wholeStart, wholeEnd := loc[0], loc[1]
		bodyStart, bodyEnd := loc[2], loc[3]
		out.WriteString(content[last:wholeStart])
		body := content[bodyStart:bodyEnd]
		newBody, hits := replaceInContainer(body, pairs, spec.text)
		total += hits
		out.WriteString(content[wholeStart:bodyStart]) // 保留容器开标签
		out.WriteString(newBody)
		out.WriteString(content[bodyEnd:wholeEnd]) // 保留容器闭标签
		last = wholeEnd
	}
	out.WriteString(content[last:])
	return out.String(), total
}

// replaceInContainer 聚合容器内所有文本节点，替换后写回第一个节点、清空其余。
// 这样即使一句话被拆进多个 run，也能整句匹配；样式标签原样保留。
func replaceInContainer(body string, pairs []Pair, re *regexp.Regexp) (string, int) {
	locs := re.FindAllStringSubmatchIndex(body, -1)
	if len(locs) == 0 {
		return body, 0
	}
	texts := make([]string, 0, len(locs))
	for _, loc := range locs {
		texts = append(texts, html.UnescapeString(body[loc[4]:loc[5]]))
	}
	combined := strings.Join(texts, "")
	newText, hits := applyPairs(combined, pairs)
	if hits == 0 {
		return body, 0
	}

	var sb strings.Builder
	sb.Grow(len(body) + len(newText))
	last := 0
	for i, loc := range locs {
		sb.WriteString(body[last:loc[0]])
		openTag := body[loc[2]:loc[3]]
		if i == 0 {
			// 写回完整替换结果，并确保 xml:space="preserve" 保留首尾空格
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

// replaceFlat 对每个文本节点独立替换，不做跨节点聚合（用于文档属性等）
func replaceFlat(content string, pairs []Pair, re *regexp.Regexp) (string, int) {
	total := 0
	var out strings.Builder
	last := 0
	for _, loc := range re.FindAllStringSubmatchIndex(content, -1) {
		old := html.UnescapeString(content[loc[4]:loc[5]])
		newText, hits := applyPairs(old, pairs)
		out.WriteString(content[last:loc[0]])
		out.WriteString(content[loc[2]:loc[3]])
		if hits > 0 {
			out.WriteString(xmlEscape(newText))
			total += hits
		} else {
			out.WriteString(content[loc[4]:loc[5]])
		}
		out.WriteString(content[loc[6]:loc[7]])
		last = loc[1]
	}
	out.WriteString(content[last:])
	return out.String(), total
}

// xmlEscape / xmlUnescape 处理 XML 实体（&amp; &lt; &#21271; 等）
func xmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return r.Replace(s)
}

func xmlUnescape(s string) string {
	// html.UnescapeString 同时处理命名实体与数字字符实体
	return html.UnescapeString(s)
}

// isXMLPart 判断是否为需要处理的 XML 部件（排除关系文件等）
func isXMLPart(name string) bool {
	return strings.HasSuffix(name, ".xml") && !strings.Contains(name, "_rels/")
}
