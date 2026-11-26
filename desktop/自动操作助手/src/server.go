package main

// server.go —— 现代化 Web 界面后端（零第三方依赖）
//
// 提供内嵌 HTTP 服务，把 Web Dashboard 与 AI 内核连接起来。
// SSE（Server-Sent Events）实时推送 AI 的每一步决策与执行结果。

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

type Server struct {
	mu      sync.Mutex
	cfg     AIConfig
	cfgPath string
	flows   []Flow
	events  map[chan string]bool
	evMu    sync.Mutex
	running bool
}

type Flow struct {
	Name        string `json:"name"`
	Instruction string `json:"instruction"`
	Desc        string `json:"desc,omitempty"`
	Mode        string `json:"mode,omitempty"`
}

func newServer() *Server {
	exe, _ := os.Executable()
	dir := filepath.Dir(exe)
	return &Server{
		cfgPath: filepath.Join(dir, "neuralflow-config.json"),
		events:  make(map[chan string]bool),
	}
}

func (s *Server) loadConfig() {
	data, err := os.ReadFile(s.cfgPath)
	if err != nil {
		s.cfg = AIConfig{Provider: "local", MaxSteps: 12}
		return
	}
	var c AIConfig
	if json.Unmarshal(data, &c) == nil {
		s.cfg = c
		if s.cfg.MaxSteps <= 0 {
			s.cfg.MaxSteps = 12
		}
	}
}

func (s *Server) saveConfig(c AIConfig) error {
	s.mu.Lock()
	s.cfg = c
	s.mu.Unlock()
	data, _ := json.MarshalIndent(c, "", "  ")
	return os.WriteFile(s.cfgPath, data, 0600)
}

func (s *Server) broadcast(ev interface{}) {
	data, _ := json.Marshal(ev)
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

	// 静态页面
	sub, _ := fs.Sub(webFS, "web")
	mux.Handle("/", http.FileServer(http.FS(sub)))

	// 读取配置（API Key 返回给本机界面，仅本机可访问）
	mux.HandleFunc("/api/config", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			s.mu.Lock()
			c := s.cfg
			s.mu.Unlock()
			writeJSON(w, c)
			return
		}
		if r.Method == "POST" {
			var c AIConfig
			if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
				writeJSON(w, map[string]string{"error": err.Error()})
				return
			}
			if err := s.saveConfig(c); err != nil {
				writeJSON(w, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, map[string]bool{"ok": true})
		}
	})

	// 测试 AI 连接
	mux.HandleFunc("/api/test", func(w http.ResponseWriter, r *http.Request) {
		var c AIConfig
		json.NewDecoder(r.Body).Decode(&c)
		reply, err := testAIConnection(c)
		if err != nil {
			writeJSON(w, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, map[string]string{"reply": reply})
	})

	// SSE 事件流
	mux.HandleFunc("/api/events", func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "SSE 不支持", 500)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		ch := make(chan string, 100)
		s.evMu.Lock()
		s.events[ch] = true
		s.evMu.Unlock()
		defer func() {
			s.evMu.Lock()
			delete(s.events, ch)
			s.evMu.Unlock()
		}()
		fmt.Fprintf(w, "data: {\"type\":\"log\",\"message\":\"事件流已连接\"}\n\n")
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

	// 运行 AI 任务
	mux.HandleFunc("/api/run", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Instruction string `json:"instruction"`
			Mode        string `json:"mode"`
			Window      string `json:"window"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		s.mu.Lock()
		if s.running {
			s.mu.Unlock()
			http.Error(w, "已有任务正在运行", 409)
			return
		}
		s.running = true
		cfg := s.cfg
		s.mu.Unlock()
		defer func() { s.mu.Lock(); s.running = false; s.mu.Unlock() }()

		flusher, _ := w.(http.Flusher)
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		send := func(ev interface{}) {
			data, _ := json.Marshal(ev)
			fmt.Fprintf(w, "data: %s\n\n", data)
			if flusher != nil {
				flusher.Flush()
			}
			s.broadcast(ev)
		}

		dl := strings.TrimSpace(cfg.DownloadDir)
		if dl == "" {
			// 未指定时默认到用户目录下的 Downloads/NeuralFlow
			dl = firstNonEmpty(os.Getenv("USERPROFILE"), os.Getenv("HOME"))
			if dl != "" {
				dl = filepath.Join(dl, "Downloads", "NeuralFlow")
			}
		}

		err := runAIAgent(req.Instruction, cfg, cfg.BrowserPath, dl, req.Window, req.Mode,
			func(p AIProgress) {
				send(map[string]interface{}{
					"type": "step", "step": p.Step, "total": p.Total,
					"action": p.Action, "message": p.Message,
				})
			})
		if err != nil {
			send(map[string]interface{}{"type": "error", "message": err.Error()})
		} else {
			send(map[string]interface{}{"type": "done", "message": "任务已完成"})
		}
	})

	// 流程列表
	mux.HandleFunc("/api/flows", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		flows := s.flows
		s.mu.Unlock()
		if flows == nil { flows = []Flow{} }
		writeJSON(w, map[string]interface{}{"flows": flows})
	})

	return mux
}


// consoleURL 记录控制台实际监听地址（端口被占用时会自动换端口）
var consoleURL string

const consolePort = "18760"

// probeConsole 判断 127.0.0.1:port 上是否已经有我们自己的控制台在运行
func probeConsole(port string) bool {
	client := &http.Client{Timeout: 800 * time.Millisecond}
	resp, err := client.Get("http://127.0.0.1:" + port + "/api/config")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return false
	}
	var c AIConfig
	return json.NewDecoder(resp.Body).Decode(&c) == nil
}

// 启动 Web 服务并打开浏览器
func serveWeb() error {
	s := newServer()
	s.loadConfig()

	// 1) 已有控制台在运行（上次没关 / 重复双击）：直接打开它
	if probeConsole(consolePort) {
		consoleURL = "http://127.0.0.1:" + consolePort
		openBrowser(consoleURL)
		return nil
	}

	// 2) 首选 18760；被占用或被系统保留时自动换空闲端口
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

	srv := &http.Server{
		Handler:           s.routes(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	// 把控制台地址写到 exe 同目录，浏览器没自动弹出时可手动打开
	if dir := filepath.Dir(s.cfgPath); dir != "" {
		_ = os.WriteFile(filepath.Join(dir, "neuralflow-url.txt"),
			[]byte("\ufeffNeuralFlow 控制台正在运行，如果浏览器没有自动打开，\r\n请复制下面的地址到浏览器地址栏：\r\n\r\n"+consoleURL+"\r\n"), 0644)
	}
	go openBrowser(consoleURL) // 监听成功后再打开浏览器，避免打开一个打不开的页面
	return srv.Serve(ln)
}

func openBrowser(url string) {
	time.Sleep(400 * time.Millisecond)
	if _, err := os.Stat("/usr/bin/xdg-open"); err == nil {
		exec.Command("xdg-open", url).Start()
		return
	}
	exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
}
