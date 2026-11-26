package main

import (
	"strings"
	"testing"
)

// ---------- 指令解析 ----------

func TestParseInstructionBasic(t *testing.T) {
	cases := []struct {
		in   string
		want []intent
	}{
		{"打开 example.com 然后输入“天气”并点击搜索", []intent{
			{Type: "open", URL: "https://example.com"},
			{Type: "input", Text: "天气"},
			{Type: "click", Target: "搜索"},
			{Type: "finish"},
		}},
		{"搜索 Golang 教程", []intent{
			{Type: "input", Target: "搜索", Text: "Golang 教程", Enter: true},
			{Type: "finish"},
		}},
		{"打开 https://a.com/x.zip 并下载", []intent{
			{Type: "download", URL: "https://a.com/x.zip", Seconds: 60},
			{Type: "finish"},
		}},
		{"点击下载按钮", []intent{
			{Type: "click", Target: "下载"},
			{Type: "finish"},
		}},
		{"等 3 秒 后下载报表", []intent{
			{Type: "wait", Seconds: 3},
			{Type: "download", Target: "报表"},
			{Type: "finish"},
		}},
	}
	for _, c := range cases {
		got := parseInstruction(c.in)
		if len(got) != len(c.want) {
			t.Fatalf("%q: 数量不符 got=%+v", c.in, got)
		}
		for i := range got {
			g := got[i]
			if g != c.want[i] {
				t.Errorf("%q 第%d项: got %+v want %+v", c.in, i, g, c.want[i])
			}
		}
	}
}

func TestParseInstructionUnknown(t *testing.T) {
	got := parseInstruction("随便来一下")
	if len(got) != 1 || got[0].Type != "fail" {
		t.Errorf("应返回 fail，got %+v", got)
	}
}

// ---------- 语义匹配 ----------

func TestSimScore(t *testing.T) {
	if simScore("下载", "下载") != 1 {
		t.Error("完全相同应为 1")
	}
	if simScore("登录", "立即登录") < 0.8 {
		t.Error("包含关系应 ≥0.8")
	}
	if simScore("下载", "关于我们") > 0.3 {
		t.Error("无关词应低分")
	}
}

func TestExpandSynonyms(t *testing.T) {
	ex := strings.Join(expandSynonyms("登陆"), ",")
	if !strings.Contains(ex, "login") {
		t.Error("同义词应展开到 login")
	}
}

// ---------- 规划器闭环 ----------

const webObs = `1. input [data-nf="1"] 文本="搜索"
2. button [data-nf="2"] 文本="搜索"
3. a [data-nf="3"] 文本="立即下载"
4. a [data-nf="4"] 文本="关于我们"
5. input [data-nf="5"] type=file 文本=""`

func TestLocalPlannerWebFlow(t *testing.T) {
	p := newLocalPlanner("打开 https://x.com，搜索 Golang，然后点击下载", "")
	obs := Observation{Kind: "web", Elements: webObs}

	a, _ := p.plan("", obs, nil)
	if a.Type != "open" || a.URL != "https://x.com" {
		t.Fatalf("第1步应为 open，got %+v", a)
	}
	a, _ = p.plan("", obs, nil)
	if a.Type != "input" || a.Enter != true || a.Text != "Golang" {
		t.Fatalf("第2步应为输入+回车，got %+v", a)
	}
	if a.Selector != `[data-nf="1"]` {
		t.Errorf("应选输入框而非按钮，got %s", a.Selector)
	}
	a, _ = p.plan("", obs, nil)
	if a.Type != "click" || a.Selector != `[data-nf="3"]` {
		t.Fatalf("第3步应点中“立即下载”，got %+v", a)
	}
	a, _ = p.plan("", obs, nil)
	if a.Type != "finish" {
		t.Fatalf("最后应为 finish，got %+v", a)
	}
}

func TestLocalPlannerNotFoundThenFail(t *testing.T) {
	p := newLocalPlanner("点击 购物车", "")
	obs := Observation{Kind: "web", Elements: webObs}
	a, _ := p.plan("", obs, nil)
	if a.Type != "wait" {
		t.Fatalf("找不到目标时应先等待，got %+v", a)
	}
	a, _ = p.plan("", obs, nil)
	if a.Type != "fail" || !strings.Contains(a.Reason, "购物车") {
		t.Fatalf("再次找不到应失败并说明，got %+v", a)
	}
}

func TestLocalPlannerDesktop(t *testing.T) {
	p := newLocalPlanner("点击保存", "记事本")
	obs := Observation{Kind: "desktop", Elements: `控件 1: ControlType.Button 名称="保存(S)" 自动化ID="3"
控件 2: ControlType.MenuItem 名称="文件" 自动化ID="1"`}
	a, _ := p.plan("", obs, nil)
	if a.Type != "click" || a.Window != "记事本" || a.AutoID != "3" {
		t.Fatalf("桌面点击应带窗口与自动化ID，got %+v", a)
	}
}

func TestUseLocalEngine(t *testing.T) {
	if !useLocalEngine(AIConfig{}) || !useLocalEngine(AIConfig{Provider: "local"}) {
		t.Error("空/本地 应走本地引擎")
	}
	if useLocalEngine(AIConfig{Provider: "qwen-vl", APIKey: "k"}) {
		t.Error("配了云端 Key 不应走本地引擎")
	}
	if _, err := testAIConnection(AIConfig{Provider: "local"}); err != nil {
		t.Errorf("本地引擎连接测试不应失败：%v", err)
	}
}
