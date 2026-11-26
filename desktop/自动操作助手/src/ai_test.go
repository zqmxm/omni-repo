package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseAIAction(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{`{"type":"click","selector":"#download","reason":"点击下载"}`, "click"},
		{"```json\n" + `{"type":"input","selector":"input[name=q]","text":"test"}` + "\n```", "input"},
		{"思考了一下\n" + `{"type":"finish","reason":"完成"}` + "\n结束", "finish"},
		{`{"type":"download","url":"https://a.com/f.zip","file":"D:/下载","seconds":60}`, "download"},
	}
	for _, c := range cases {
		a, err := parseAIAction(c.in)
		if err != nil {
			t.Fatalf("解析失败 %q: %v", c.in, err)
		}
		if a.Type != c.want {
			t.Fatalf("期望 %s，实际 %s", c.want, a.Type)
		}
	}
}

func TestParseAIActionInvalid(t *testing.T) {
	for _, in := range []string{"没有 JSON", `{"type":}`, `{"selector":"x"}`} {
		if _, err := parseAIAction(in); err == nil {
			t.Fatalf("应当失败: %q", in)
		}
	}
}

func TestAIPlanWithMockServer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 校验请求头与多模态消息结构
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Error("缺少 API Key 头")
		}
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error("请求体不是 JSON")
		}
		msgs := body["messages"].([]interface{})
		user := msgs[1].(map[string]interface{})
		content := user["content"].([]interface{})
		if len(content) < 2 {
			t.Error("应当同时包含文字与图片")
		}
		if content[1].(map[string]interface{})["type"] != "image_url" {
			t.Error("缺少图片内容")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[{"message":{"content":"{\"type\":\"click\",\"selector\":\"#go\",\"reason\":\"测试\"}"}}]}`))
	}))
	defer server.Close()

	cfg := AIConfig{Provider: "custom", Endpoint: server.URL, APIKey: "test-key", Model: "test-model"}
	client, err := newAIClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	action, err := client.plan("测试指令", Observation{
		Kind:       "web",
		URL:        "https://example.com",
		Screenshot: "aGVsbG8=", // "hello" base64
		Elements:   "1. button [#go] 文本=\"开始\"",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if action.Type != "click" || action.Selector != "#go" {
		t.Fatalf("动作不符: %+v", action)
	}
}

func TestDefaults(t *testing.T) {
	if !strings.Contains(defaultEndpoint("qwen-vl"), "dashscope") {
		t.Error("通义千问默认地址错误")
	}
	if !strings.Contains(defaultEndpoint("doubao"), "volces") {
		t.Error("豆包默认地址错误")
	}
	if defaultModel("qwen") != "qwen-vl-max-latest" {
		t.Error("默认模型错误")
	}
}
