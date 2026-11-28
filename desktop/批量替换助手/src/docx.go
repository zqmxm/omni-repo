
package main

// docx.go —— Word 文档内容替换
//
// 覆盖部件：
//   - 正文 / 表格 / 文本框 / 页眉页脚 / 批注 / 脚注尾注（w:t）
//   - 图表标题与数据标签、形状文本框（a:t，DrawingML）
//   - SmartArt 图示文字（dgm:t）
//   - 文档属性（dc:title / cp:keywords 等）
//   - 支持 .docx / .docm / .dotx / .dotm
//
// 用正则定位文本节点、保留原始 XML 字节，不做整体序列化，确保文档无损。

import "strings"

// ReplaceDocx 替换 Word 系文档中的文字，返回命中次数
func ReplaceDocx(src, dst string, pairs []Pair) (int, error) {
	return rewriteZipDynamic(src, dst, func(name string) transformFunc {
		n := normalizeEntryName(name)
		switch {
		// WordprocessingML 主文档部件（正文、表格、文本框、页眉页脚、批注、脚注尾注都在 w:p 里）
		case n == "word/document.xml" || n == "word/glossary/document.xml" ||
			n == "word/footnotes.xml" || n == "word/endnotes.xml" ||
			n == "word/comments.xml" || n == "word/comments-extended.xml" ||
			((strings.HasPrefix(n, "word/header") || strings.HasPrefix(n, "word/footer")) && isXMLPart(n)):
			return func(s string) (string, int) { return transformSpec(s, pairs, specWord) }

		// SmartArt 图示数据
		case strings.HasPrefix(n, "word/diagrams/data") && isXMLPart(n):
			return func(s string) (string, int) { return transformSpec(s, pairs, specDiagram) }

		// 图表（chart1.xml 等）——标题、图例、数据标签
		case strings.HasPrefix(n, "word/charts/") && isXMLPart(n):
			return func(s string) (string, int) { return transformSpec(s, pairs, specDrawing) }

		// 文档属性
		case n == "docprops/core.xml" || n == "docprops/app.xml" || n == "docprops/custom.xml":
			return func(s string) (string, int) { return replaceFlat(s, pairs, rePropText) }
		}
		return nil
	})
}
