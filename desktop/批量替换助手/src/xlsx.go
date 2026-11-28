
package main

// xlsx.go —— Excel 工作簿内容替换
//
// 覆盖部件：
//   - 共享字符串 xl/sharedStrings.xml（<si><t>，含富文本拆分）
//   - 内联字符串 xl/worksheets/*.xml（<is><t>）
//   - 批注 xl/comments*.xml、线程批注 xl/threadedComments/*.xml
//   - 图表标题/数据标签 xl/charts/*.xml（a:t，DrawingML）
//   - 数据透视表缓存 xl/pivotCache/*.xml
//   - 文档属性 docProps/*.xml
//
// 数值单元格 <v> 不动，以免破坏公式与数字格式。
// 一个共享字符串被多个单元格引用时，替换会同时影响所有引用位置。

import "strings"

// ReplaceXlsx 替换 Excel 系文档中的文字，返回命中次数
func ReplaceXlsx(src, dst string, pairs []Pair) (int, error) {
	return rewriteZipDynamic(src, dst, func(name string) transformFunc {
		n := normalizeEntryName(name)
		switch {
		case n == "xl/sharedstrings.xml":
			return func(s string) (string, int) { return transformSpec(s, pairs, specSheetSI) }

		case strings.HasPrefix(n, "xl/worksheets/") && isXMLPart(n):
			return func(s string) (string, int) { return transformSpec(s, pairs, specSheetIS) }

		// 批注（含线程批注）
		case strings.HasPrefix(n, "xl/comments") && isXMLPart(n),
			strings.HasPrefix(n, "xl/threadedcomments/") && isXMLPart(n):
			return func(s string) (string, int) { return transformSpec(s, pairs, specSheetComment) }

		// 图表
		case strings.HasPrefix(n, "xl/charts/") && isXMLPart(n):
			return func(s string) (string, int) { return transformSpec(s, pairs, specDrawing) }

		// 数据透视表缓存里的字符串项
		case strings.HasPrefix(n, "xl/pivotcache/") && isXMLPart(n):
			return func(s string) (string, int) { return transformSpec(s, pairs, specSheetSI) }

		// 文档属性
		case n == "docprops/core.xml" || n == "docprops/app.xml" || n == "docprops/custom.xml":
			return func(s string) (string, int) { return replaceFlat(s, pairs, rePropText) }
		}
		return nil
	})
}
