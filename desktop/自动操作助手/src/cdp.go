package main

// 后台浏览器自动化内核：通过 Chrome DevTools Protocol 控制一个独立的无头浏览器。
// 所有操作都发生在浏览器内部（JS / CDP），不会向 Windows 发送鼠标键盘事件，
// 因此用户在电脑上的正常操作完全不受影响。

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ===== 最小 WebSocket 客户端 =====

type wsResponse struct {
	data []byte
	err  error
}

type wsConn struct {
	conn    net.Conn
	br      *bufio.Reader
	mu      sync.Mutex
	pending map[int]chan wsResponse
	nextID  int
	closed  chan struct{}
}

func dialWebSocket(rawURL string) (*wsConn, error) {
	if !strings.HasPrefix(rawURL, "ws://") {
		return nil, fmt.Errorf("仅支持 ws:// 本地调试地址: %s", rawURL)
	}
	rest := strings.TrimPrefix(rawURL, "ws://")
	slash := strings.Index(rest, "/")
	host, path := rest, "/"
	if slash >= 0 {
		host, path = rest[:slash], rest[slash:]
	}
	if !strings.Contains(host, ":") {
		host += ":80"
	}
	c, err := net.DialTimeout("tcp", host, 5*time.Second)
	if err != nil {
		return nil, err
	}
	key := make([]byte, 16)
	if _, err := rand.Read(key); err != nil {
		c.Close()
		return nil, err
	}
	req := "GET " + path + " HTTP/1.1\r\n" +
		"Host: " + host + "\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Key: " + base64.StdEncoding.EncodeToString(key) + "\r\n" +
		"Sec-WebSocket-Version: 13\r\n\r\n"
	if _, err := io.WriteString(c, req); err != nil {
		c.Close()
		return nil, err
	}
	br := bufio.NewReader(c)
	status := ""
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			c.Close()
			return nil, err
		}
		if status == "" {
			status = strings.TrimSpace(line)
		}
		if line == "\r\n" {
			break
		}
	}
	if !strings.Contains(status, " 101") {
		c.Close()
		return nil, fmt.Errorf("WebSocket 握手失败: %s", status)
	}
	ws := &wsConn{conn: c, br: br, pending: make(map[int]chan wsResponse), closed: make(chan struct{})}
	go ws.readLoop()
	return ws, nil
}

func (w *wsConn) close() {
	select {
	case <-w.closed:
		return
	default:
		close(w.closed)
		w.conn.Close()
	}
}

func (w *wsConn) readLoop() {
	defer w.close()
	for {
		hdr := make([]byte, 2)
		if _, err := io.ReadFull(w.br, hdr); err != nil {
			return
		}
		opcode := hdr[0] & 0x0f
		masked := hdr[1]&0x80 != 0
		length := uint64(hdr[1] & 0x7f)
		if length == 126 {
			b := make([]byte, 2)
			if _, err := io.ReadFull(w.br, b); err != nil {
				return
			}
			length = uint64(binary.BigEndian.Uint16(b))
		} else if length == 127 {
			b := make([]byte, 8)
			if _, err := io.ReadFull(w.br, b); err != nil {
				return
			}
			length = binary.BigEndian.Uint64(b)
		}
		var mask []byte
		if masked {
			mask = make([]byte, 4)
			if _, err := io.ReadFull(w.br, mask); err != nil {
				return
			}
		}
		if length > 32<<20 {
			return
		}
		payload := make([]byte, length)
		if _, err := io.ReadFull(w.br, payload); err != nil {
			return
		}
		if masked {
			for i := range payload {
				payload[i] ^= mask[i%4]
			}
		}
		switch opcode {
		case 0x8: // close
			return
		case 0x9: // ping -> pong
			w.writeFrame(0xA, payload)
		case 0x1, 0x2:
			var env struct {
				ID     int             `json:"id"`
				Result json.RawMessage `json:"result"`
				Error  json.RawMessage `json:"error"`
			}
			if err := json.Unmarshal(payload, &env); err != nil || env.ID == 0 {
				continue
			}
			w.mu.Lock()
			ch := w.pending[env.ID]
			delete(w.pending, env.ID)
			w.mu.Unlock()
			if ch == nil {
				continue
			}
			if len(env.Error) > 0 && string(env.Error) != "null" {
				ch <- wsResponse{err: fmt.Errorf("CDP 错误: %s", env.Error)}
			} else {
				ch <- wsResponse{data: env.Result}
			}
		}
	}
}

func (w *wsConn) writeFrame(opcode byte, data []byte) error {
	mask := make([]byte, 4)
	if _, err := rand.Read(mask); err != nil {
		return err
	}
	n := len(data)
	header := []byte{0x80 | opcode}
	switch {
	case n < 126:
		header = append(header, 0x80|byte(n))
	case n < 1<<16:
		header = append(header, 0x80|126, byte(n>>8), byte(n))
	default:
		header = append(header, 0x80|127)
		b := make([]byte, 8)
		binary.BigEndian.PutUint64(b, uint64(n))
		header = append(header, b...)
	}
	header = append(header, mask...)
	masked := make([]byte, n)
	for i := range data {
		masked[i] = data[i] ^ mask[i%4]
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	select {
	case <-w.closed:
		return errors.New("WebSocket 已关闭")
	default:
	}
	if _, err := w.conn.Write(append(header, masked...)); err != nil {
		return err
	}
	return nil
}

// call 向 CDP 发送一条命令并等待对应 id 的响应
func (w *wsConn) call(method string, params map[string]interface{}, sessionID string, timeout time.Duration) ([]byte, error) {
	w.mu.Lock()
	w.nextID++
	id := w.nextID
	ch := make(chan wsResponse, 1)
	w.pending[id] = ch
	w.mu.Unlock()
	defer func() {
		w.mu.Lock()
		delete(w.pending, id)
		w.mu.Unlock()
	}()

	msg := map[string]interface{}{"id": id, "method": method}
	if params != nil {
		msg["params"] = params
	}
	if sessionID != "" {
		msg["sessionId"] = sessionID
	}
	raw, err := json.Marshal(msg)
	if err != nil {
		return nil, err
	}
	if err := w.writeFrame(0x1, raw); err != nil {
		return nil, err
	}
	select {
	case res := <-ch:
		return res.data, res.err
	case <-time.After(timeout):
		return nil, fmt.Errorf("CDP 命令超时: %s", method)
	case <-w.closed:
		return nil, errors.New("CDP 连接已关闭")
	}
}

// ===== 后台浏览器会话 =====

type bgBrowser struct {
	ws          *wsConn
	cmd         *exec.Cmd
	cancel      context.CancelFunc
	sessionID   string
	downloadDir string
	profileDir  string
}

// findBrowser 返回本机可用的 Chrome / Edge 路径；pathHint 非空时优先使用
func findBrowser(pathHint string) (string, error) {
	if strings.TrimSpace(pathHint) != "" {
		if _, err := os.Stat(pathHint); err == nil {
			return pathHint, nil
		}
		return "", fmt.Errorf("找不到指定的浏览器: %s", pathHint)
	}
	pf := os.Getenv("ProgramFiles")
	pf86 := os.Getenv("ProgramFiles(x86)")
	local := os.Getenv("LOCALAPPDATA")
	candidates := []string{
		filepath.Join(pf, "Google", "Chrome", "Application", "chrome.exe"),
		filepath.Join(pf86, "Google", "Chrome", "Application", "chrome.exe"),
		filepath.Join(local, "Google", "Chrome", "Application", "chrome.exe"),
		filepath.Join(pf, "Microsoft", "Edge", "Application", "msedge.exe"),
		filepath.Join(pf86, "Microsoft", "Edge", "Application", "msedge.exe"),
	}
	for _, p := range candidates {
		if p == "" || strings.HasPrefix(p, string(filepath.Separator)) && pf == "" {
			continue
		}
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", errors.New("未找到 Chrome / Edge，请在界面里填写浏览器路径")
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return port, nil
}

func startBackgroundBrowser(browserPath, downloadDir string) (*bgBrowser, error) {
	if err := os.MkdirAll(downloadDir, 0755); err != nil {
		return nil, err
	}
	profile, err := os.MkdirTemp("", "zidongcaozuo-profile-")
	if err != nil {
		return nil, err
	}
	port, err := freePort()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	args := []string{
		"--headless=new", "--no-sandbox", "--disable-dev-shm-usage", "--disable-gpu",
		"--no-first-run", "--no-default-browser-check",
		"--disable-extensions", "--disable-background-networking",
		fmt.Sprintf("--remote-debugging-port=%d", port),
		"--user-data-dir=" + profile,
		"--window-size=1280,900", "about:blank",
	}
	cmd := exec.CommandContext(ctx, browserPath, args...)
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			return cmd.Process.Kill()
		}
		return nil
	}
	if err := cmd.Start(); err != nil {
		cancel()
		os.RemoveAll(profile)
		return nil, err
	}

	var ver struct {
		WSURL string `json:"webSocketDebuggerUrl"`
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/json/version", port))
		if err == nil {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if err := json.Unmarshal(body, &ver); err == nil && ver.WSURL != "" {
				break
			}
		}
		if time.Now().After(deadline) {
			cancel()
			os.RemoveAll(profile)
			return nil, errors.New("浏览器调试端口启动超时")
		}
		time.Sleep(150 * time.Millisecond)
	}

	ws, err := dialWebSocket(ver.WSURL)
	if err != nil {
		cancel()
		os.RemoveAll(profile)
		return nil, err
	}
	b := &bgBrowser{ws: ws, cmd: cmd, cancel: cancel, downloadDir: downloadDir, profileDir: profile}

	targetRaw, err := ws.call("Target.createTarget", map[string]interface{}{"url": "about:blank"}, "", 15*time.Second)
	if err != nil {
		b.Close()
		return nil, err
	}
	var target struct{ TargetID string `json:"targetId"` }
	json.Unmarshal(targetRaw, &target)
	attachRaw, err := ws.call("Target.attachToTarget", map[string]interface{}{"targetId": target.TargetID, "flatten": true}, "", 15*time.Second)
	if err != nil {
		b.Close()
		return nil, err
	}
	var attached struct{ SessionID string `json:"sessionId"` }
	json.Unmarshal(attachRaw, &attached)
	b.sessionID = attached.SessionID

	for _, m := range []string{"Page.enable", "Runtime.enable", "DOM.enable"} {
		if _, err := ws.call(m, nil, b.sessionID, 15*time.Second); err != nil {
			b.Close()
			return nil, err
		}
	}
	// 浏览器级：允许下载到指定目录，且不要弹出保存对话框
	_, err = ws.call("Browser.setDownloadBehavior", map[string]interface{}{
		"behavior": "allow", "downloadPath": downloadDir, "eventsEnabled": true,
	}, "", 15*time.Second)
	if err != nil {
		b.Close()
		return nil, err
	}
	return b, nil
}

func (b *bgBrowser) Close() {
	if b == nil {
		return
	}
	if b.ws != nil {
		b.ws.close()
	}
	if b.cancel != nil {
		b.cancel()
	}
	time.Sleep(300 * time.Millisecond)
	if b.profileDir != "" {
		os.RemoveAll(b.profileDir)
	}
}

func jsonQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// eval 执行 JS 并返回 JSON 值
func (b *bgBrowser) eval(expr string) (interface{}, error) {
	raw, err := b.ws.call("Runtime.evaluate", map[string]interface{}{
		"expression": expr, "returnByValue": true, "awaitPromise": true,
	}, b.sessionID, 60*time.Second)
	if err != nil {
		return nil, err
	}
	var reply struct {
		Result struct {
			Value   interface{} `json:"value"`
			Type    string      `json:"type"`
			Subtype string      `json:"subtype"`
		} `json:"result"`
		Exception interface{} `json:"exceptionDetails"`
	}
	if err := json.Unmarshal(raw, &reply); err != nil {
		return nil, err
	}
	if reply.Exception != nil {
		return nil, fmt.Errorf("JS 执行异常: %v", reply.Exception)
	}
	return reply.Result.Value, nil
}

func (b *bgBrowser) openURL(url string) error {
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") && !strings.HasPrefix(url, "file://") {
		url = "https://" + url
	}
	_, err := b.ws.call("Page.navigate", map[string]interface{}{"url": url}, b.sessionID, 30*time.Second)
	if err != nil {
		return err
	}
	deadline := time.Now().Add(45 * time.Second)
	for {
		v, err := b.eval("document.readyState")
		if err == nil && v == "complete" {
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("网页加载超时")
		}
		time.Sleep(300 * time.Millisecond)
	}
}

func (b *bgBrowser) click(selector, text string) error {
	expr := fmt.Sprintf(`(function(){
		var sel=%s, txt=%s, el=null;
		if(sel){ try{ el=document.querySelector(sel); }catch(e){} }
		if(!el && txt){
			var all=document.querySelectorAll('a,button,input[type=submit],div,span,li');
			for(var i=0;i<all.length;i++){
				var t=(all[i].innerText||all[i].value||'').trim();
				if(t.indexOf(txt)>=0){ el=all[i]; break; }
			}
		}
		if(!el) return '未找到元素: '+(sel||txt);
		el.scrollIntoView({block:'center'});
		el.click();
		return 'ok';
	})()`, jsonQuote(selector), jsonQuote(text))
	v, err := b.eval(expr)
	if err != nil {
		return err
	}
	if s, ok := v.(string); ok && s != "ok" {
		return errors.New(s)
	}
	return nil
}

func (b *bgBrowser) inputText(selector, text string) error {
	expr := fmt.Sprintf(`(function(){
		var el=document.querySelector(%s);
		if(!el) return '未找到输入框: '+%s;
		if(el.isContentEditable){ el.innerText=%s; }
		else { el.value=%s; }
		el.dispatchEvent(new Event('input',{bubbles:true}));
		el.dispatchEvent(new Event('change',{bubbles:true}));
		return 'ok';
	})()`, jsonQuote(selector), jsonQuote(selector), jsonQuote(text), jsonQuote(text))
	v, err := b.eval(expr)
	if err != nil {
		return err
	}
	if s, ok := v.(string); ok && s != "ok" {
		return errors.New(s)
	}
	return nil
}

func snapshotDir(dir string) map[string]int64 {
	m := make(map[string]int64)
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.IsDir() || strings.HasSuffix(e.Name(), ".crdownload") {
			continue
		}
		if info, err := e.Info(); err == nil {
			m[e.Name()] = info.Size()
		}
	}
	return m
}

// download 触发下载（直接导航到资源地址），等待文件落盘后可选复制到目标文件夹
func (b *bgBrowser) waitDownload(before map[string]int64, saveDir string, timeoutSec int) (string, error) {
	if timeoutSec <= 0 {
		timeoutSec = 90
	}
	deadline := time.Now().Add(time.Duration(timeoutSec) * time.Second)
	var found string
	for time.Now().Before(deadline) {
		after := snapshotDir(b.downloadDir)
		for name, size := range after {
			if _, existed := before[name]; !existed && size > 0 {
				time.Sleep(500 * time.Millisecond)
				info, err := os.Stat(filepath.Join(b.downloadDir, name))
				if err == nil && info.Size() == size {
					found = filepath.Join(b.downloadDir, name)
					break
				}
			}
		}
		if found != "" {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if found == "" {
		return "", errors.New("等待下载超时；请检查网址或下载按钮")
	}
	if saveDir != "" {
		if err := os.MkdirAll(saveDir, 0755); err == nil {
			target := filepath.Join(saveDir, filepath.Base(found))
			if data, err := os.ReadFile(found); err == nil {
				os.WriteFile(target, data, 0644)
				found = target
			}
		}
	}
	return found, nil
}

func (b *bgBrowser) download(url, saveDir string, timeoutSec int) (string, error) {
	before := snapshotDir(b.downloadDir)
	if err := b.openURL(url); err != nil {
		return "", err
	}
	return b.waitDownload(before, saveDir, timeoutSec)
}

// downloadByClick 打开页面后点击下载按钮，再等待文件落盘
func (b *bgBrowser) downloadByClick(url, selector, saveDir string, timeoutSec int) (string, error) {
	if err := b.openURL(url); err != nil {
		return "", err
	}
	before := snapshotDir(b.downloadDir)
	if err := b.click(selector, ""); err != nil {
		return "", err
	}
	return b.waitDownload(before, saveDir, timeoutSec)
}

// upload 给 file input 设置本地文件（不弹系统文件对话框），然后可点提交按钮
func (b *bgBrowser) upload(selector, filePath, submitSelector string) error {
	abs, err := filepath.Abs(filePath)
	if err != nil {
		return err
	}
	if _, err := os.Stat(abs); err != nil {
		return fmt.Errorf("上传文件不存在: %s", abs)
	}
	rootRaw, err := b.ws.call("DOM.getDocument", map[string]interface{}{"depth": -1}, b.sessionID, 30*time.Second)
	if err != nil {
		return err
	}
	var doc struct {
		Root struct{ NodeID int `json:"nodeId"` } `json:"root"`
	}
	if err := json.Unmarshal(rootRaw, &doc); err != nil {
		return err
	}
	qRaw, err := b.ws.call("DOM.querySelector", map[string]interface{}{
		"nodeId": doc.Root.NodeID, "selector": selector,
	}, b.sessionID, 30*time.Second)
	if err != nil {
		return err
	}
	var q struct{ NodeID int `json:"nodeId"` }
	if err := json.Unmarshal(qRaw, &q); err != nil {
		return err
	}
	if q.NodeID == 0 {
		return fmt.Errorf("未找到上传控件: %s", selector)
	}
	if _, err := b.ws.call("DOM.setFileInputFiles", map[string]interface{}{
		"nodeId": q.NodeID, "files": []string{abs},
	}, b.sessionID, 30*time.Second); err != nil {
		return err
	}
	if strings.TrimSpace(submitSelector) != "" {
		time.Sleep(500 * time.Millisecond)
		return b.click(submitSelector, "")
	}
	return nil
}
