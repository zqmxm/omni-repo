package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"
)

// ===== Win32 API 声明 =====
var (
	user32   = syscall.NewLazyDLL("user32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	shell32  = syscall.NewLazyDLL("shell32.dll")

	pRegisterClassEx    = user32.NewProc("RegisterClassExW")
	pCreateWindowEx     = user32.NewProc("CreateWindowExW")
	pShowWindow         = user32.NewProc("ShowWindow")
	pUpdateWindow       = user32.NewProc("UpdateWindow")
	pGetMessage         = user32.NewProc("GetMessageW")
	pTranslateMessage   = user32.NewProc("TranslateMessage")
	pDispatchMessage    = user32.NewProc("DispatchMessageW")
	pPostQuitMessage    = user32.NewProc("PostQuitMessage")
	pDefWindowProc      = user32.NewProc("DefWindowProcW")
	pSendMessage        = user32.NewProc("SendMessageW")
	pSetWindowText      = user32.NewProc("SetWindowTextW")
	pGetWindowText      = user32.NewProc("GetWindowTextW")
	pGetWindowTextLen   = user32.NewProc("GetWindowTextLengthW")
	pSetFocus           = user32.NewProc("SetFocus")
	pSetWindowsHookEx   = user32.NewProc("SetWindowsHookExW")
	pUnhookWindowsHook  = user32.NewProc("UnhookWindowsHookEx")
	pCallNextHook       = user32.NewProc("CallNextHookEx")
	pGetCursorPos       = user32.NewProc("GetCursorPos")
	pSetCursorPos       = user32.NewProc("SetCursorPos")
	pGetWindowRect      = user32.NewProc("GetWindowRect")
	pGetForegroundWin   = user32.NewProc("GetForegroundWindow")
	pRegisterHotKey     = user32.NewProc("RegisterHotKey")
	pUnregisterHotKey   = user32.NewProc("UnregisterHotKey")
	pMouseEvent         = user32.NewProc("mouse_event")
	pKeybdEvent         = user32.NewProc("keybd_event")
	pSetProcessDPIAware = user32.NewProc("SetProcessDPIAware")
	pOpenClipboard      = user32.NewProc("OpenClipboard")
	pCloseClipboard     = user32.NewProc("CloseClipboard")
	pEmptyClipboard     = user32.NewProc("EmptyClipboard")
	pSetClipboardData   = user32.NewProc("SetClipboardData")
	pGlobalAlloc        = kernel32.NewProc("GlobalAlloc")
	pGlobalLock         = kernel32.NewProc("GlobalLock")
	pGlobalUnlock       = kernel32.NewProc("GlobalUnlock")
	pShellExecute       = shell32.NewProc("ShellExecuteW")
)

const (
	wmDestroy  = 0x0002
	wmCommand  = 0x0111
	wmHotkey   = 0x0312
	wmCreate   = 0x0001
	wmClose    = 0x0010
	wsChild    = 0x40000000
	wsVisible  = 0x10000000
	wsTabStop  = 0x00010000
	wsBorder   = 0x00800000
	wsVScroll  = 0x00200000
	esAutoHScroll = 0x00000080
	lbsNotify  = 0x00000001
	swShow     = 5
	lbAddString = 0x0180
	lbResetContent = 0x0184
	lbGetCount  = 0x018B

	whKeyboardLL = 13
	whMouseLL    = 14
	hcAction     = 0

	vkF9 = 0x78
	vkF10 = 0x79
	vkF11 = 0x7A
	vkControl = 0x11
	vkShift = 0x10
	vkAlt = 0x12
	vkEscape = 0x1B
	vkReturn = 0x0D
	vkTab = 0x09
	vkSpace = 0x20
	vkBack = 0x08
	vkDelete = 0x2E
	vkLeft = 0x25
	vkUp = 0x26
	vkRight = 0x27
	vkDown = 0x28

	mouseMove = 0x0001
	leftDown  = 0x0002
	leftUp    = 0x0004
	rightDown = 0x0008
	rightUp   = 0x0010
	mouseWheel = 0x0800
	keyUpFlag = 0x0002

	cfUnicodeText = 13
	gmemMoveable  = 0x0002
	swShowNormal  = 1
)

type point struct { X, Y int32 }
type rect struct { Left, Top, Right, Bottom int32 }
type msg struct {
	Hwnd uintptr
	Message uint32
	WParam uintptr
	LParam uintptr
	Time uint32
	Pt point
	Private uint32
}
type wndClassEx struct {
	Size uint32
	Style uint32
	WndProc uintptr
	ClsExtra int32
	WndExtra int32
	Instance uintptr
	Icon uintptr
	Cursor uintptr
	Background uintptr
	MenuName *uint16
	ClassName *uint16
	IconSm uintptr
}
type kbdHookStruct struct {
	VKCode uint32
	ScanCode uint32
	Flags uint32
	Time uint32
	ExtraInfo uintptr
}
type mouseHookStruct struct {
	Pt point
	MouseData uint32
	Flags uint32
	Time uint32
	ExtraInfo uintptr
}

// ===== 控件 ID =====
const (
	idRecord = 1001
	idStopRec = 1002
	idPlay = 1003
	idStopPlay = 1004
	idSave = 1005
	idLoad = 1006
	idClear = 1007
	idWait = 1008
	idAddText = 1009
	idAddURL = 1010
	idLoopEdit = 1011
	idTextEdit = 1012
	idURLEdit = 1013
	idList = 1014
	idStatus = 1015
	idSelEdit = 1016
	idSaveDirEdit = 1017
	idUploadEdit = 1018
	idSubmitEdit = 1019
	idTimeoutEdit = 1020
	idBrowserEdit = 1021
	idAddWebOpen = 1022
	idAddWebClick = 1023
	idAddWebInput = 1024
	idAddWebDownload = 1025
	idAddWebUpload = 1026
	idWebURLEdit = 1027
)

// ===== 动作数据 =====
type Action struct {
	Kind string `json:"kind"` // move,ldown,lup,rdown,rup,wheel,kdown,kup,wait,text,url
	X int `json:"x,omitempty"`
	Y int `json:"y,omitempty"`
	VK int `json:"vk,omitempty"`
	Delta int32 `json:"delta,omitempty"`
	Text string `json:"text,omitempty"`
	Target string `json:"target,omitempty"` // 网址或 CSS 选择器
	File string `json:"file,omitempty"`     // 上传/下载保存路径
	Submit string `json:"submit,omitempty"` // 上传后提交按钮选择器
	Seconds int `json:"seconds,omitempty"`  // 下载超时秒数
	Delay int64 `json:"delay"`
}

var (
	actions []Action
	hwndMain uintptr
	hList, hStatus, hLoopEdit, hTextEdit, hURLEdit uintptr
	hSelEdit, hSaveDirEdit, hUploadEdit, hSubmitEdit, hTimeoutEdit, hBrowserEdit, hWebURLEdit uintptr
	browserPathUsed string
	hHookMouse, hHookKbd uintptr
	recording atomic.Bool
	playing atomic.Bool
	stopPlay atomic.Bool
	lastRecordMS int64
	lastMoveMS int64
	lastMoveX, lastMoveY int32
	loopCount int = 0
	scriptPath string
	wndProcCallback uintptr
	kbdCallback uintptr
	mouseCallback uintptr
)

func utf16ptr(s string) *uint16 { p, _ := syscall.UTF16PtrFromString(s); return p }

func getText(h uintptr) string {
	n, _, _ := pGetWindowTextLen.Call(h)
	buf := make([]uint16, n+2)
	pGetWindowText.Call(h, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return syscall.UTF16ToString(buf)
}

func setText(h uintptr, s string) {
	pSetWindowText.Call(h, uintptr(unsafe.Pointer(utf16ptr(s))))
}

func listAdd(s string) {
	pSendMessage.Call(hList, lbAddString, 0, uintptr(unsafe.Pointer(utf16ptr(s))))
}

func refreshList() {
	pSendMessage.Call(hList, lbResetContent, 0, 0)
	for _, a := range actions {
		listAdd(describe(a))
	}
}

func describe(a Action) string {
	switch a.Kind {
	case "move": return fmt.Sprintf("移动鼠标 → (%d, %d)", a.X, a.Y)
	case "ldown": return fmt.Sprintf("按下左键 (%d, %d)", a.X, a.Y)
	case "lup": return fmt.Sprintf("松开左键 (%d, %d)", a.X, a.Y)
	case "rdown": return fmt.Sprintf("按下右键 (%d, %d)", a.X, a.Y)
	case "rup": return fmt.Sprintf("松开右键 (%d, %d)", a.X, a.Y)
	case "wheel": return fmt.Sprintf("滚轮 %d", a.Delta)
	case "kdown": return fmt.Sprintf("按下按键 [%s]", keyName(a.VK))
	case "kup": return fmt.Sprintf("松开按键 [%s]", keyName(a.VK))
	case "wait": return fmt.Sprintf("等待 %d 毫秒", a.Delay)
	case "text": return "输入文本: " + a.Text
	case "url": return "打开网址(前台): " + a.Text
	case "webopen": return "🌐 后台打开: " + a.Target
	case "webclick": return "🌐 后台点击: " + firstNonEmpty(a.Target, "文字="+a.Text)
	case "webinput": return "🌐 后台填写: " + a.Target + " ← " + a.Text
	case "webdownload": return "🌐 后台下载: " + a.Target + " → " + a.File
	case "webdownloadclick": return "🌐 后台点击下载: " + a.Target + " [" + a.Submit + "] → " + a.File
	case "webupload": return "🌐 后台上传: " + a.File + " → " + a.Target
	case "webwait": return "🌐 后台等待 " + fmt.Sprintf("%d", a.Delay/1000) + " 秒"
	}
	return a.Kind
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" { return v }
	}
	return ""
}

func addWebAction(kind string) {
	url := strings.TrimSpace(getText(hWebURLEdit))
	sel := strings.TrimSpace(getText(hSelEdit))
	text := getText(hTextEdit)
	saveDir := strings.TrimSpace(getText(hSaveDirEdit))
	upload := strings.TrimSpace(getText(hUploadEdit))
	submit := strings.TrimSpace(getText(hSubmitEdit))
	secs := 60
	fmt.Sscanf(getText(hTimeoutEdit), "%d", &secs)

	a := Action{Kind: kind}
	switch kind {
	case "webopen":
		if url == "" { setText(hStatus, "请先填写网址"); return }
		a.Target = url
	case "webclick":
		if sel == "" && strings.TrimSpace(text) == "" { setText(hStatus, "请填写元素选择器或按钮文字"); return }
		a.Target, a.Text = sel, text
	case "webinput":
		if sel == "" { setText(hStatus, "请填写输入框选择器"); return }
		a.Target, a.Text = sel, text
	case "webdownload":
		if url == "" { setText(hStatus, "请填写下载地址"); return }
		a.Target, a.File, a.Seconds = url, saveDir, secs
		if sel != "" {
			a.Kind = "webdownloadclick"
			a.Submit = sel
		}
	case "webupload":
		if sel == "" || upload == "" { setText(hStatus, "请填写上传控件选择器和文件路径"); return }
		a.Target, a.File, a.Submit = sel, upload, submit
	}
	now := time.Now().UnixMilli()
	d := now - lastRecordMS
	if lastRecordMS == 0 { d = 0 }
	if d > 3000 { d = 3000 }
	lastRecordMS = now
	a.Delay = d
	actions = append(actions, a)
	listAdd(describe(a))
	setText(hStatus, "已添加：" + describe(a) + "（启动后后台执行，不占用鼠标键盘）")
}

func keyName(vk int) string {
	switch vk {
	case vkControl: return "Ctrl"
	case vkShift: return "Shift"
	case vkAlt: return "Alt"
	case vkReturn: return "Enter"
	case vkTab: return "Tab"
	case vkSpace: return "空格"
	case vkBack: return "退格"
	case vkDelete: return "Delete"
	case vkLeft: return "←"
	case vkUp: return "↑"
	case vkRight: return "→"
	case vkDown: return "↓"
	case vkF9: return "F9"
	case vkF10: return "F10"
	case vkF11: return "F11"
	}
	if vk >= '0' && vk <= '9' || vk >= 'A' && vk <= 'Z' { return string(rune(vk)) }
	return fmt.Sprintf("VK_%d", vk)
}

func inWindow(x, y int32) bool {
	var r rect
	pGetWindowRect.Call(hwndMain, uintptr(unsafe.Pointer(&r)))
	return x >= r.Left && x <= r.Right && y >= r.Top && y <= r.Bottom
}

func record(kind string, x, y int, vk, delta int, text string) {
	if !recording.Load() || playing.Load() { return }
	now := time.Now().UnixMilli()
	d := now - lastRecordMS
	if lastRecordMS == 0 { d = 0 }
	if d > 3000 { d = 3000 }
	lastRecordMS = now
	actions = append(actions, Action{Kind: kind, X: x, Y: y, VK: vk, Delta: int32(delta), Text: text, Delay: d})
	listAdd(describe(actions[len(actions)-1]))
}

func manualAction(kind, text string) {
	now := time.Now().UnixMilli()
	d := now - lastRecordMS
	if lastRecordMS == 0 { d = 0 }
	if d > 3000 { d = 3000 }
	lastRecordMS = now
	a := Action{Kind: kind, Text: text, Delay: d}
	actions = append(actions, a)
	listAdd(describe(a))
}

func mouseProc(nCode int32, wParam uintptr, lParam uintptr) uintptr {
	if nCode >= hcAction && recording.Load() && !playing.Load() {
		m := (*mouseHookStruct)(unsafe.Pointer(lParam))
		if !inWindow(m.Pt.X, m.Pt.Y) {
			switch wParam {
			case 0x0200: // WM_MOUSEMOVE
				now := time.Now().UnixMilli()
				dx := m.Pt.X - lastMoveX
				dy := m.Pt.Y - lastMoveY
				if now-lastMoveMS > 40 && (dx*dx+dy*dy) > 16 {
					record("move", int(m.Pt.X), int(m.Pt.Y), 0, 0, "")
					lastMoveMS, lastMoveX, lastMoveY = now, m.Pt.X, m.Pt.Y
				}
			case 0x0201: record("ldown", int(m.Pt.X), int(m.Pt.Y), 0, 0, "")
			case 0x0202: record("lup", int(m.Pt.X), int(m.Pt.Y), 0, 0, "")
			case 0x0204: record("rdown", int(m.Pt.X), int(m.Pt.Y), 0, 0, "")
			case 0x0205: record("rup", int(m.Pt.X), int(m.Pt.Y), 0, 0, "")
			case 0x020A:
				d := int16(m.MouseData >> 16)
				record("wheel", int(m.Pt.X), int(m.Pt.Y), 0, int(d), "")
			}
		}
	}
	r, _, _ := pCallNextHook.Call(hHookMouse, uintptr(nCode), wParam, lParam)
	return r
}

func kbdProc(nCode int32, wParam uintptr, lParam uintptr) uintptr {
	if nCode >= hcAction && recording.Load() && !playing.Load() {
		k := (*kbdHookStruct)(unsafe.Pointer(lParam))
		fg, _, _ := pGetForegroundWin.Call()
		if fg != hwndMain {
			vk := int(k.VKCode)
			if vk == vkF9 || vk == vkF10 || vk == vkF11 { goto next }
			if wParam == 0x0100 || wParam == 0x0104 { // KEYDOWN / SYSKEYDOWN
				record("kdown", 0, 0, vk, 0, "")
			} else if wParam == 0x0101 || wParam == 0x0105 {
				record("kup", 0, 0, vk, 0, "")
			}
		}
	}
next:
	r, _, _ := pCallNextHook.Call(hHookKbd, uintptr(nCode), wParam, lParam)
	return r
}

func startRecord() {
	if playing.Load() { return }
	lastRecordMS, lastMoveMS = 0, 0
	recording.Store(true)
	setText(hStatus, "● 正在录制（动作追加到列表末尾；按 F9 停止）")
}

func stopRecord() {
	if !recording.Load() { return }
	recording.Store(false)
	lastRecordMS = 0
	setText(hStatus, fmt.Sprintf("■ 已停止录制，共 %d 个动作。可点「保存脚本」，或点「启动」试跑。", len(actions)))
}

func keyTap(vk int) {
	pKeybdEvent.Call(uintptr(vk), 0, 0, 0)
	time.Sleep(30 * time.Millisecond)
	pKeybdEvent.Call(uintptr(vk), 0, keyUpFlag, 0)
	time.Sleep(30 * time.Millisecond)
}

func pasteText(text string) {
	if r, _, _ := pOpenClipboard.Call(0); r == 0 { return }
	defer pCloseClipboard.Call()
	pEmptyClipboard.Call()
	u, err := syscall.UTF16FromString(text)
	if err != nil { return }
	bytes := len(u) * 2
	hMem, _, _ := pGlobalAlloc.Call(gmemMoveable, uintptr(bytes))
	if hMem == 0 { return }
	ptr, _, _ := pGlobalLock.Call(hMem)
	copy((*[1 << 28]uint16)(unsafe.Pointer(ptr))[:len(u)], u)
	pGlobalUnlock.Call(hMem)
	pSetClipboardData.Call(cfUnicodeText, hMem)

	pKeybdEvent.Call(vkControl, 0, 0, 0)
	pKeybdEvent.Call('V', 0, 0, 0)
	time.Sleep(40 * time.Millisecond)
	pKeybdEvent.Call('V', 0, keyUpFlag, 0)
	pKeybdEvent.Call(vkControl, 0, keyUpFlag, 0)
	time.Sleep(80 * time.Millisecond)
}

func sleepStop(ms int64) bool {
	for ms > 0 {
		if stopPlay.Load() { return false }
		step := int64(50)
		if ms < step { step = ms }
		time.Sleep(time.Duration(step) * time.Millisecond)
		ms -= step
	}
	return !stopPlay.Load()
}

func replay() {
	if playing.Load() || recording.Load() { return }
	if len(actions) == 0 {
		if hwndMain != 0 { setText(hStatus, "请先录制或添加动作，再点启动") }
		return
	}
	playing.Store(true)
	stopPlay.Store(false)
	loops := loopCount
	defer func() {
		playing.Store(false)
		if hwndMain != 0 {
			setText(hStatus, "■ 回放结束（按 F10 可再次启动）")
		}
	}()
	setText(hStatus, "▶ 正在自动运行…（按 F11 紧急停止）")
	browserPathUsed = getText(hBrowserEdit)
	var bg *bgBrowser
	var bgDir string
	ensureBG := func() error {
		if bg != nil { return nil }
		var err error
		bgDir, err = os.MkdirTemp("", "zidongcaozuo-dl-")
		if err != nil { return err }
		bg, err = startBackgroundBrowser(browserPathUsed, bgDir)
		return err
	}
	defer func() {
		if bg != nil { bg.Close() }
		if bgDir != "" { os.RemoveAll(bgDir) }
	}()

	for i := 0; (loops == 0 || i < loops) && !stopPlay.Load(); i++ {
		for _, a := range actions {
			if stopPlay.Load() { return }
			if a.Delay > 0 && !sleepStop(a.Delay) { return }
			if strings.HasPrefix(a.Kind, "web") && a.Kind != "webwait" {
				if err := ensureBG(); err != nil {
					setText(hStatus, "后台浏览器启动失败：" + err.Error())
					return
				}
			}
			var werr error
			switch a.Kind {
			case "webopen":
				werr = bg.openURL(a.Target)
			case "webclick":
				werr = bg.click(a.Target, a.Text)
			case "webinput":
				werr = bg.inputText(a.Target, a.Text)
			case "webdownload":
				var saved string
				saved, werr = bg.download(a.Target, a.File, a.Seconds)
				if werr == nil { setText(hStatus, "后台下载完成：" + saved) }
			case "webdownloadclick":
				var saved string
				saved, werr = bg.downloadByClick(a.Target, a.Submit, a.File, a.Seconds)
				if werr == nil { setText(hStatus, "后台下载完成：" + saved) }
			case "webupload":
				werr = bg.upload(a.Target, a.File, a.Submit)
				if werr == nil { setText(hStatus, "后台上传完成：" + a.File) }
			case "webwait":
				if !sleepStop(a.Delay) { return }
			case "move":
				pSetCursorPos.Call(uintptr(a.X), uintptr(a.Y))
			case "ldown":
				pSetCursorPos.Call(uintptr(a.X), uintptr(a.Y))
				pMouseEvent.Call(leftDown, 0, 0, 0, 0)
			case "lup":
				pSetCursorPos.Call(uintptr(a.X), uintptr(a.Y))
				pMouseEvent.Call(leftUp, 0, 0, 0, 0)
			case "rdown":
				pSetCursorPos.Call(uintptr(a.X), uintptr(a.Y))
				pMouseEvent.Call(rightDown, 0, 0, 0, 0)
			case "rup":
				pSetCursorPos.Call(uintptr(a.X), uintptr(a.Y))
				pMouseEvent.Call(rightUp, 0, 0, 0, 0)
			case "wheel":
				pMouseEvent.Call(mouseWheel, 0, 0, uintptr(uint32(a.Delta)), 0)
			case "kdown":
				pKeybdEvent.Call(uintptr(a.VK), 0, 0, 0)
			case "kup":
				pKeybdEvent.Call(uintptr(a.VK), 0, keyUpFlag, 0)
			case "wait":
				if !sleepStop(a.Delay) { return }
			case "text":
				pasteText(a.Text)
			case "url":
				pShellExecute.Call(hwndMain, uintptr(unsafe.Pointer(utf16ptr("open"))),
					uintptr(unsafe.Pointer(utf16ptr(a.Text))), 0, 0, swShowNormal)
				time.Sleep(1200 * time.Millisecond)
			}
			if werr != nil {
				setText(hStatus, "后台操作失败：" + werr.Error())
				return
			}
		}
		if loops == 0 || i < loops-1 {
			if !sleepStop(600) { return }
		}
	}
}

func addWait() {
	now := time.Now().UnixMilli()
	d := int64(1000)
	if lastRecordMS != 0 { d = now - lastRecordMS }
	actions = append(actions, Action{Kind: "wait", Delay: d})
	listAdd(describe(actions[len(actions)-1]))
	lastRecordMS = now
}

func saveScript() {
	if len(actions) == 0 { setText(hStatus, "没有可保存的动作"); return }
	f, err := os.Create(scriptPath)
	if err != nil { setText(hStatus, "保存失败: "+err.Error()); return }
	defer f.Close()
	json.NewEncoder(f).Encode(actions)
	setText(hStatus, "已保存脚本: "+scriptPath)
}

func loadScript() {
	f, err := os.Open(scriptPath)
	if err != nil { setText(hStatus, "没有找到脚本文件，请先保存"); return }
	defer f.Close()
	var a []Action
	if err := json.NewDecoder(f).Decode(&a); err != nil { setText(hStatus, "加载失败: "+err.Error()); return }
	actions = a
	refreshList()
	setText(hStatus, fmt.Sprintf("已加载 %d 个动作", len(actions)))
}

func wndProc(hwnd uintptr, m uint32, wParam uintptr, lParam uintptr) uintptr {
	switch m {
	case wmCommand:
		switch wParam & 0xffff {
		case idRecord: startRecord()
		case idStopRec: stopRecord()
		case idPlay:
			n, err := fmt.Sscanf(getText(hLoopEdit), "%d", &loopCount)
			if n != 1 || err != nil { loopCount = 0 }
			go replay()
		case idStopPlay:
			stopPlay.Store(true)
			recording.Store(false)
			setText(hStatus, "已请求停止")
		case idSave: saveScript()
		case idLoad: loadScript()
		case idClear:
			actions = nil
			refreshList()
			setText(hStatus, "已清空")
		case idWait:
			actions = append(actions, Action{Kind: "wait", Delay: 1000})
			listAdd("等待 1000 毫秒")
		case idAddText:
			t := strings.TrimSpace(getText(hTextEdit))
			if t == "" { setText(hStatus, "请先输入要写入的文本"); return 0 }
			manualAction("text", t)
			setText(hStatus, "已添加文本输入动作（回放时自动粘贴）")
		case idAddWebOpen: addWebAction("webopen")
		case idAddWebClick: addWebAction("webclick")
		case idAddWebInput: addWebAction("webinput")
		case idAddWebDownload: addWebAction("webdownload")
		case idAddWebUpload: addWebAction("webupload")
		case idAddURL:
			u := strings.TrimSpace(getText(hURLEdit))
			if u == "" { setText(hStatus, "请先输入网址"); return 0 }
			if !strings.HasPrefix(u, "http") { u = "https://" + u }
			manualAction("url", u)
			setText(hStatus, "已添加网址动作（回放时自动打开浏览器）")
		}
	case wmHotkey:
		switch wParam {
		case 1:
			if recording.Load() { stopRecord() } else { startRecord() }
		case 2:
			if playing.Load() { stopPlay.Store(true) } else {
				n, err := fmt.Sscanf(getText(hLoopEdit), "%d", &loopCount)
				if n != 1 || err != nil { loopCount = 0 }
				go replay()
			}
		case 3:
			stopPlay.Store(true)
			recording.Store(false)
		}
	case wmClose:
		if recording.Load() || playing.Load() {
			stopPlay.Store(true)
			recording.Store(false)
			return 0
		}
		if len(actions) > 0 { saveScript() }
		pPostQuitMessage.Call(0)
	case wmDestroy:
		pUnregisterHotKey.Call(hwndMain, 1)
		pUnregisterHotKey.Call(hwndMain, 2)
		pUnregisterHotKey.Call(hwndMain, 3)
		if hHookMouse != 0 { pUnhookWindowsHook.Call(hHookMouse) }
		if hHookKbd != 0 { pUnhookWindowsHook.Call(hHookKbd) }
	}
	r, _, _ := pDefWindowProc.Call(hwnd, uintptr(m), wParam, lParam)
	return r
}

func makeControl(class, text string, id int, x, y, w, h int, style uint32) uintptr {
	hw, _, _ := pCreateWindowEx.Call(0, uintptr(unsafe.Pointer(utf16ptr(class))),
		uintptr(unsafe.Pointer(utf16ptr(text))), uintptr(wsChild|wsVisible|style),
		uintptr(x), uintptr(y), uintptr(w), uintptr(h), hwndMain, uintptr(id), 0, 0)
	return hw
}

func main() {
	pSetProcessDPIAware.Call()
	hInst, _, _ := kernel32.NewProc("GetModuleHandleW").Call(0)

	wndProcCallback = syscall.NewCallback(wndProc)
	kbdCallback = syscall.NewCallback(kbdProc)
	mouseCallback = syscall.NewCallback(mouseProc)

	cls := wndClassEx{
		Size: uint32(unsafe.Sizeof(wndClassEx{})),
		Style: 0x0002 | 0x0008, // CS_HREDRAW|CS_VREDRAW
		WndProc: wndProcCallback,
		Instance: hInst,
		Background: 6, // COLOR_WINDOW
		ClassName: utf16ptr("AutoOpsWindowClass"),
	}
	pRegisterClassEx.Call(uintptr(unsafe.Pointer(&cls)))

	hwndMain, _, _ = pCreateWindowEx.Call(0,
		uintptr(unsafe.Pointer(utf16ptr("AutoOpsWindowClass"))),
		uintptr(unsafe.Pointer(utf16ptr("自动操作助手 - 录制与循环回放"))),
		0x00CF0000, // WS_OVERLAPPEDWINDOW
		80, 40, 700, 750, 0, 0, hInst, 0)

	makeControl("Static", "推荐后台模式：填写网址/选择器 → 添加后台动作 → 点「启动」。全程独立浏览器，不占用鼠标键盘。", 0, 16, 10, 660, 22, 0)
	makeControl("Static", "前台录制（可选）：F9 录制任意软件操作，再按 F10 重复；录制期间会占用键鼠。", 0, 16, 32, 660, 20, 0)
	makeControl("Static", "全局热键：F9 录制/停止    F10 启动/停止    F11 紧急停止", 0, 16, 50, 660, 22, 0)

	makeControl("Button", "开始录制(F9)", idRecord, 16, 80, 130, 32, wsTabStop)
	makeControl("Button", "停止录制", idStopRec, 152, 80, 100, 32, wsTabStop)
	makeControl("Button", "启动 ▶ (F10)", idPlay, 262, 80, 130, 32, wsTabStop)
	makeControl("Button", "停止 ■ (F11)", idStopPlay, 398, 80, 130, 32, wsTabStop)
	makeControl("Button", "保存脚本", idSave, 534, 80, 140, 32, wsTabStop)

	makeControl("Button", "加载脚本", idLoad, 16, 120, 100, 30, wsTabStop)
	makeControl("Button", "清空动作", idClear, 124, 120, 100, 30, wsTabStop)
	makeControl("Button", "+等待1秒", idWait, 232, 120, 100, 30, wsTabStop)
	makeControl("Static", "循环次数(0=无限):", 0, 350, 125, 140, 22, 0)
	hLoopEdit = makeControl("Edit", "0", idLoopEdit, 490, 120, 60, 30, wsBorder|wsTabStop|esAutoHScroll)
	makeControl("Static", "提示：填 3 表示重复 3 遍", 0, 560, 125, 130, 22, 0)

	makeControl("Static", "输入文本:", 0, 16, 164, 70, 22, 0)
	hTextEdit = makeControl("Edit", "", idTextEdit, 88, 160, 400, 28, wsBorder|wsTabStop|esAutoHScroll)
	makeControl("Button", "添加文本动作", idAddText, 498, 160, 176, 28, wsTabStop)

	makeControl("Static", "打开网址:", 0, 16, 200, 70, 22, 0)
	hURLEdit = makeControl("Edit", "", idURLEdit, 88, 196, 400, 28, wsBorder|wsTabStop|esAutoHScroll)
	makeControl("Button", "添加网址动作", idAddURL, 498, 196, 176, 28, wsTabStop)

	makeControl("Static", "── 后台任务（网页自动化，不影响你使用电脑）──", 0, 16, 232, 660, 22, 0)
	makeControl("Static", "后台网址:", 0, 16, 260, 70, 20, 0)
	hWebURLEdit = makeControl("Edit", "", idWebURLEdit, 96, 256, 330, 26, wsBorder|wsTabStop|esAutoHScroll)
	makeControl("Button", "后台打开网址", idAddWebOpen, 434, 256, 240, 26, wsTabStop)
	makeControl("Static", "元素选择器:", 0, 16, 294, 80, 20, 0)
	makeControl("Static", "保存目录:", 0, 16, 328, 70, 20, 0)
	makeControl("Static", "上传文件:", 0, 16, 362, 70, 20, 0)
	makeControl("Static", "提交按钮:", 0, 16, 396, 70, 20, 0)
	makeControl("Static", "浏览器路径:", 0, 16, 430, 80, 20, 0)

	hSelEdit = makeControl("Edit", "", idSelEdit, 96, 290, 330, 26, wsBorder|wsTabStop|esAutoHScroll)
	makeControl("Button", "后台点击", idAddWebClick, 434, 290, 110, 26, wsTabStop)
	makeControl("Button", "后台填写", idAddWebInput, 552, 290, 122, 26, wsTabStop)
	hSaveDirEdit = makeControl("Edit", "", idSaveDirEdit, 96, 324, 330, 26, wsBorder|wsTabStop|esAutoHScroll)
	makeControl("Button", "后台下载", idAddWebDownload, 434, 324, 110, 26, wsTabStop)
	makeControl("Static", "超时秒:", 0, 550, 328, 50, 20, 0)
	hTimeoutEdit = makeControl("Edit", "60", idTimeoutEdit, 606, 324, 68, 26, wsBorder|wsTabStop|esAutoHScroll)
	hUploadEdit = makeControl("Edit", "", idUploadEdit, 96, 358, 330, 26, wsBorder|wsTabStop|esAutoHScroll)
	makeControl("Button", "后台上传", idAddWebUpload, 434, 358, 240, 26, wsTabStop)
	hSubmitEdit = makeControl("Edit", "", idSubmitEdit, 96, 392, 578, 26, wsBorder|wsTabStop|esAutoHScroll)
	hBrowserEdit = makeControl("Edit", "", idBrowserEdit, 96, 426, 330, 26, wsBorder|wsTabStop|esAutoHScroll)
	makeControl("Static", "留空自动检测 Chrome / Edge", 0, 434, 430, 240, 20, 0)
	makeControl("Static", "元素选择器同时用于：后台点击 / 后台填写 / 后台上传。示例：#download、a.download、input[type=file]", 0, 16, 456, 660, 20, 0)

	makeControl("Static", "动作列表：", 0, 16, 480, 100, 20, 0)
	hList = makeControl("ListBox", "", idList, 16, 502, 658, 170, wsBorder|wsVScroll|lbsNotify|wsTabStop)
	hStatus = makeControl("Static", "准备就绪。后台任务请用上方按钮添加；前台录制用 F9。", idStatus, 16, 680, 658, 56, 0)

	// 脚本自动保存到 exe 同目录
	exe, err := os.Executable()
	if err == nil { scriptPath = filepath.Join(filepath.Dir(exe), "录制脚本.json") } else { scriptPath = "录制脚本.json" }

	if scriptPath != "" { loadScript() }

	pShowWindow.Call(hwndMain, swShow)
	pUpdateWindow.Call(hwndMain)
	pRegisterHotKey.Call(hwndMain, 1, 0, vkF9)
	pRegisterHotKey.Call(hwndMain, 2, 0, vkF10)
	pRegisterHotKey.Call(hwndMain, 3, 0, vkF11)

	hHookKbd, _, _ = pSetWindowsHookEx.Call(whKeyboardLL, kbdCallback, hInst, 0)
	hHookMouse, _, _ = pSetWindowsHookEx.Call(whMouseLL, mouseCallback, hInst, 0)

	var m msg
	for {
		ret, _, _ := pGetMessage.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(ret) <= 0 { break }
		pTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		pDispatchMessage.Call(uintptr(unsafe.Pointer(&m)))
	}
}
