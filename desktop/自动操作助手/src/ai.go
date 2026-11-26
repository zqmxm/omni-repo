package main

// ai.go —— AI 视觉自动化内核（零第三方依赖）
//
// 工作流程：
//   1. 通过 CDP 抓取网页截图 + DOM 可交互元素摘要（或 UIA 控件树）
//   2. 把「用户指令 + 页面观察」发给多模态大模型（通义千问 VL / 豆包 / OpenAI 兼容接口）
//   3. 模型返回结构化动作 JSON，逐步执行
//   4. 每步执行后重新观察，形成「感知→决策→执行」闭环
//
// 全程通过 CDP / UI Automation 操作元素，不向系统发送键鼠事件，不占用用户电脑。

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// ---------- 配置 ----------

type AIConfig struct {
	Provider string `json:"provider"` // qwen-vl | doubao | openai | custom
	Endpoint string `json:"endpoint"` // OpenAI 兼容 chat/completions 地址
	APIKey   string `json:"apiKey"`
	Model    string `json:"model"`
	MaxSteps int    `json:"maxSteps"`
	BrowserPath string `json:"browserPath,omitempty"`
	DownloadDir string `json:"downloadDir,omitempty"`
}

func defaultEndpoint(p string) string {
	switch strings.ToLower(p) {
	case "qwen-vl", "qwen", "通义千问":
		return "https://dashscope.aliyuncs.com/compatible-mode/v1/chat/completions"
	case "doubao", "豆包":
		return "https://ark.cn-beijing.volces.com/api/v3/chat/completions"
	case "openai":
		return "https://api.openai.com/v1/chat/completions"
	default:
		return ""
	}
}

func defaultModel(p string) string {
	switch strings.ToLower(p) {
	case "qwen-vl", "qwen", "通义千问":
		return "qwen-vl-max-latest"
	case "doubao", "豆包":
		return "doubao-1-5-vision-pro-32k-250115"
	case "openai":
		return "gpt-4o"
	default:
		return ""
	}
}

// ---------- 观察结果 ----------

type Observation struct {
	Kind       string `json:"kind"`       // web | desktop
	URL        string `json:"url,omitempty"`
	Title      string `json:"title,omitempty"`
	Screenshot string `json:"screenshot"` // base64 PNG
	Elements   string `json:"elements"`   // 可交互元素摘要（CDP DOM 或 UIA 控件树）
}

// ---------- AI 动作 ----------

type AIAction struct {
	Type       string `json:"type"`       // open | click | input | download | upload | wait | finish | fail
	Selector   string `json:"selector"`   // CSS 选择器（网页）
	Text       string `json:"text"`       // 输入文本 / 说明
	URL        string `json:"url"`        // open/download 用
	File       string `json:"file"`       // upload 保存路径
	Name       string `json:"name"`       // 桌面控件名称
	AutoID     string `json:"autoid"`     // 桌面控件自动化 ID
	Window     string `json:"window"`     // 桌面窗口标题
	Seconds    int    `json:"seconds"`    // wait / download 超时
	Reason     string `json:"reason"`     // AI 的思考说明
}

// ---------- OpenAI 兼容多模态客户端 ----------

type aiClient struct {
	cfg    AIConfig
	client *http.Client
}

func newAIClient(cfg AIConfig) (*aiClient, error) {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, errors.New("请先填写 API Key")
	}
	ep := strings.TrimSpace(cfg.Endpoint)
	if ep == "" {
		ep = defaultEndpoint(cfg.Provider)
	}
	if ep == "" {
		return nil, errors.New("请先填写 API 地址")
	}
	model := strings.TrimSpace(cfg.Model)
	if model == "" {
		model = defaultModel(cfg.Provider)
	}
	cfg.Endpoint, cfg.Model = ep, model
	return &aiClient{cfg: cfg, client: &http.Client{Timeout: 120 * time.Second}}, nil
}

const systemPrompt = `你是一个电脑自动化助手。你会收到「用户指令」和「当前页面观察」（截图 + 可交互元素列表）。
请一步步完成用户指令，每一步只输出一个动作，用严格的 JSON 格式（不要 markdown、不要解释、不要代码块）：

{"type":"click","selector":"#download","reason":"点击下载按钮"}
{"type":"input","selector":"input[name=q]","text":"关键词","reason":"在搜索框输入"}
{"type":"open","url":"https://example.com","reason":"打开目标网站"}
{"type":"download","url":"https://.../file.zip","file":"D:/下载","seconds":60,"reason":"下载文件"}
{"type":"upload","selector":"input[type=file]","file":"D:/a.png","reason":"上传文件"}
{"type":"wait","seconds":2,"reason":"等待页面加载"}
{"type":"click","window":"记事本","name":"保存","reason":"点击桌面软件保存按钮"}
{"type":"input","window":"打开","name":"文件名:","text":"D:/a.png","reason":"在系统对话框输入路径"}
{"type":"finish","reason":"任务完成"}
{"type":"fail","reason":"无法完成，原因"}

可用 type：open / click / input / download / upload / wait / finish / fail。
网页操作用 selector（优先用元素列表里给出的选择器）；桌面软件用 window + name 或 window + autoid。
截图用于理解页面布局和视觉元素；元素列表用于精确选择。
只输出一个 JSON 对象，不要输出其他任何文字。`

func (c *aiClient) plan(instruction string, obs Observation, history []AIAction) (AIAction, error) {
	// 构造消息：截图（图片）+ 文字（指令 + 元素摘要 + 历史）
	userText := fmt.Sprintf("用户指令：%s\n\n页面类型：%s\n", instruction, obs.Kind)
	if obs.URL != "" {
		userText += "当前网址：" + obs.URL + "\n"
	}
	if obs.Title != "" {
		userText += "页面标题：" + obs.Title + "\n"
	}
	userText += "\n可交互元素列表：\n" + obs.Elements + "\n"
	if len(history) > 0 {
		userText += "\n已执行动作：\n"
		for i, h := range history {
			userText += fmt.Sprintf("%d. %s %s %s（%s）\n", i+1, h.Type, h.Selector, h.Text, h.Reason)
		}
	}
	userText += "\n请输出下一步动作 JSON："

	content := []map[string]interface{}{
		{"type": "text", "text": userText},
	}
	if obs.Screenshot != "" {
		content = append(content, map[string]interface{}{
			"type": "image_url",
			"image_url": map[string]interface{}{
				"url": "data:image/png;base64," + obs.Screenshot,
			},
		})
	}

	body := map[string]interface{}{
		"model": c.cfg.Model,
		"messages": []map[string]interface{}{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": content},
		},
		"temperature": 0.2,
		"max_tokens":  600,
	}
	data, _ := json.Marshal(body)
	req, err := http.NewRequest("POST", c.cfg.Endpoint, bytes.NewReader(data))
	if err != nil {
		return AIAction{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)

	resp, err := c.client.Do(req)
	if err != nil {
		return AIAction{}, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return AIAction{}, fmt.Errorf("AI 接口返回 %d：%s", resp.StatusCode, string(truncate(raw, 300)))
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content interface{} `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return AIAction{}, fmt.Errorf("解析 AI 响应失败：%v", err)
	}
	if len(result.Choices) == 0 {
		return AIAction{}, errors.New("AI 响应为空")
	}
	text := contentToString(result.Choices[0].Message.Content)
	action, err := parseAIAction(text)
	if err != nil {
		return AIAction{}, fmt.Errorf("AI 动作解析失败：%v（原文：%s）", err, truncate([]byte(text), 200))
	}
	return action, nil
}

func contentToString(v interface{}) string {
	switch x := v.(type) {
	case string:
		return x
	case []interface{}:
		var sb strings.Builder
		for _, part := range x {
			if m, ok := part.(map[string]interface{}); ok {
				if t, ok := m["text"].(string); ok {
					sb.WriteString(t)
				}
			}
		}
		return sb.String()
	default:
		return fmt.Sprintf("%v", v)
	}
}

func truncate(b []byte, n int) []byte {
	if len(b) <= n {
		return b
	}
	return b[:n]
}

var reFence = regexp.MustCompile("(?s)```(?:json)?\\s*(.*?)\\s*```")

func parseAIAction(text string) (AIAction, error) {
	text = strings.TrimSpace(text)
	if m := reFence.FindStringSubmatch(text); len(m) == 2 {
		text = strings.TrimSpace(m[1])
	}
	// 截取第一个完整 JSON 对象
	start := strings.Index(text, "{")
	if start < 0 {
		return AIAction{}, errors.New("响应中没有 JSON 对象")
	}
	depth, end := 0, -1
	inStr, escaped := false, false
	for i := start; i < len(text); i++ {
		ch := text[i]
		if inStr {
			if escaped {
				escaped = false
			} else if ch == '\\' {
				escaped = true
			} else if ch == '"' {
				inStr = false
			}
			continue
		}
		switch ch {
		case '"':
			inStr = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				end = i + 1
			}
		}
		if end > 0 {
			break
		}
	}
	if end < 0 {
		return AIAction{}, errors.New("JSON 对象不完整")
	}
	var a AIAction
	if err := json.Unmarshal([]byte(text[start:end]), &a); err != nil {
		return AIAction{}, err
	}
	if a.Type == "" {
		return AIAction{}, errors.New("动作缺少 type 字段")
	}
	return a, nil
}

// ---------- 页面观察：CDP ----------

// observeWeb 抓取当前网页截图与可交互元素摘要
func observeWeb(b *bgBrowser) (Observation, error) {
	obs := Observation{Kind: "web"}
	// 网址
	if u, err := b.eval("location.href"); err == nil {
		obs.URL, _ = u.(string)
	}
	if t, err := b.eval("document.title"); err == nil {
		obs.Title, _ = t.(string)
	}
	// 可交互元素摘要（限制数量，控制 token）
	js := `(function(){
		var els = document.querySelectorAll('a,button,input,select,textarea,[role=button],[onclick]');
		var out = [], n = 0;
		for (var i=0;i<els.length && n<80;i++) {
			var e = els[i];
			var r = e.getBoundingClientRect();
			if (r.width===0 || r.height===0) continue;
			var sel = e.id ? '#'+e.id : '';
			if (!sel && e.name) sel = e.tagName.toLowerCase()+'[name="'+e.name+'"]';
			if (!sel) { var cls=(e.className&&typeof e.className==='string')?e.className.trim().split(/\\s+/)[0]:''; sel = e.tagName.toLowerCase()+(cls?'.'+cls:''); }
			var txt = (e.innerText||e.value||e.placeholder||e.getAttribute('aria-label')||'').trim().slice(0,60);
			out.push((n+1)+'. '+e.tagName.toLowerCase()+' ['+sel+'] 文本="'+txt+'"');
			n++;
		}
		return out.join('\\n');
	})()`
	if v, err := b.eval(js); err == nil {
		obs.Elements, _ = v.(string)
	}
	// 截图（CDP Page.captureScreenshot）
	shot, err := b.screenshot()
	if err != nil {
		// 截图失败不影响文字观察
		obs.Elements += "\n（截图暂不可用，请根据元素列表决策）"
	} else {
		obs.Screenshot = shot
	}
	return obs, nil
}

// screenshot 通过 CDP 截取当前网页，返回 base64 PNG
func (b *bgBrowser) screenshot() (string, error) {
	raw, err := b.ws.call("Page.captureScreenshot", map[string]interface{}{
		"format":  "jpeg",
		"quality": 70,
	}, b.sessionID, 20*time.Second)
	if err != nil {
		return "", err
	}
	var r struct {
		Data string `json:"data"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return "", err
	}
	return r.Data, nil
}

// observeDesktop 抓取 UIA 控件树（结构化"视觉"），不截图
func observeDesktop(window string) (Observation, error) {
	obs := Observation{Kind: "desktop"}
	if strings.TrimSpace(window) == "" {
		return obs, errors.New("请先填写窗口标题关键词")
	}
	tree, err := uiaDumpTree(window)
	if err != nil {
		return obs, err
	}
	obs.Title = window
	obs.Elements = tree
	return obs, nil
}

// ---------- 动作执行 ----------

func executeAIAction(a AIAction, b *bgBrowser, cfg AIConfig) error {
	switch strings.ToLower(a.Type) {
	case "open":
		if b == nil {
			return errors.New("后台浏览器未启动")
		}
		return b.openURL(a.URL)
	case "click":
		// 带 window / name / autoid → 桌面软件 UIA 点击；否则走网页
		if strings.TrimSpace(a.Window) != "" || strings.TrimSpace(a.AutoID) != "" {
			_, err := uiaInvoke(a.Window, firstNonEmpty(a.Name, a.AutoID), a.AutoID)
			return err
		}
		if b == nil {
			return errors.New("后台浏览器未启动")
		}
		return b.click(a.Selector, a.Text)
	case "input":
		// 带 window → 桌面控件赋值；否则走网页
		if strings.TrimSpace(a.Window) != "" || strings.TrimSpace(a.AutoID) != "" {
			_, err := uiaSetValue(a.Window, firstNonEmpty(a.Name, a.AutoID), a.AutoID, a.Text)
			return err
		}
		if b == nil {
			return errors.New("后台浏览器未启动")
		}
		return b.inputText(a.Selector, a.Text)
	case "download":
		if b == nil {
			return errors.New("后台浏览器未启动")
		}
		if a.URL != "" {
			_, err := b.download(a.URL, a.File, a.Seconds)
			return err
		}
		if a.Selector != "" {
			_, err := b.downloadByClick(currentURL(b), a.Selector, a.File, a.Seconds)
			return err
		}
		return errors.New("下载动作缺少 url 或 selector")
	case "upload":
		if b == nil {
			return errors.New("后台浏览器未启动")
		}
		return b.upload(a.Selector, a.File, "")
	case "wait":
		secs := a.Seconds
		if secs <= 0 {
			secs = 2
		}
		time.Sleep(time.Duration(secs) * time.Second)
		return nil
	case "click_desktop":
		_, err := uiaInvoke(a.Window, firstNonEmpty(a.Name, a.AutoID), a.AutoID)
		return err
	case "input_desktop":
		_, err := uiaSetValue(a.Window, firstNonEmpty(a.Name, a.AutoID), a.AutoID, a.Text)
		return err
	case "finish", "fail":
		return nil
	default:
		return fmt.Errorf("未知动作类型：%s", a.Type)
	}
}

func currentURL(b *bgBrowser) string {
	if b == nil {
		return ""
	}
	v, err := b.eval("location.href")
	if err != nil {
		return ""
	}
	s, _ := v.(string)
	return s
}

// ---------- 主循环：AI 自主完成任务 ----------

type AIProgress struct {
	Step    int      `json:"step"`
	Total   int      `json:"total"`
	Action  AIAction `json:"action"`
	Message string   `json:"message"`
}

// runAIAgent 是 AI 视觉自动化的主入口
// mode: "web"（网页）或 "desktop"（桌面软件）
// onProgress 为进度回调，可为 nil
func runAIAgent(instruction string, cfg AIConfig, browserPath, downloadDir, window string, mode string, onProgress func(AIProgress)) error {
	client, err := newAIClient(cfg)
	if err != nil {
		return err
	}
	maxSteps := cfg.MaxSteps
	if maxSteps <= 0 {
		maxSteps = 12
	}

	var b *bgBrowser
	if mode == "web" {
		b, err = startBackgroundBrowser(browserPath, downloadDir)
		if err != nil {
			return fmt.Errorf("启动后台浏览器失败：%v", err)
		}
		defer b.Close()
	}

	history := make([]AIAction, 0, maxSteps)
	for step := 1; step <= maxSteps; step++ {
		var obs Observation
		if mode == "web" {
			obs, err = observeWeb(b)
		} else {
			obs, err = observeDesktop(window)
		}
		if err != nil {
			return fmt.Errorf("第 %d 步观察失败：%v", step, err)
		}

		action, err := client.plan(instruction, obs, history)
		if err != nil {
			return fmt.Errorf("第 %d 步 AI 决策失败：%v", step, err)
		}
		if onProgress != nil {
			onProgress(AIProgress{Step: step, Total: maxSteps, Action: action, Message: action.Reason})
		}

		t := strings.ToLower(action.Type)
		if t == "finish" {
			return nil
		}
		if t == "fail" {
			return errors.New("AI 判定无法完成：" + action.Reason)
		}

		if err := executeAIAction(action, b, cfg); err != nil {
			return fmt.Errorf("第 %d 步执行失败（%s）：%v", step, action.Type, err)
		}
		history = append(history, action)
	}
	return fmt.Errorf("达到最大步数 %d，任务可能未完成；请查看运行日志", maxSteps)
}

// ---------- 连接测试 ----------

func testAIConnection(cfg AIConfig) (string, error) {
	client, err := newAIClient(cfg)
	if err != nil {
		return "", err
	}
	body := map[string]interface{}{
		"model": client.cfg.Model,
		"messages": []map[string]interface{}{
			{"role": "user", "content": "回复\"连接成功\"三个字"},
		},
		"max_tokens": 20,
	}
	data, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", client.cfg.Endpoint, bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+client.cfg.APIKey)
	resp, err := client.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("HTTP %d：%s", resp.StatusCode, string(truncate(raw, 200)))
	}
	var result struct {
		Choices []struct {
			Message struct {
				Content interface{} `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", err
	}
	if len(result.Choices) == 0 {
		return "", errors.New("响应为空")
	}
	return contentToString(result.Choices[0].Message.Content), nil
}

// base64 编码工具（供界面层调用）
func encodeFileToBase64(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}
