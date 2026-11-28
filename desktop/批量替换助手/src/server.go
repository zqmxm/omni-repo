
//go:build webui

package main

// server.go —— 内嵌 Web 控制台后端（零第三方依赖）
// 注意：桌面版默认不编译本文件（构建标签 webui），exe 内不存在任何 HTTP 服务。
// 复用自动操作助手的加固模式：单实例检测、端口占用自动换端口、启动失败弹窗与 URL 文件。

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed web/*
var webFS embed.FS

const consolePort = "18762"

var consoleURL string

type Server struct {
	mu      sync.Mutex
	running bool
	events  map[chan string]bool
	evMu    sync.Mutex
}

func newServer() *Server {
	return &Server{events: make(map[chan string]bool)}
}

func (s *Server) broadcast(v interface{}) {
	data, _ := json.Marshal(v)
	s.evMu.Lock()
	for ch := range s.events {
		select {
		case ch <- string(data):
		default:
		}
	}
	s.evMu.Unlock()
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(v)
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	sub, _ := fs.Sub(webFS, "web")
	mux.Handle("/", http.FileServer(http.FS(sub)))

	// SSE 事件流
	mux.HandleFunc("/api/events", func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "不支持流式输出", 500)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		ch := make(chan string, 64)
		s.evMu.Lock()
		s.events[ch] = true
		s.evMu.Unlock()
		defer func() {
			s.evMu.Lock()
			delete(s.events, ch)
			s.evMu.Unlock()
		}()
		flusher.Flush()
		for {
			select {
			case msg := <-ch:
				fmt.Fprintf(w, "data: %s\n\n", msg)
				flusher.Flush()
			case <-r.Context().Done():
				return
			}
		}
	})

	// 原生文件夹选择对话框
	mux.HandleFunc("/api/pickfolder", func(w http.ResponseWriter, r *http.Request) {
		path, err := pickFolderDialog()
		if err != nil {
			writeJSON(w, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, map[string]string{"path": path})
	})

	// 退出程序（关掉常驻的本地服务）
	mux.HandleFunc("/api/quit", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]bool{"ok": true})
		go func() {
			time.Sleep(400 * time.Millisecond)
			os.Exit(0)
		}()
	})

	// 执行替换任务
	mux.HandleFunc("/api/run", func(w http.ResponseWriter, r *http.Request) {
		var req ReplaceReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, map[string]string{"error": "请求格式错误：" + err.Error()})
			return
		}
		if err := validateReq(&req); err != nil {
			writeJSON(w, map[string]string{"error": err.Error()})
			return
		}
		s.mu.Lock()
		if s.running {
			s.mu.Unlock()
			writeJSON(w, map[string]string{"error": "已有任务正在运行，请等待完成"})
			return
		}
		s.running = true
		s.mu.Unlock()
		defer func() {
			s.mu.Lock()
			s.running = false
			s.mu.Unlock()
		}()

		s.broadcast(map[string]interface{}{"type": "log", "level": "info", "message": "开始扫描文件夹：" + req.Dir})
		done, failed := Execute(req, func(it Item) {
			s.broadcast(map[string]interface{}{"type": "item", "item": it})
		})
		s.broadcast(map[string]interface{}{
			"type": "done", "done": done, "failed": failed,
			"message": fmt.Sprintf("全部完成：成功 %d 项，失败/跳过 %d 项", done, failed),
		})
		writeJSON(w, map[string]interface{}{"ok": true, "done": done, "failed": failed})
	})
	return mux
}

func probeConsole(port string) bool {
	client := &http.Client{Timeout: 800 * time.Millisecond}
	resp, err := client.Get("http://127.0.0.1:" + port + "/")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == 200
}

func serveWeb() error {
	if probeConsole(consolePort) {
		consoleURL = "http://127.0.0.1:" + consolePort
		if !openBrowser(consoleURL) {
			showErrorBox("批量替换助手", "程序已在运行，但自动打开浏览器失败。\n请在浏览器地址栏输入：\n"+consoleURL)
		}
		return nil
	}
	addr := "127.0.0.1:" + consolePort
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		ln, err = net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return fmt.Errorf("无法监听本地端口：%w", err)
		}
		addr = "127.0.0.1:" + strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)
	}
	consoleURL = "http://" + addr
	// 写地址文件
	exe, _ := os.Executable()
	dir := filepath.Dir(exe)
	_ = os.WriteFile(filepath.Join(dir, "批量替换助手-url.txt"),
		[]byte("\ufeff批量替换助手控制台正在运行，如浏览器未自动打开，请复制下面地址到浏览器：\r\n\r\n"+consoleURL+"\r\n"), 0644)
	go func() {
		if !openBrowser(consoleURL) {
			showErrorBox("批量替换助手", "自动打开浏览器失败。\n请在浏览器地址栏输入：\n"+consoleURL+
				"\n\n（程序已在后台运行；用完请在网页右上角点「退出程序」）")
		}
	}()
	srv := &http.Server{Handler: newServer().routes(), ReadHeaderTimeout: 5 * time.Second}
	return srv.Serve(ln)
}

func openBrowser(url string) bool {
	time.Sleep(400 * time.Millisecond)
	if openBrowserNative(url) {
		return true
	}
	// 兜底：系统默认关联 / xdg-open
	if _, err := os.Stat("/usr/bin/xdg-open"); err == nil {
		if exec.Command("xdg-open", url).Start() == nil {
			return true
		}
	}
	if exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start() == nil {
		return true
	}
	return false
}

var _ = strings.TrimSpace
