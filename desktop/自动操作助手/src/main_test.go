package main

import (
	"encoding/json"
	"testing"
)

// 检查动作列表的中文描述
func TestDescribeActions(t *testing.T) {
	cases := []struct {
		a Action
		want string
	}{
		{Action{Kind: "ldown", X: 100, Y: 200}, "按下左键 (100, 200)"},
		{Action{Kind: "wheel", Delta: 120}, "滚轮 120"},
		{Action{Kind: "wait", Delay: 1000}, "等待 1000 毫秒"},
		{Action{Kind: "text", Text: "你好"}, "输入文本: 你好"},
		{Action{Kind: "url", Text: "https://example.com"}, "打开网址(前台): https://example.com"},
		{Action{Kind: "webopen", Target: "https://example.com"}, "🌐 后台打开: https://example.com"},
		{Action{Kind: "webclick", Target: "#download"}, "🌐 后台点击: #download"},
		{Action{Kind: "webdownloadclick", Target: "https://example.com", Submit: "#dl", File: "D:/下载"}, "🌐 后台点击下载: https://example.com [#dl] → D:/下载"},
		{Action{Kind: "webupload", Target: "input[type=file]", File: "D:/a.png"}, "🌐 后台上传: D:/a.png → input[type=file]"},
		{Action{Kind: "winclick", Target: "记事本", Name: "保存"}, "🖥 后台点击控件: 保存 @ 记事本"},
		{Action{Kind: "wininput", Target: "打开", AutoID: "1148", Text: "D:/test.png"}, "🖥 后台填写控件: 自动化ID=1148 ← D:/test.png"},
	}
	for _, c := range cases {
		if got := describe(c.a); got != c.want {
			t.Errorf("describe(%v) = %q, 期望 %q", c.a, got, c.want)
		}
	}
}

// 检查脚本文件能正确保存与读取
func TestActionScriptRoundTrip(t *testing.T) {
	in := []Action{
		{Kind: "ldown", X: 10, Y: 20, Delay: 100},
		{Kind: "kdown", VK: 65, Delay: 50},
		{Kind: "text", Text: "中文测试", Delay: 200},
		{Kind: "webupload", Target: "input[type=file]", File: "D:/测试.png", Submit: "#go", Delay: 300},
	}
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out []Action
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	if len(out) != len(in) || out[2].Text != "中文测试" || out[0].X != 10 {
		t.Fatalf("脚本往返失败: %+v", out)
	}
}

// 检查常用按键名称
func TestKeyName(t *testing.T) {
	if keyName(vkControl) != "Ctrl" || keyName(vkF9) != "F9" || keyName('A') != "A" {
		t.Fatal("按键名称映射不正确")
	}
}
