//go:build legacy

package main

// legacy.go —— 旧格式 .doc/.xls 替换（通过本机 Office / WPS 的 COM 后台接口）
//
// 【已停用】v3.1.6 起本文件默认不参与编译（build tag: legacy）。
// engine.go 的 legacy-doc / legacy-xls 走自研 docparse.go / xls.go，
// 全仓没有任何调用点；保留仅为历史参考，需要时 go build -tags legacy。
//
// 标准库无法可靠解析 OLE2 格式的 .doc/.xls，因此借助 Windows 上安装的
// Microsoft Office 或 WPS：用 PowerShell 创建 COM 对象，后台打开文档 →
// 逐组查找替换 → 原格式保存。检测不到任何可用 COM 时返回明确提示并跳过。

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// replaceLegacy 替换 .doc/.xls；返回（结果说明，错误）
func replaceLegacy(path, kind string, pairs []Pair) (string, error) {
	if err := checkWindows(); err != nil {
		return "", err
	}
	pairsJSON, _ := json.Marshal(pairs)
	script := buildLegacyScript(path, kind, string(pairsJSON))
	// 写临时 ps1 文件，避免命令行转义问题
	f, err := os.CreateTemp("", "nf-legacy-*.ps1")
	if err != nil {
		return "", fmt.Errorf("创建临时脚本失败：%w", err)
	}
	scriptPath := f.Name()
	defer os.Remove(scriptPath)
	if _, err := f.WriteString(script); err != nil {
		f.Close()
		return "", err
	}
	f.Close()

	cmd := exec.Command("powershell.exe", "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", scriptPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("调用办公软件失败：%s", strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// buildLegacyScript 生成 PowerShell 脚本：自动探测 Word/Excel 或 WPS 的 COM 组件
func buildLegacyScript(path, kind, pairsJSON string) string {
	isWord := kind == "legacy-doc"
	docVar := "doc"
	progIDs := []string{"Word.Application", "KWps.Application"} // Word / WPS 文字
	closeMethod := "Close"
	if !isWord {
		progIDs = []string{"Excel.Application", "KET.Application"} // Excel / WPS 表格
		docVar = "wb"
		closeMethod = "Close"
	}

	var b strings.Builder
	b.WriteString("$ErrorActionPreference='Stop'\n")
	b.WriteString("$pairs = ConvertFrom-Json '" + strings.ReplaceAll(pairsJSON, "'", "''") + "'\n")
	b.WriteString("$app = $null\n")
	for _, id := range progIDs {
		b.WriteString(fmt.Sprintf("if ($null -eq $app) { try { $app = New-Object -ComObject %s } catch {} }\n", id))
	}
	b.WriteString("if ($null -eq $app) { Write-Output '未安装 Microsoft Office 或 WPS，跳过旧格式文件'; exit 0 }\n")
	b.WriteString("$app.Visible = $false\n")
	b.WriteString("$app.DisplayAlerts = $false\n")
	b.WriteString(fmt.Sprintf("$target = '%s'\n", strings.ReplaceAll(filepath.ToSlash(path), "'", "''")))
	b.WriteString(fmt.Sprintf("$%s = $null\n", docVar))
	if isWord {
		b.WriteString(fmt.Sprintf("try { $%s = $app.Documents.Open($target, $false, $false) } catch { Write-Output ('打开失败：' + $_.Exception.Message); $app.Quit(); exit 0 }\n", docVar))
		b.WriteString("$total = 0\n")
		b.WriteString("foreach ($p in $pairs) {\n")
		b.WriteString("  $find = $doc.Content.Find\n")
		b.WriteString("  $find.ClearFormatting()\n")
		b.WriteString("  $find.Replacement.ClearFormatting()\n")
		b.WriteString("  $find.Text = $p.from\n")
		b.WriteString("  $find.Replacement.Text = $p.to\n")
		b.WriteString("  $find.Forward = $true\n  $find.Wrap = 1\n  $find.Format = $false\n")
		b.WriteString("  $null = $find.Execute([ref]$find.Text, [ref]$find.MatchCase, [ref]$find.MatchWholeWord, [ref]$find.MatchWildcards, [ref]$find.MatchSoundsLike, [ref]$find.MatchAllWordForms, [ref]$find.Forward, [ref]$find.Wrap, [ref]$find.Format, [ref]$find.Replacement.Text, 2)\n")
		b.WriteString("  $total++\n")
		b.WriteString("}\n")
		b.WriteString("$doc.Save()\n")
	} else {
		b.WriteString(fmt.Sprintf("try { $%s = $app.Workbooks.Open($target) } catch { Write-Output ('打开失败：' + $_.Exception.Message); $app.Quit(); exit 0 }\n", docVar))
		b.WriteString("$total = 0\n")
		b.WriteString("foreach ($p in $pairs) {\n")
		b.WriteString("  foreach ($ws in $wb.Worksheets) { $null = $ws.Cells.Replace($p.from, $p.to) }\n")
		b.WriteString("  $total++\n")
		b.WriteString("}\n")
		b.WriteString("$wb.Save()\n")
	}
	b.WriteString(fmt.Sprintf("if ($%s) { $%s.%s() }\n", docVar, docVar, closeMethod))
	b.WriteString("$app.Quit()\n")
	b.WriteString("[System.Runtime.InteropServices.Marshal]::ReleaseComObject($app) | Out-Null\n")
	b.WriteString("Write-Output ('已通过办公软件替换 ' + $total + ' 组规则')\n")
	return b.String()
}

func checkWindows() error {
	if os.PathSeparator != '\\' {
		return fmt.Errorf("旧格式 .doc/.xls 仅在 Windows 上可用（需本机 Office/WPS）")
	}
	return nil
}
