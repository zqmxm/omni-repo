package main

// local.go —— 本地智能引擎（无需任何 API，完全离线）
//
// 用「中文指令解析 + 元素语义匹配」替代云端大模型：
//   1. 把一句中文指令按动词拆成有序意图（打开 / 搜索 / 输入 / 点击 / 下载 / 等待 / 完成）
//   2. 每一步把意图目标与页面元素做语义匹配（精确 → 包含 → 同义词 → 字符二元组相似度）
//   3. 选出最匹配的元素执行，执行后重新观察，直到意图全部完成
//
// 优点：零成本、零联网、毫秒级响应、结果可预期；适合结构清晰的常规自动化任务。
// 复杂推理任务仍可在设置里切换到云端多模态模型。

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// ---------- 意图 ----------

type intent struct {
	Type    string // open | input | click | download | wait | finish
	Target  string // 元素目标描述（可为空，自动选择）
	Text    string // 输入文本
	URL     string
	Enter   bool
	Seconds int
}

var (
	reVerb   = regexp.MustCompile(`打开|搜索|检索|查询|输入|填写|填入|点击|按下|单击|勾选|选中|选择|下载|等待|等`)
	reURL    = regexp.MustCompile(`https?://[^\s，。；,、“”"'「」『』）)]+|www\.[^\s，。；,、“”"'「」『』）)]+|[a-zA-Z0-9-]+\.(?:com|cn|net|org|io|dev|top|xyz|cc|me|info|app|ai|gov|edu|co|tv|site|online|shop|vip|work|pro|link|club|mobi|tech|store)(?:/[\w\-./?%=&#]*)?`)
	reQuoted = regexp.MustCompile(`[「『“"']([^」』”"']+)[」』”"']`)
	reNum    = regexp.MustCompile(`\d+`)
	reTail   = regexp.MustCompile(`^(.*?)(?:然后|接着|随后|之后|并且|并|再|和|最后|同时|即可|去|来)*$`)
	reFile   = regexp.MustCompile(`\.(zip|rar|7z|pdf|docx?|xlsx?|pptx?|exe|msi|apk|mp3|mp4|jpg|jpeg|png|gif|csv|txt)(\?|$)`)
)

var targetTrim = strings.NewReplacer("按钮", "", "链接", "", "图标", "", "一下", "", "进去", "", "这个", "", "那个", "", "那个", "")

// cleanTarget 去掉目标描述里的修饰词，只留核心名称
func cleanTarget(s string) string {
	s = strings.TrimSpace(s)
	if m := reTail.FindStringSubmatch(s); m != nil {
		s = strings.TrimSpace(m[1])
	}
	s = strings.Trim(s, "，。；、,.;:：!！?？（）()「」『』“”\"' ")
	s = targetTrim.Replace(s)
	// 去掉开头的介词（到 / 在 / 把 / 将），只去一个
	for _, p := range []string{"到", "在", "把", "将"} {
		if strings.HasPrefix(s, p) && len([]rune(s)) > 1 {
			s = strings.TrimPrefix(s, p)
			break
		}
	}
	return strings.TrimSpace(s)
}

// parseInstruction 把一句中文指令拆成有序意图列表
func parseInstruction(s string) []intent {
	var out []intent
	matches := reVerb.FindAllStringSubmatchIndex(s, -1)
	if len(matches) == 0 {
		return []intent{{Type: "fail"}}
	}
	// “点击搜索 / 按下登录”这类：前一个动词是点击类且中间没有别的内容，则本词只是点击目标
	skip := make([]bool, len(matches))
	for i := 1; i < len(matches); i++ {
		prev := matches[i-1]
		if strings.Contains("点击按下单击勾选选中选择", s[prev[0]:prev[1]]) && strings.TrimSpace(s[prev[1]:matches[i][0]]) == "" {
			skip[i] = true
		}
	}
	fileURLHandled := false
	for i, loc := range matches {
		if skip[i] {
			continue
		}
		verb := s[loc[0]:loc[1]]
		end := len(s)
		for j := i + 1; j < len(matches); j++ {
			if !skip[j] {
				end = matches[j][0]
				break
			}
		}
		seg := strings.TrimSpace(s[loc[1]:end])
		if m := reQuoted.FindStringSubmatch(seg); m != nil {
			seg = m[1]
		}
		target := cleanTarget(seg)
		url := ""
		if m := reURL.FindString(seg); m != "" {
			url = strings.TrimRight(m, "，。；、)）")
			if !strings.HasPrefix(url, "http") {
				url = "https://" + url
			}
		}

		switch verb {
		case "打开":
			if url != "" {
				if reFile.MatchString(strings.ToLower(url)) && strings.Contains(s, "下载") {
					out = append(out, intent{Type: "download", URL: url, Seconds: 60})
					fileURLHandled = true
				} else {
					out = append(out, intent{Type: "open", URL: url})
				}
			} else if target != "" {
				// “打开某某网站”：没给网址就按关键词搜索
				out = append(out, intent{Type: "open", URL: "https://www.bing.com/search?q=" + urlQuery(target)})
			}
		case "搜索", "检索", "查询":
			out = append(out, intent{Type: "input", Target: "搜索", Text: target, Enter: true})
		case "输入", "填写", "填入":
			out = append(out, intent{Type: "input", Text: seg, Enter: strings.Contains(seg, "回车") || strings.Contains(strings.ToLower(seg), "enter")})
		case "点击", "按下", "单击", "勾选", "选中", "选择":
			if target != "" {
				out = append(out, intent{Type: "click", Target: target})
			}
		case "下载":
			switch {
			case url != "":
				out = append(out, intent{Type: "download", URL: url, Seconds: 60})
			case target != "":
				out = append(out, intent{Type: "download", Target: target})
			case fileURLHandled:
				// 文件链接已经由“打开 xxx.zip”处理过了
			default:
				out = append(out, intent{Type: "download", Target: "下载"})
			}
		case "等待", "等":
			n := 2
			if m := reNum.FindString(seg); m != "" {
				n, _ = strconv.Atoi(m)
			}
			out = append(out, intent{Type: "wait", Seconds: n})
		}
	}
	if len(out) == 0 {
		return []intent{{Type: "fail"}}
	}
	// 搜索后若没有显式点击动作，自动回车（已在搜索意图里设置 Enter）
	return append(out, intent{Type: "finish"})
}

func urlQuery(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r < 128 && (unicode.IsLetter(r) || unicode.IsDigit(r)) {
			b.WriteRune(r)
		} else if r == ' ' {
			b.WriteByte('+')
		} else {
			b.WriteString("%" + strings.ToUpper(fmt.Sprintf("%x", []byte(string(r)))))
		}
	}
	return b.String()
}

// ---------- 元素候选 ----------

type cand struct {
	idx    int
	tag    string
	sel    string
	text   string
	name   string
	autoid string
}

var (
	reWebLine  = regexp.MustCompile(`^(\d+)\.\s+([a-z0-9]+)\s+\[([^\]]*)\]\s+文本="(.*)"$`)
	reDeskLine = regexp.MustCompile(`^控件\s*(\d+):\s*(\S+)\s+名称="([^"]*)"(?:\s+自动化ID="([^"]*)")?`)
)

func parseElements(list string) []cand {
	var out []cand
	for _, line := range strings.Split(list, "\n") {
		line = strings.TrimSpace(line)
		if m := reWebLine.FindStringSubmatch(line); m != nil {
			n, _ := strconv.Atoi(m[1])
			out = append(out, cand{idx: n, tag: m[2], sel: "[" + m[3] + "]", text: m[4]})
		} else if m := reDeskLine.FindStringSubmatch(line); m != nil {
			n, _ := strconv.Atoi(m[1])
			out = append(out, cand{idx: n, tag: m[2], name: m[3], autoid: m[4], text: m[3]})
		}
	}
	return out
}

// ---------- 语义匹配 ----------

var synonymGroups = [][]string{
	{"下载", "download", "下载文件", "下载地址", "立即下载", "下载中心", "免费下载", "保存"},
	{"登录", "登陆", "login", "log in", "sign in", "立即登录", "登录账号"},
	{"注册", "register", "sign up", "免费注册", "注册账号"},
	{"搜索", "search", "查询", "检索", "go", "搜索按钮", "查找"},
	{"提交", "submit", "确定", "确认", "ok", "yes", "提交订单"},
	{"下一步", "next", "下一页", "继续", "下一步按钮"},
	{"上传", "upload", "上传文件", "选择文件", "浏览"},
	{"关闭", "close", "取消", "cancel", "关闭弹窗"},
	{"首页", "home", "主页", "返回首页"},
	{"更多", "more", "查看更多", "展开", "详情"},
	{"复制", "copy", "复制链接", "复制地址"},
	{"删除", "delete", "remove", "移除"},
	{"编辑", "edit", "修改", "编辑内容"},
}

func norm(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// bigramDice 字符二元组 Dice 相似度（对中文效果很好）
func bigramDice(a, b string) float64 {
	ra, rb := []rune(a), []rune(b)
	if len(ra) < 2 || len(rb) < 2 {
		if string(ra) == string(rb) {
			return 1
		}
		return 0
	}
	ma := map[string]int{}
	for i := 0; i+1 < len(ra); i++ {
		ma[string(ra[i:i+2])]++
	}
	inter := 0
	for i := 0; i+1 < len(rb); i++ {
		k := string(rb[i:i+2])
		if ma[k] > 0 {
			ma[k]--
			inter++
		}
	}
	return 2 * float64(inter) / float64(len(ra)-1+len(rb)-1)
}

func simScore(target, text string) float64 {
	t, x := norm(target), norm(text)
	if t == "" || x == "" {
		return 0
	}
	if t == x {
		return 1
	}
	if strings.Contains(x, t) || strings.Contains(t, x) {
		return 0.8
	}
	return bigramDice(t, x) * 0.6
}

func expandSynonyms(target string) []string {
	n := norm(target)
	out := []string{target}
	for _, g := range synonymGroups {
		for _, w := range g {
			nw := norm(w)
			if n == nw || strings.Contains(n, nw) || strings.Contains(nw, n) {
				out = append(out, g...)
				break
			}
		}
	}
	return out
}

func fmtPct(score float64) string {
	if score > 1 {
		score = 1
	}
	return fmt.Sprintf("%.0f%%", score*100)
}

// ---------- 本地规划器 ----------

type localPlanner struct {
	intents []intent
	next    int
	waited  bool
	window  string
}

func newLocalPlanner(instruction, window string) *localPlanner {
	return &localPlanner{intents: parseInstruction(instruction), window: window}
}

func (p *localPlanner) plan(instruction string, obs Observation, history []AIAction) (AIAction, error) {
	if p.next >= len(p.intents) {
		return AIAction{Type: "finish", Reason: "所有意图已完成"}, nil
	}
	it := p.intents[p.next]
	cands := parseElements(obs.Elements)

	// 不需要元素的意图直接执行
	switch it.Type {
	case "fail":
		p.next++
		return AIAction{Type: "fail", Reason: "没能理解这条指令，请换个说法（例如：打开 xxx.com，输入“关键词”，点击搜索）"}, nil
	case "open":
		p.next++
		return AIAction{Type: "open", URL: it.URL, Reason: "打开 " + it.URL}, nil
	case "wait":
		p.next++
		return AIAction{Type: "wait", Seconds: it.Seconds, Reason: fmt.Sprintf("等待 %d 秒让页面稳定", it.Seconds)}, nil
	case "finish":
		p.next++
		return AIAction{Type: "finish", Reason: "指令中的所有步骤都已完成"}, nil
	case "download":
		if it.URL != "" {
			p.next++
			return AIAction{Type: "download", URL: it.URL, Seconds: it.Seconds, Reason: "直接下载文件链接 " + it.URL}, nil
		}
	}

	// 需要在页面上找元素
	if len(cands) == 0 {
		if !p.waited {
			p.waited = true
			return AIAction{Type: "wait", Seconds: 2, Reason: "页面上暂时没有可操作元素，等待加载"}, nil
		}
		return AIAction{Type: "fail", Reason: "页面上没有发现任何可操作元素"}, nil
	}

	label := it.Target
	if label == "" {
		label = "输入框"
	}
	best, score, desc := p.pick(it, cands)
	if score < 0.5 {
		// 下载意图找不到具体目标时退回到“下载”二字
		if it.Type == "download" && it.Target != "下载" {
			it.Target = "下载"
			p.intents[p.next] = it
			return p.plan(instruction, obs, history)
		}
		if !p.waited {
			p.waited = true
			return AIAction{Type: "wait", Seconds: 2, Reason: fmt.Sprintf("正在等待「%s」出现", label)}, nil
		}
		hint := ""
		if best.text != "" {
			hint = fmt.Sprintf("（最接近的元素是「%s」，匹配度 %s）", best.text, fmtPct(score))
		}
		return AIAction{Type: "fail", Reason: fmt.Sprintf("第 %d 步没有找到与「%s」匹配的元素%s", p.next+1, label, hint)}, nil
	}
	p.waited = false
	p.next++

	a := AIAction{Reason: fmt.Sprintf("%s（目标「%s」→ 匹配「%s」，%s）", desc, label, best.text, fmt.Sprintf("%.0f%%", score*100))}
	if obs.Kind == "desktop" {
		a.Window = p.window
		a.Name = best.name
		a.AutoID = best.autoid
	} else {
		a.Selector = best.sel
	}
	switch it.Type {
	case "input":
		a.Type = "input"
		a.Text = it.Text
		a.Enter = it.Enter
		a.Reason = fmt.Sprintf("在「%s」输入 %q%s", best.text, it.Text, map[bool]string{true: " 并回车", false: ""}[it.Enter])
	case "download":
		if obs.Kind == "desktop" {
			a.Type = "click"
		} else {
			a.Type = "download"
			a.Seconds = 60
		}
	default:
		a.Type = "click"
	}
	return a, nil
}

// pick 在候选元素里为意图挑出最匹配的一个
func (p *localPlanner) pick(it intent, cands []cand) (cand, float64, string) {
	var best cand
	bestScore := -1.0
	desc := "点击"
	if it.Type == "input" {
		desc = "输入"
	} else if it.Type == "download" {
		desc = "下载"
	}

	// 输入意图且没指定目标：自动挑输入框
	if it.Type == "input" && it.Target == "" {
		for _, c := range cands {
			t := strings.ToLower(c.tag)
			if strings.HasSuffix(t, "input") || strings.HasSuffix(t, "textarea") || strings.HasSuffix(t, "edit") {
				return c, 1, desc
			}
		}
	}

	targets := expandSynonyms(it.Target)
	for _, c := range cands {
		s := 0.0
		for _, t := range targets {
			if v := simScore(t, c.text); v > s {
				s = v
			}
		}
		tag := strings.ToLower(c.tag)
		if it.Type == "input" {
			if strings.HasSuffix(tag, "input") || strings.HasSuffix(tag, "textarea") || strings.HasSuffix(tag, "edit") {
				s += 0.4
			} else {
				s -= 0.3
			}
		} else if it.Type == "click" || it.Type == "download" {
			if strings.HasSuffix(tag, "a") || strings.HasSuffix(tag, "button") {
				s += 0.05
			}
		}
		// 分数相近时优先文本更短（更精确）的元素
		if s > bestScore+0.02 || (s > bestScore-0.02 && len([]rune(c.text)) < len([]rune(best.text))) {
			best, bestScore = c, s
		}
	}
	return best, bestScore, desc
}
