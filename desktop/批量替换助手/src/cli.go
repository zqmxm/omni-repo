
package main

// cli.go —— 跨平台命令行入口（Windows / Linux / macOS 通用，纯标准库）
//
// 同一个源码树：
//   - 不带参数双击运行 → Windows 原生桌面窗口（GUI）
//   - 带任意命令行参数 → CLI 模式
// 构建控制台程序时加不加 --cli 都可以，--cli 只是显式标记。

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const appVersion = "3.1.1"

type ruleList []Pair

func (r *ruleList) String() string {
	parts := make([]string, 0, len(*r))
	for _, p := range *r {
		parts = append(parts, p.From+"="+p.To)
	}
	return strings.Join(parts, ",")
}

func (r *ruleList) Set(v string) error {
	i := strings.Index(v, "=")
	if i < 1 {
		return fmt.Errorf("规则格式应为 原内容=替换内容，收到：%q", v)
	}
	*r = append(*r, Pair{From: v[:i], To: v[i+1:]})
	return nil
}

type cliResult struct {
	Version  string `json:"version"`
	Done     int    `json:"done"`
	Skipped  int    `json:"skipped"`
	Failed   int    `json:"failed"`
	Undoable bool   `json:"undoable"`
	Items    []Item `json:"items"`
	Error    string `json:"error,omitempty"`
}

func runCLI(args []string) int {
	fs := flag.NewFlagSet("batchreplace", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	var rules ruleList
	fs.Var(&rules, "rule", "替换规则 原内容=替换内容，可重复指定；替换内容可包含 = 号")
	dir := fs.String("dir", "", "要处理的文件夹")
	rulesJSON := fs.String("rules", "", `规则 JSON，如 [{"from":"A","to":"B"}]`)
	rulesFile := fs.String("rules-file", "", "从文件读规则：每行 A=B，或 JSON 数组")
	saveAs := fs.String("save-as", "", "另存目录：不改动原文件，结果写到该目录（不指定即在原文件上改）")
	overwrite := fs.Bool("overwrite", false, "另存时覆盖已存在的同名文件（默认跳过）")
	noRecursive := fs.Bool("no-recursive", false, "不处理子文件夹（默认处理）")
	noContent := fs.Bool("no-content", false, "不替换文档内容")
	rename := fs.Bool("rename", false, "同时替换文件名")
	dryRun := fs.Bool("dry-run", false, "只预览会改动哪些文件，不写入")
	undo := fs.Bool("undo", false, "撤销上一次替换（可连续执行多次）")
	state := fs.String("state", "", "撤销记录路径（默认在程序目录）")
	jsonOut := fs.Bool("json", false, "以 JSON 输出结果")
	showVersion := fs.Bool("version", false, "显示版本")
	help := fs.Bool("help", false, "显示帮助")
	// 兼容显式标记
	_ = fs.Bool("cli", false, "显式启用命令行模式（带参数时自动启用）")

	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *help || (len(args) == 1 && (args[0] == "--cli" || args[0] == "-cli")) {
		printHelp()
		return 0
	}
	if *showVersion {
		fmt.Println("批量替换助手 CLI v" + appVersion)
		return 0
	}
	if *undo {
		return doUndo(*state, *jsonOut)
	}

	pairs := []Pair(rules)
	if *rulesJSON != "" {
		more, err := parseRulesJSON(*rulesJSON)
		if err != nil {
			return cliFail(*jsonOut, "解析 --rules 失败："+err.Error())
		}
		pairs = append(pairs, more...)
	}
	if *rulesFile != "" {
		more, err := loadRulesFile(*rulesFile)
		if err != nil {
			return cliFail(*jsonOut, "读取规则文件失败："+err.Error())
		}
		pairs = append(pairs, more...)
	}

	req := ReplaceReq{
		Dir:            *dir,
		Pairs:          pairs,
		Recursive:      !*noRecursive,
		ReplaceContent: !*noContent,
		RenameFiles:    *rename,
		SaveAs:         *saveAs,
		Overwrite:      *overwrite,
		DryRun:         *dryRun,
		StatePath:      *state,
	}

	items := make([]Item, 0, 32)
	stats := ExecuteEx(req, func(it Item) {
		items = append(items, it)
		if !*jsonOut {
			fmt.Println(formatItem(it))
		}
	})

	res := cliResult{
		Version:  appVersion,
		Done:     stats.Done,
		Skipped:  stats.Skipped,
		Failed:   stats.Failed,
		Undoable: !*dryRun && HasUndo(*state),
		Items:    items,
	}
	if *jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(res)
	} else {
		fmt.Printf("\n完成：成功 %d 项，跳过 %d 项，失败 %d 项", stats.Done, stats.Skipped, stats.Failed)
		if *dryRun {
			fmt.Print("（预览模式，未写入任何文件）")
		}
		fmt.Println()
		if req.SaveAs != "" {
			fmt.Println("输出目录：" + req.SaveAs)
		}
		if res.Undoable {
			fmt.Println("可用 --undo 撤销本次操作")
		}
	}
	if stats.Failed > 0 {
		return 1
	}
	return 0
}

func cliFail(jsonOut bool, msg string) int {
	if jsonOut {
		_ = json.NewEncoder(os.Stdout).Encode(cliResult{Version: appVersion, Error: msg})
	} else {
		fmt.Fprintln(os.Stderr, "✖ "+msg)
	}
	return 1
}

func formatItem(it Item) string {
	prefix := "⏭ "
	switch it.Status {
	case "ok":
		prefix = "✔ "
	case "failed":
		prefix = "✖ "
	}
	line := prefix + it.Kind + "  " + it.Message
	if it.Kind == "rename" && it.FromName != "" {
		line = prefix + "改名  " + it.FromName + " → " + it.ToName + "（" + it.Message + "）"
	}
	if it.Path != "" {
		line += "  [" + filepath.Base(it.Path) + "]"
	}
	return line
}

func doUndo(state string, jsonOut bool) int {
	results, opTime, err := UndoLast(state)
	if err != nil {
		return cliFail(jsonOut, "撤销失败："+err.Error())
	}
	if jsonOut {
		_ = json.NewEncoder(os.Stdout).Encode(map[string]interface{}{
			"version": appVersion, "undoneAt": opTime, "results": results,
			"undoable": HasUndo(state),
		})
		return 0
	}
	okCount := 0
	for _, r := range results {
		if r.OK {
			okCount++
			fmt.Println("✔ " + r.Message)
		} else {
			fmt.Println("⏭ " + r.Message)
		}
	}
	fmt.Printf("已撤销 %s 的操作：成功 %d 项，跳过 %d 项\n", opTime, okCount, len(results)-okCount)
	if HasUndo(state) {
		fmt.Println("仍有更早的操作可继续撤销")
	}
	return 0
}

func parseRulesJSON(s string) ([]Pair, error) {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "{") {
		var wrapper struct {
			Pairs []Pair `json:"pairs"`
		}
		if err := json.Unmarshal([]byte(s), &wrapper); err != nil {
			return nil, err
		}
		return wrapper.Pairs, nil
	}
	var pairs []Pair
	if err := json.Unmarshal([]byte(s), &pairs); err != nil {
		return nil, err
	}
	return pairs, nil
}

func loadRulesFile(path string) ([]Pair, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	s := strings.TrimSpace(string(data))
	if strings.HasPrefix(s, "[") || strings.HasPrefix(s, "{") {
		return parseRulesJSON(s)
	}
	var pairs []Pair
	sc := bufio.NewScanner(strings.NewReader(s))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "//") {
			continue
		}
		i := strings.Index(line, "=")
		if i < 1 {
			return nil, fmt.Errorf("第 %d 行格式错误（应为 原内容=替换内容）：%s", len(pairs)+1, line)
		}
		pairs = append(pairs, Pair{From: line[:i], To: line[i+1:]})
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return pairs, nil
}

func printHelp() {
	fmt.Printf(`批量替换助手 CLI v%s —— 纯本地、无需 Office/WPS

用法：
  batchreplace --dir <文件夹> --rule "原内容=替换内容" [选项]
  batchreplace --undo [选项]

规则：
  --rule "A=B"         替换规则，可重复指定；规则按顺序链式生效
  --rules '<JSON>'     以 JSON 数组给规则，如 [{"from":"A","to":"B"}]
  --rules-file <文件>  从文件读规则：每行 A=B（# 开头为注释），或 JSON 数组

输出方式（二选一）：
  （不指定）           原地替换，直接改源文件
  --save-as <目录>     另存：源文件完全不动，结果写到该目录并保持子目录结构
  --overwrite          另存时允许覆盖目标目录里已存在的同名文件（默认跳过）

选项：
  --rename             同时替换文件名
  --no-content         不替换文档内容（配合 --rename 使用）
  --no-recursive       不处理子文件夹（默认处理）
  --dry-run            只预览会改动哪些文件，不写入、不生成撤销记录
  --undo               撤销上一次替换，可连续执行多次（最多保留 10 步）
  --state <路径>       指定撤销记录文件位置（便于脚本隔离）
  --json               以 JSON 输出结果
  --version            显示版本
  --help               显示本帮助

示例：
  # 原地替换，直接改源文件
  batchreplace --dir "D:\合同" --rule "北京=上海" --rule "2025=2026" --rename

  # 另存：原文件一个都不动，输出到新目录
  batchreplace --dir "D:\合同" --save-as "D:\合同_新版" --rule "北京=上海" --rename

  # 先看会改什么，再决定
  batchreplace --dir "D:\合同" --rule "北京=上海" --dry-run

  # 改错了，撤销
  batchreplace --undo

支持格式：.docx/.docm/.dotx/.dotm、.xlsx/.xlsm/.xltx/.xltm、.doc、.xls
`, appVersion)
}
