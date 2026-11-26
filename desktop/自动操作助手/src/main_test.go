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
		{Action{Kind: "url", Text: "https://example.com"}, "打开网址: https://example.com"},
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
