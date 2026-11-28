
//go:build windows

package main

// ui_win.go —— 原生 Win32 窗口（零第三方依赖，纯 syscall）
// 双击 exe 直接弹出桌面窗口，不经过浏览器、不监听端口。
// 文件夹选择用 SHBrowseForFolderW 拿完整绝对路径；复用 Execute() 同进程执行。

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

// runtimeKeepAlive 防止模态调用期间参数被 GC 回收
func runtimeKeepAlive(v ...interface{}) { runtime.KeepAlive(v) }

// ---------- 常量 ----------
const (
	wmAppItem         = 0x0400 + 1 // 工作线程 -> UI：一条结果
	wmAppDone         = 0x0400 + 2 // 工作线程 -> UI：全部完成
	wmAppFolderPicked = 0x0400 + 4 // 文件夹选择线程 -> UI：选择结果
	wmAppUndoDone     = 0x0400 + 6 // 撤销线程 -> UI：撤销结果

	// 控件 ID 一律显式赋值：绝不要用 iota。
	// 在 const 块顶部增删任何一行都会让 iota 整体漂移，导致 wmCommand 分支、
	// 外部自动化工具与控件实际 ID 全部错位（历史上已踩过两次）。
	idDirEdit        = 1000
	idBrowseBtn      = 1001
	idRecursive      = 1002
	idReplaceContent = 1003
	idRenameFiles    = 1004
	idStartBtn       = 1005
	idLogEdit        = 1006
	idAddPair        = 1007
	idSaveAsChk      = 1008
	idSaveAsEdit     = 1009
	idSaveAsBrowse   = 1010
	idUndoBtn        = 1011
	idPairFromBase   = 1100 // 第 i 行：1100+i*3=原内容, +1=替换为, +2=删除

	// 控件样式
	wsChild        = 0x40000000
	wsVisible      = 0x10000000
	wsTabstop      = 0x00010000
	wsVScroll      = 0x00200000
	wsBorder       = 0x00800000
	esMultiline    = 0x0004
	esReadonly     = 0x0800
	esAutoHScroll  = 0x0080
	bsOwnerDraw    = 0x0000000C
	bsAutoCheckbox = 0x00000003
	bmSetCheck     = 0x00F1
	bmGetCheck     = 0x00F0
	ssLeft         = 0x0000

	// 消息
	wmCreate         = 0x0001
	wmDestroy        = 0x0002
	wmSize           = 0x0005
	wmPaint          = 0x000F
	wmClose          = 0x0010
	wmEraseBkgnd     = 0x0014
	wmCommand        = 0x0111
	wmDrawItem       = 0x002B
	wmCtlColorEdit   = 0x0133 // WM_CTLCOLOREDIT（已按 winuser.h 核实）
	wmCtlColorBtn    = 0x0135
	wmCtlColorStatic = 0x0138
	wmSetFont        = 0x0030

	bnClicked = 0x0000

	// Edit 消息
	emSetSel      = 0x00B1
	emReplaceSel  = 0x00C2
	emScrollCaret = 0x00B7

	// DrawItem 状态
	odsChecked  = 0x0008
	odsSelected = 0x0001
	odsFocus    = 0x0004

	// 绘制
	transparent  = 1
	opaque       = 2
	dtCenter     = 0x0001
	dtVcenter    = 0x0004
	dtSingleline = 0x0080

	// Browse 文件夹标志
	bifReturnOnlyFSDirs  = 0x00000001
	bifNewDialogStyle    = 0x00000040
	bifEditbox           = 0x00000010
	bifNonewFolderButton = 0x00000200

	// DPI 上下文 PER_MONITOR_AWARE_V2 = -4
	dpiAwareV2 = ^uintptr(3)
)

// ---------- 结构 ----------
type RECT struct {
	Left, Top, Right, Bottom int32
}

type POINT struct{ X, Y int32 }

type WNDCLASSEXW struct {
	cbSize        uint32
	style         uint32
	lpfnWndProc   uintptr
	cbClsExtra    int32
	cbWndExtra    int32
	hInstance     uintptr
	hIcon         uintptr
	hCursor       uintptr
	hbrBackground uintptr
	lpszMenuName  *uint16
	lpszClassName *uint16
	hIconSm       uintptr
}

type MSG struct {
	Hwnd     uintptr
	Message  uint32
	WParam   uintptr
	LParam   uintptr
	Time     uint32
	Pt       POINT
	LPrivate uint32 // MSG 结构体末尾字段，缺失会导致 GetMessageW 写入越界并返回 -1
}

// PAINTSTRUCT：WM_PAINT 必须 BeginPaint/EndPaint 成对调用，否则系统会不断重发绘制消息，
// 表现为窗口白屏且"未响应"。
type PAINTSTRUCT struct {
	Hdc       uintptr
	Erase     int32
	RcPaint   RECT
	Restore   int32
	IncUpdate int32
	Reserved  [32]byte
}

type BROWSEINFOW struct {
	hwndOwner      uintptr
	pidlRoot       uintptr
	pszDisplayName *uint16
	lpszTitle      *uint16
	ulFlags        uint32
	lpfn           uintptr
	lParam         uintptr
	iImage         int32
}

type DRAWITEMSTRUCT struct {
	CtlType    uint32
	CtlID      uint32
	itemID     uint32
	itemAction uint32
	itemState  uint32
	hwndItem   uintptr
	hDC        uintptr
	rcItem     RECT
	itemData   uintptr
}

type TRIVERTEX struct {
	X, Y  int32
	Red   uint16
	Green uint16
	Blue  uint16
	Alpha uint16
}

type GRADIENT_RECT struct {
	UpperLeft  uint32
	LowerRight uint32
}

type pairRow struct {
	hFrom uintptr
	hTo   uintptr
	hDel  uintptr
}

type appState struct {
	hwnd        uintptr
	hDir        uintptr
	hBrowse     uintptr
	hRecursive  uintptr
	hContent    uintptr
	hRename     uintptr
	hStart      uintptr
	hLog        uintptr
	hAdd        uintptr
	hStatus     uintptr
	hFont       uintptr
	hFontBold   uintptr
	hLblDir     uintptr
	hLblRules   uintptr
	hLblOut     uintptr
	hSaveAsChk  uintptr
	hSaveAsDir  uintptr
	hSaveAsBrw  uintptr
	hUndo       uintptr
	hPanelBrush uintptr // WM_CTLCOLOR 复用的画刷，避免每次创建新画刷导致 GDI 泄漏

	// 预渲染背景位图缓存：GradientFill 只在窗口尺寸变化时算一次，
	// 之后每次重绘只做一次 BitBlt，避免反复全屏渐变带来的卡顿与闪烁。
	hBgDC  uintptr
	hBgBmp uintptr
	hBgOld uintptr
	bgW    int32
	bgH    int32

	rows    []pairRow
	checked map[uint32]bool // 复选框/自绘按钮状态

	running bool
	done    int
	failed  int
	skipped int

	mu      sync.Mutex
	pending []Item

	// 文件夹选择结果（选择线程写，UI 线程读）
	browsePath  string
	browseErr   string
	browseWhich int // 1=源文件夹，2=另存目录

	// 撤销结果（撤销线程写，UI 线程读）
	undoResults []UndoResult
	undoTime    string
	undoErr     string
}

// ---------- DLL / Proc ----------
var (
	u32     = syscall.NewLazyDLL("user32.dll")
	g32     = syscall.NewLazyDLL("gdi32.dll")
	msimg32 = syscall.NewLazyDLL("msimg32.dll") // GradientFill 位于 msimg32.dll，不在 gdi32.dll
	shell32 = syscall.NewLazyDLL("shell32.dll")
	ole32   = syscall.NewLazyDLL("ole32.dll")
	dwmapi  = syscall.NewLazyDLL("dwmapi.dll")
	k32     = syscall.NewLazyDLL("kernel32.dll")

	pRegisterClassExW    = u32.NewProc("RegisterClassExW")
	pCreateWindowExW     = u32.NewProc("CreateWindowExW")
	pShowWindow          = u32.NewProc("ShowWindow")
	pUpdateWindow        = u32.NewProc("UpdateWindow")
	pGetMessageW         = u32.NewProc("GetMessageW")
	pTranslateMessage    = u32.NewProc("TranslateMessage")
	pDispatchMessageW    = u32.NewProc("DispatchMessageW")
	pDefWindowProcW      = u32.NewProc("DefWindowProcW")
	pPostQuitMessage     = u32.NewProc("PostQuitMessage")
	pSendMessageW        = u32.NewProc("SendMessageW")
	pPostMessageW        = u32.NewProc("PostMessageW")
	pSetWindowTextW      = u32.NewProc("SetWindowTextW")
	pGetWindowTextW      = u32.NewProc("GetWindowTextW")
	pGetWindowTextLenW   = u32.NewProc("GetWindowTextLengthW")
	pMoveWindow          = u32.NewProc("MoveWindow")
	pGetClientRect       = u32.NewProc("GetClientRect")
	pSetWindowPos        = u32.NewProc("SetWindowPos")
	pInvalidateRect      = u32.NewProc("InvalidateRect")
	pEnableWindow        = u32.NewProc("EnableWindow")
	pSetFocus            = u32.NewProc("SetFocus")
	pGetDC               = u32.NewProc("GetDC")
	pReleaseDC           = u32.NewProc("ReleaseDC")
	pBeginPaint          = u32.NewProc("BeginPaint")
	pEndPaint            = u32.NewProc("EndPaint")
	pLoadCursorW         = u32.NewProc("LoadCursorW")
	pGetSystemMetrics    = u32.NewProc("GetSystemMetrics")
	pGetDpiForWindow     = u32.NewProc("GetDpiForWindow")
	pSetProcessDpiAware  = u32.NewProc("SetProcessDpiAwarenessContext")
	pEnumWindows         = u32.NewProc("EnumWindows")
	pGetClassNameW       = u32.NewProc("GetClassNameW")
	pIsWindowVisible     = u32.NewProc("IsWindowVisible")
	pSetForegroundWindow = u32.NewProc("SetForegroundWindow")
	pDestroyWindow       = u32.NewProc("DestroyWindow")
	pSetWindowLongPtr    = u32.NewProc("SetWindowLongPtrW")
	pCreateMutexW        = k32.NewProc("CreateMutexW")
	pGetLastError        = k32.NewProc("GetLastError")
	pCloseHandle         = k32.NewProc("CloseHandle")
	pGetModuleHandleW    = k32.NewProc("GetModuleHandleW")

	pCreateSolidBrush = g32.NewProc("CreateSolidBrush")
	pGetStockObject   = g32.NewProc("GetStockObject") // DEFAULT_GUI_FONT=17
	pDeleteObject     = g32.NewProc("DeleteObject")
	pCreateFontW      = g32.NewProc("CreateFontW")
	pSelectObject     = g32.NewProc("SelectObject")
	pSetTextColor     = g32.NewProc("SetTextColor")
	pSetBkColor       = g32.NewProc("SetBkColor")
	pSetBkMode        = g32.NewProc("SetBkMode")
	pFillRect         = g32.NewProc("FillRect")
	pFrameRect        = g32.NewProc("FrameRect")
	pGradientFill          = msimg32.NewProc("GradientFill")
	pDrawTextW             = g32.NewProc("DrawTextW")
	pCreateCompatibleDC    = g32.NewProc("CreateCompatibleDC")
	pCreateCompatibleBitmap = g32.NewProc("CreateCompatibleBitmap")
	pBitBlt                = g32.NewProc("BitBlt")
	pDeleteDC              = g32.NewProc("DeleteDC")

	pSHBrowseForFolderW    = shell32.NewProc("SHBrowseForFolderW")
	pSHGetPathFromIDListW  = shell32.NewProc("SHGetPathFromIDListW")
	pCoInitialize          = ole32.NewProc("CoInitialize")
	pCoTaskMemFree         = ole32.NewProc("CoTaskMemFree")
	pDwmSetWindowAttribute = dwmapi.NewProc("DwmSetWindowAttribute")
)

// 主题色（COLORREF = 0x00BBGGRR）
func rgb(r, g, b byte) uintptr {
	return uintptr(r) | uintptr(g)<<8 | uintptr(b)<<16
}

// col 把 COLORREF(0x00BBGGRR) 拆成 TRIVERTEX 需要的 uint16 RGB 分量
func col(c uintptr) (uint16, uint16, uint16) {
	return uint16(c & 0xFF), uint16(c >> 8 & 0xFF), uint16(c >> 16 & 0xFF)
}

var (
	colorBgTop    = rgb(12, 22, 38) // 深蓝黑
	colorBgBottom = rgb(26, 42, 66) // 稍亮
	colorPanel    = rgb(18, 32, 54) // 输入框底色
	colorText     = rgb(240, 246, 252)
	colorMuted    = rgb(170, 190, 215)
	colorAccent   = rgb(0, 200, 255) // 青色强调
	colorAccent2  = rgb(0, 120, 190)
	colorOK       = rgb(0, 230, 150)
	colorFail     = rgb(255, 90, 90)
	colorSkip     = rgb(150, 165, 185)
	colorBtnTop   = rgb(0, 150, 210)
	colorBtnBot   = rgb(0, 95, 155)
)

// 全局状态（WndProc 回调必须长期存活）
var (
	gState    *appState
	wndProcCB uintptr
	enumCB    uintptr
	classCB   uintptr
)

// ---------- 通用工具 ----------
func utf16ptr(s string) *uint16 {
	p, _ := syscall.UTF16PtrFromString(s)
	return p
}

func getWindowText(hwnd uintptr) string {
	n, _, _ := pGetWindowTextLenW.Call(hwnd)
	if n == 0 {
		return ""
	}
	buf := make([]uint16, n+1)
	pGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(n+1))
	return syscall.UTF16ToString(buf)
}

func setWindowText(hwnd uintptr, s string) {
	pSetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(utf16ptr(s))))
}

func createWindow(className, title string, style uint32, x, y, w, h int32, parent, id uintptr) uintptr {
	hwnd, _, _ := pCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(utf16ptr(className))),
		uintptr(unsafe.Pointer(utf16ptr(title))),
		uintptr(style),
		uintptr(x), uintptr(y), uintptr(w), uintptr(h),
		parent, id, 0, 0,
	)
	return hwnd
}

// v3.1.2：不再调用 GetDpiForWindow（部分系统缺失该 API 且会拖慢启动），
// 布局统一按 96 DPI 逻辑像素，窗口用固定尺寸，避免高 DPI 换算把控件挤出可视区。
func scale(st *appState) float64 { return 1 }

// ---------- 启动诊断日志（v3.1.2 新增） ----------
// 写在 exe 同目录；失败则退到系统临时目录。万一仍出现白屏/未响应，
// 把「批量替换助手-启动日志.txt」发来即可定位卡在哪一步。
var startupLogOnce sync.Once
var startupLogPath string

func startupLog(msg string) {
	startupLogOnce.Do(func() {
		cands := []string{}
		if exe, err := os.Executable(); err == nil {
			cands = append(cands, filepath.Join(filepath.Dir(exe), "批量替换助手-启动日志.txt"))
		}
		cands = append(cands, "批量替换助手-启动日志.txt",
			filepath.Join(os.TempDir(), "批量替换助手-启动日志.txt"))
		for _, p := range cands {
			if f, e := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644); e == nil {
				startupLogPath = p
				f.WriteString("\r\n===== 启动 " + time.Now().Format("2006-01-02 15:04:05") + " =====\r\n")
				f.Close()
				break
			}
		}
	})
	if startupLogPath == "" {
		return
	}
	if f, e := os.OpenFile(startupLogPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644); e == nil {
		f.WriteString("[" + time.Now().Format("15:04:05.000") + "] " + msg + "\r\n")
		f.Close()
	}
}

func appendLog(st *appState, text string) {
	pSendMessageW.Call(st.hLog, emSetSel, ^uintptr(0), ^uintptr(0))
	pSendMessageW.Call(st.hLog, emReplaceSel, 0, uintptr(unsafe.Pointer(utf16ptr(text))))
	pSendMessageW.Call(st.hLog, emScrollCaret, 0, 0)
}

// ---------- 图形化选文件夹 ----------
func pickFolder(owner uintptr) (string, error) {
	pCoInitialize.Call(0)
	pathBuf := make([]uint16, 32768) // 支持长路径，避免 name too long
	nameBuf := make([]uint16, 512)
	titleBuf, _ := syscall.UTF16FromString("请选择要处理的文件夹") // 局部持有，避免模态期间被 GC
	bi := BROWSEINFOW{
		hwndOwner:      owner,
		pszDisplayName: &nameBuf[0],
		lpszTitle:      &titleBuf[0],
		ulFlags:        bifReturnOnlyFSDirs | bifNewDialogStyle | bifEditbox | bifNonewFolderButton,
	}
	pidl, _, _ := pSHBrowseForFolderW.Call(uintptr(unsafe.Pointer(&bi)))
	runtimeKeepAlive(&bi, &titleBuf, &pathBuf, &nameBuf)
	if pidl == 0 {
		return "", nil // 用户取消
	}
	defer pCoTaskMemFree.Call(pidl)
	ok, _, _ := pSHGetPathFromIDListW.Call(pidl, uintptr(unsafe.Pointer(&pathBuf[0])))
	if ok == 0 {
		return "", nil
	}
	return syscall.UTF16ToString(pathBuf), nil
}

// startFolderPicker 在独立操作系统线程上弹出文件夹选择对话框。
// 关键：SHBrowseForFolderW 是模态对话框，自带嵌套消息循环。若直接在 wndProc
// （syscall.NewCallback 回调）里调用，嵌套循环会重入同一个 Go 回调造成死锁，
// 表现为主窗口"未响应"。放到 LockOSThread 的独立线程后，UI 线程继续泵消息，
// 结果通过自定义消息回传，彻底避免死锁。
func startFolderPicker(st *appState, which int) {
	st.browseWhich = which // 1=源文件夹，2=另存目录
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()

		path, err := pickFolder(st.hwnd)
		st.mu.Lock()
		st.browsePath = path
		if err != nil {
			st.browseErr = err.Error()
		} else {
			st.browseErr = ""
		}
		st.mu.Unlock()
		pPostMessageW.Call(st.hwnd, wmAppFolderPicked, 0, 0)
	}()
}

// startUndo 在工作 goroutine 里还原文件，避免磁盘 IO 卡住消息循环
func startUndo(st *appState) {
	if st.running {
		appendLog(st, "⚠ 正在执行替换，请完成后再撤销\r\n")
		return
	}
	pEnableWindow.Call(st.hUndo, 0)
	go func() {
		res, at, err := UndoLast("")
		st.mu.Lock()
		st.undoResults, st.undoTime = res, at
		if err != nil {
			st.undoErr = err.Error()
		} else {
			st.undoErr = ""
		}
		st.mu.Unlock()
		pPostMessageW.Call(st.hwnd, wmAppUndoDone, 0, 0)
	}()
}

// ---------- 任务执行 ----------
func collectReq(st *appState) (ReplaceReq, error) {
	dir := strings.TrimSpace(getWindowText(st.hDir))
	if dir == "" {
		return ReplaceReq{}, fmtErr("请先用「浏览…」选择要处理的文件夹")
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return ReplaceReq{}, fmtErr("路径无法识别：" + err.Error())
	}
	req := ReplaceReq{
		Dir:            absDir,
		Recursive:      st.checked[idRecursive],
		ReplaceContent: st.checked[idReplaceContent],
		RenameFiles:    st.checked[idRenameFiles],
	}
	// 关键：跳过「替换为」为空的行，避免把原内容删掉
	pairs := make([]Pair, 0, len(st.rows))
	for _, r := range st.rows {
		from := strings.TrimSpace(getWindowText(r.hFrom))
		to := getWindowText(r.hTo)
		if from == "" || to == "" {
			continue
		}
		pairs = append(pairs, Pair{From: from, To: to})
	}
	req.Pairs = pairs
	// v3.1.5：规则先收齐，再算输出文件夹名（文件夹名也过同一套规则）
	if st.checked[idSaveAsChk] {
		s := strings.TrimSpace(getWindowText(st.hSaveAsDir))
		if s == "" {
			return ReplaceReq{}, fmtErr("已勾选「另存」，请先用「另存到…」选择输出目录")
		}
		req.SaveAs = normalizeSaveAsDir(absDir, s, pairs)
	}
	if err := validateReq(&req); err != nil {
		return ReplaceReq{}, err
	}
	return req, nil
}

func fmtErr(s string) error { return &uiError{s} }

type uiError struct{ s string }

func (e *uiError) Error() string { return e.s }

func startTask(st *appState) {
	req, err := collectReq(st)
	if err != nil {
		appendLog(st, "✖ "+err.Error()+"\r\n")
		return
	}
	st.running = true
	pEnableWindow.Call(st.hStart, 0)
	setWindowText(st.hStatus, "执行中…")

	appendLog(st, "━━━━━━━━━━━━━━━━━━━━━━━━━━━━\r\n")
	appendLog(st, "开始处理文件夹："+req.Dir+"\r\n")
	appendLog(st, "规则 "+itoa(len(req.Pairs))+" 组，替换内容="+yesNo(req.ReplaceContent)+"，替换文件名="+yesNo(req.RenameFiles)+"\r\n")
	if len(req.Pairs) > 1 {
		appendLog(st, "⚠ 规则按顺序链式生效：前一条的「替换为」若命中后一条的「原内容」，会被继续替换（如 北京→上海 + 上海→天津 会得到 天津）\r\n")
	}
	if req.SaveAs != "" {
		appendLog(st, "输出方式：另存 → "+req.SaveAs+"（原文件完全不动）\r\n")
		appendLog(st, "输出会统一放进上面这个文件夹，里面的文件与目录结构和源文件夹一致\r\n")
		appendLog(st, "输出文件夹名也会按同一套规则替换；源文件夹本身不改名\r\n")
	} else {
		appendLog(st, "输出方式：原地替换（完成后可点「↶ 撤销上次替换」还原）\r\n")
	}

	// 快照所需数据到工作 goroutine，避免 UI 线程竞争
	go func(req ReplaceReq) {
		stats := ExecuteEx(req, func(it Item) {
			st.mu.Lock()
			st.pending = append(st.pending, it)
			st.mu.Unlock()
			pPostMessageW.Call(st.hwnd, wmAppItem, 0, 0)
		})
		st.mu.Lock()
		st.done, st.failed, st.skipped = stats.Done, stats.Failed, stats.Skipped
		st.mu.Unlock()
		pPostMessageW.Call(st.hwnd, wmAppDone, 0, 0)
	}(req)
}

func yesNo(b bool) string {
	if b {
		return "是"
	}
	return "否"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

func drainItems(st *appState) {
	st.mu.Lock()
	items := st.pending
	st.pending = nil
	st.mu.Unlock()
	for _, it := range items {
		var prefix string // Edit 控件不支持逐行着色，用符号区分状态
		switch it.Status {
		case "ok":
			prefix = "✔ "
		case "failed":
			prefix = "✖ "
		default:
			prefix = "⏭ "
		}
		line := prefix + it.Kind + "  " + it.Message
		if it.Kind == "rename" && it.FromName != "" {
			line = prefix + "改名  " + it.FromName + " → " + it.ToName + "（" + it.Message + "）"
		}
		if it.Path != "" {
			line += "  [" + filepath.Base(it.Path) + "]"
		}
		appendLog(st, line+"\r\n")
	}
}

// ---------- 绘制 ----------
func drawButton(st *appState, dis *DRAWITEMSTRUCT, accent bool) {
	hdc := dis.hDC
	rc := dis.rcItem
	pressed := dis.itemState&odsSelected != 0

	top, bot := colorBtnTop, colorBtnBot
	if !accent {
		top, bot = rgb(46, 68, 96), rgb(32, 50, 74)
	}
	if pressed {
		top, bot = colorAccent2, colorBtnTop
	}

	tr, tg, tb := col(top)
	br, bg, bb := col(bot)
	verts := [2]TRIVERTEX{
		{X: rc.Left, Y: rc.Top, Red: tr, Green: tg, Blue: tb, Alpha: 0},
		{X: rc.Left, Y: rc.Bottom, Red: br, Green: bg, Blue: bb, Alpha: 0},
	}
	gr := GRADIENT_RECT{UpperLeft: 0, LowerRight: 1}
	pGradientFill.Call(hdc, uintptr(unsafe.Pointer(&verts[0])), 2, uintptr(unsafe.Pointer(&gr)), 1, 1) // GRADIENT_FILL_RECT_V = 1

	brush, _, _ := pCreateSolidBrush.Call(colorAccent)
	pFrameRect.Call(hdc, uintptr(unsafe.Pointer(&rc)), brush)
	pDeleteObject.Call(brush)

	pSetBkMode.Call(hdc, transparent)
	pSetTextColor.Call(hdc, colorText)
	text := utf16ptr(getWindowText(dis.hwndItem))
	pDrawTextW.Call(hdc, uintptr(unsafe.Pointer(text)), ^uintptr(0), uintptr(unsafe.Pointer(&rc)), dtCenter|dtVcenter|dtSingleline)
}

func drawCheckbox(st *appState, dis *DRAWITEMSTRUCT) {
	hdc := dis.hDC
	rc := dis.rcItem
	pSetBkMode.Call(hdc, transparent)

	// 方框
	box := RECT{Left: rc.Left + 2, Top: rc.Top + (rc.Bottom-rc.Top)/2 - 8, Right: rc.Left + 18, Bottom: rc.Top + (rc.Bottom-rc.Top)/2 + 8}
	brush, _, _ := pCreateSolidBrush.Call(colorPanel)
	pFillRect.Call(hdc, uintptr(unsafe.Pointer(&box)), brush)
	pDeleteObject.Call(brush)
	border, _, _ := pCreateSolidBrush.Call(colorAccent)
	pFrameRect.Call(hdc, uintptr(unsafe.Pointer(&box)), border)
	pDeleteObject.Call(border)

	checked := st.checked[uint32(dis.CtlID)]
	if checked {
		pSetTextColor.Call(hdc, colorAccent)
		mark := utf16ptr("✓")
		pDrawTextW.Call(hdc, uintptr(unsafe.Pointer(mark)), ^uintptr(0), uintptr(unsafe.Pointer(&box)), dtCenter|dtVcenter|dtSingleline)
	}

	// 文字
	text := utf16ptr(getWindowText(dis.hwndItem))
	tr := RECT{Left: rc.Left + 24, Top: rc.Top, Right: rc.Right, Bottom: rc.Bottom}
	pSetTextColor.Call(hdc, colorText)
	pDrawTextW.Call(hdc, uintptr(unsafe.Pointer(text)), ^uintptr(0), uintptr(unsafe.Pointer(&tr)), dtVcenter|dtSingleline)
}

// rebuildBackground 把整窗渐变预渲染到内存位图。
// 只在窗口尺寸变化时调用一次，避免每次 WM_ERASEBKGND / WM_PAINT 都重算 GradientFill。
func rebuildBackground(st *appState, w, h int32) {
	if w <= 0 || h <= 0 {
		return
	}
	releaseBackground(st)
	screenDC, _, _ := pGetDC.Call(st.hwnd)
	if screenDC == 0 {
		return
	}
	dc, _, _ := pCreateCompatibleDC.Call(screenDC)
	bmp, _, _ := pCreateCompatibleBitmap.Call(screenDC, uintptr(w), uintptr(h))
	pReleaseDC.Call(st.hwnd, screenDC)
	if dc == 0 || bmp == 0 {
		if dc != 0 {
			pDeleteDC.Call(dc)
		}
		if bmp != 0 {
			pDeleteObject.Call(bmp)
		}
		return
	}
	old, _, _ := pSelectObject.Call(dc, bmp)
	tr, tg, tb := col(colorBgTop)
	br, bg, bb := col(colorBgBottom)
	verts := [2]TRIVERTEX{
		{X: 0, Y: 0, Red: tr, Green: tg, Blue: tb, Alpha: 0},
		{X: w, Y: h, Red: br, Green: bg, Blue: bb, Alpha: 0},
	}
	gr := GRADIENT_RECT{UpperLeft: 0, LowerRight: 1}
	pGradientFill.Call(dc, uintptr(unsafe.Pointer(&verts[0])), 2, uintptr(unsafe.Pointer(&gr)), 1, 1)
	st.hBgDC, st.hBgBmp, st.hBgOld = dc, bmp, old
	st.bgW, st.bgH = w, h
}

func releaseBackground(st *appState) {
	if st.hBgDC != 0 {
		pSelectObject.Call(st.hBgDC, st.hBgOld)
		pDeleteDC.Call(st.hBgDC)
	}
	if st.hBgBmp != 0 {
		pDeleteObject.Call(st.hBgBmp)
	}
	st.hBgDC, st.hBgBmp, st.hBgOld, st.bgW, st.bgH = 0, 0, 0, 0, 0
}

// paintBackground 用一次 BitBlt 把缓存位图贴到窗口，配合 WM_ERASEBKGND 返回 1 实现双缓冲
func paintBackground(st *appState) {
	var ps PAINTSTRUCT
	hdc, _, _ := pBeginPaint.Call(st.hwnd, uintptr(unsafe.Pointer(&ps)))
	if hdc == 0 {
		return
	}
	var rc RECT
	pGetClientRect.Call(st.hwnd, uintptr(unsafe.Pointer(&rc)))
	if st.hBgDC == 0 || st.bgW != rc.Right || st.bgH != rc.Bottom {
		rebuildBackground(st, rc.Right, rc.Bottom)
	}
	if st.hBgDC != 0 {
		pBitBlt.Call(hdc, 0, 0, uintptr(rc.Right), uintptr(rc.Bottom), st.hBgDC, 0, 0, 0x00CC0020) // SRCCOPY
	} else {
		brush, _, _ := pCreateSolidBrush.Call(colorBgBottom)
		pFillRect.Call(hdc, uintptr(unsafe.Pointer(&rc)), brush)
		pDeleteObject.Call(brush)
	}
	pEndPaint.Call(st.hwnd, uintptr(unsafe.Pointer(&ps)))
}

// ---------- 布局 ----------
func layoutControls(st *appState) {
	var rc RECT
	pGetClientRect.Call(st.hwnd, uintptr(unsafe.Pointer(&rc)))
	s := scale(st)
	W := float64(rc.Right) / s // 客户区宽度（逻辑像素）：高 DPI 下必须换算，否则控件会溢出窗口
	v := func(x float64) uintptr { return uintptr(int32(x * s)) } // 逻辑像素 -> 物理像素

	margin := 22.0
	rowH := 30.0
	gap := 8.0
	lblH := 22.0
	y := 14.0

	// ① 文件夹标签 + 输入框 + 浏览
	pMoveWindow.Call(st.hLblDir, v(margin), v(y), v(W-2*margin), v(lblH), 1)
	y += lblH + 2
	pMoveWindow.Call(st.hDir, v(margin), v(y), v(W-2*margin-96), v(rowH), 1)
	pMoveWindow.Call(st.hBrowse, v(W-margin-88), v(y), v(88), v(rowH), 1)
	y += rowH + gap

	// ② 规则标签
	pMoveWindow.Call(st.hLblRules, v(margin), v(y), v(W-2*margin), v(lblH), 1)
	y += lblH + 2

	// 规则行
	n := len(st.rows)
	for i, r := range st.rows {
		ry := v(y + float64(i)*(rowH+gap))
		wFrom := v((W - 2*margin - 64) / 2)
		pMoveWindow.Call(r.hFrom, v(margin), ry, wFrom, v(rowH), 1)
		pMoveWindow.Call(r.hTo, v(margin)+wFrom+v(12), ry, wFrom, v(rowH), 1)
		pMoveWindow.Call(r.hDel, v(W-margin-44), ry, v(44), v(rowH), 1)
	}
	y += float64(n) * (rowH + gap)
	pMoveWindow.Call(st.hAdd, v(margin), v(y), v(120), v(rowH), 1)
	y += rowH + gap

	// 三个复选框
	cbW := 160.0
	pMoveWindow.Call(st.hRecursive, v(margin), v(y), v(cbW), v(rowH), 1)
	pMoveWindow.Call(st.hContent, v(margin+cbW+8), v(y), v(cbW), v(rowH), 1)
	pMoveWindow.Call(st.hRename, v(margin+2*(cbW+8)), v(y), v(cbW), v(rowH), 1)
	y += rowH + gap

	// ③ 输出方式：说明 + 另存复选 + 撤销按钮 + 另存路径
	pMoveWindow.Call(st.hLblOut, v(margin), v(y), v(W-2*margin), v(lblH), 1)
	y += lblH + 2
	pMoveWindow.Call(st.hSaveAsChk, v(margin), v(y), v(240), v(rowH), 1)
	pMoveWindow.Call(st.hUndo, v(W-margin-160), v(y), v(160), v(rowH), 1)
	y += rowH + gap
	pMoveWindow.Call(st.hSaveAsDir, v(margin), v(y), v(W-2*margin-112), v(rowH), 1)
	pMoveWindow.Call(st.hSaveAsBrw, v(W-margin-104), v(y), v(104), v(rowH), 1)
	y += rowH + gap

	// 开始按钮 + 状态文本
	pMoveWindow.Call(st.hStart, v(margin), v(y), v(140), v(rowH+6), 1)
	pMoveWindow.Call(st.hStatus, v(margin+150), v(y+4), v(W-2*margin-150), v(rowH), 1)
	y += rowH + 6 + gap

	// 日志框（占据剩余空间）：logH 是逻辑像素，必须再经 v() 换算成物理像素
	logH := float64(rc.Bottom)/s - y - margin
	if logH < 120 {
		logH = 120
	}
	pMoveWindow.Call(st.hLog, v(margin), v(y), v(W-2*margin), v(logH), 1)
}

func addPairRow(st *appState, from, to string) {
	s := scale(st)
	id := len(st.rows)
	hFrom := createWindow("Edit", from, wsChild|wsVisible|wsTabstop|wsBorder, 0, 0, int32(200*s), int32(30*s), st.hwnd, idPairFromBase+uintptr(id*3))
	hTo := createWindow("Edit", to, wsChild|wsVisible|wsTabstop|wsBorder, 0, 0, int32(200*s), int32(30*s), st.hwnd, idPairFromBase+uintptr(id*3+1))
	hDel := createWindow("Button", "删除", wsChild|wsVisible|wsTabstop, 0, 0, int32(44*s), int32(30*s), st.hwnd, idPairFromBase+uintptr(id*3+2))
	if st.hFont != 0 {
		pSendMessageW.Call(hFrom, wmSetFont, st.hFont, 1)
		pSendMessageW.Call(hTo, wmSetFont, st.hFont, 1)
		pSendMessageW.Call(hDel, wmSetFont, st.hFont, 1)
	}
	st.rows = append(st.rows, pairRow{hFrom: hFrom, hTo: hTo, hDel: hDel})
}

func removePairRow(st *appState, id uint32) {
	if len(st.rows) <= 1 {
		return // 至少保留一行
	}
	idx := int(id-idPairFromBase) / 3
	if idx < 0 || idx >= len(st.rows) {
		return
	}
	r := st.rows[idx]
	pDestroyWindow.Call(r.hFrom)
	pDestroyWindow.Call(r.hTo)
	pDestroyWindow.Call(r.hDel)
	st.rows = append(st.rows[:idx], st.rows[idx+1:]...)
	// 关键：后续行的控件 ID 必须重排，否则删除第 2 行后第 3 行的删除按钮 ID 会错位
	gwlID := ^uintptr(11) // GWL_ID = -12
	for i := idx; i < len(st.rows); i++ {
		base := idPairFromBase + uintptr(i*3)
		pSetWindowLongPtr.Call(st.rows[i].hFrom, gwlID, base)
		pSetWindowLongPtr.Call(st.rows[i].hTo, gwlID, base+1)
		pSetWindowLongPtr.Call(st.rows[i].hDel, gwlID, base+2)
	}
	layoutControls(st)
}

// ---------- 窗口过程 ----------
func wndProc(hwnd uintptr, msg uint32, wParam, lParam uintptr) uintptr {
	st := gState
	switch msg {
	case wmCreate:
		st.hwnd = hwnd
		st.hPanelBrush, _, _ = pCreateSolidBrush.Call(colorPanel)

		// v3.1.2：去掉 DWM 深色标题栏与 14 参数的 CreateFontW。
		// DwmSetWindowAttribute 需与 DWM 进程同步通信，在精简系统 / 远程桌面下
		// 可能长时间不返回；CreateFontW 也可能卡在 GDI 字体加载。改用系统 stock 字体。
		hFont, _, _ := pGetStockObject.Call(17) // DEFAULT_GUI_FONT = 17
		st.hFont, st.hFontBold = hFont, hFont
		startupLog("WM_CREATE：字体就绪")

		st.checked = map[uint32]bool{
			idRecursive:      true,
			idReplaceContent: true,
			idRenameFiles:    false,
			idSaveAsChk:      false,
		}

		// 静态标签
		// Static 控件必须带 WS_CHILD|WS_VISIBLE，只传 ssLeft(0) 会创建失败，导致标签完全不显示
		st.hLblDir = createWindow("Static", "① 选择要处理的文件夹", wsChild|wsVisible|ssLeft, 0, 0, 0, 0, hwnd, 0)
		st.hDir = createWindow("Edit", "", wsChild|wsVisible|wsTabstop|wsBorder, 0, 0, 0, 0, hwnd, idDirEdit)
		st.hBrowse = createWindow("Button", "浏览…", wsChild|wsVisible|wsTabstop, 0, 0, 0, 0, hwnd, idBrowseBtn)
		st.hLblRules = createWindow("Static", "② 替换规则（「替换为」留空则不替换；支持 Word/Excel 新旧格式，无需装 Office）", wsChild|wsVisible|ssLeft, 0, 0, 0, 0, hwnd, 0)

		// 5 条预置规则
		for _, v := range []string{"AAAA", "BBBB", "CCCC", "DDDD", "EEEE"} {
			addPairRow(st, v, "")
		}

		st.hAdd = createWindow("Button", "+ 添加一组", wsChild|wsVisible|wsTabstop, 0, 0, 0, 0, hwnd, idAddPair)
		// 用系统标准复选框代替自绘：自绘消息在部分环境下不会送达，标准控件保证文字和勾选状态可见
		st.hRecursive = createWindow("Button", "包含子文件夹", wsChild|wsVisible|wsTabstop|bsAutoCheckbox, 0, 0, 0, 0, hwnd, idRecursive)
		st.hContent = createWindow("Button", "替换文档内容", wsChild|wsVisible|wsTabstop|bsAutoCheckbox, 0, 0, 0, 0, hwnd, idReplaceContent)
		st.hRename = createWindow("Button", "同时替换文件名", wsChild|wsVisible|wsTabstop|bsAutoCheckbox, 0, 0, 0, 0, hwnd, idRenameFiles)
		// ③ 输出方式：不勾选「另存」就是在原文件上直接改
		st.hLblOut = createWindow("Static", "③ 输出方式（勾选「另存」会生成一个新文件夹，文件夹名和里面的文件名都按规则替换）", wsChild|wsVisible|ssLeft, 0, 0, 0, 0, hwnd, 0)
		st.hSaveAsChk = createWindow("Button", "另存为新文件（原文件不动）", wsChild|wsVisible|wsTabstop|bsAutoCheckbox, 0, 0, 0, 0, hwnd, idSaveAsChk)
		st.hSaveAsDir = createWindow("Edit", "", wsChild|wsVisible|wsTabstop|wsBorder, 0, 0, 0, 0, hwnd, idSaveAsEdit)
		st.hSaveAsBrw = createWindow("Button", "另存到…", wsChild|wsVisible|wsTabstop, 0, 0, 0, 0, hwnd, idSaveAsBrowse)
		st.hStart = createWindow("Button", "开始替换", wsChild|wsVisible|wsTabstop, 0, 0, 0, 0, hwnd, idStartBtn)
		st.hUndo = createWindow("Button", "↶ 撤销上次替换", wsChild|wsVisible|wsTabstop, 0, 0, 0, 0, hwnd, idUndoBtn)
		st.hStatus = createWindow("Static", "⚠ 替换前请先备份；另存模式不改原文件，原地模式可用「撤销」还原", wsChild|wsVisible|ssLeft, 0, 0, 0, 0, hwnd, 0)
		st.hLog = createWindow("Edit", "", wsChild|wsVisible|wsTabstop|wsBorder|wsVScroll|esMultiline|esReadonly|esAutoHScroll, 0, 0, 0, 0, hwnd, idLogEdit)

		for _, h := range []uintptr{st.hDir, st.hBrowse, st.hAdd, st.hRecursive, st.hContent, st.hRename,
			st.hStart, st.hUndo, st.hStatus, st.hLog, st.hLblDir, st.hLblRules, st.hLblOut,
			st.hSaveAsChk, st.hSaveAsDir, st.hSaveAsBrw} {
			pSendMessageW.Call(h, wmSetFont, st.hFont, 1)
		}
		pSendMessageW.Call(st.hStart, wmSetFont, st.hFontBold, 1)
		// 同步初始勾选状态到标准复选框
		for id, on := range map[uint32]bool{
			idRecursive: true, idReplaceContent: true, idRenameFiles: false, idSaveAsChk: false,
		} {
			if !on {
				continue
			}
			var h uintptr
			switch id {
			case idRecursive:
				h = st.hRecursive
			case idReplaceContent:
				h = st.hContent
			case idRenameFiles:
				h = st.hRename
			case idSaveAsChk:
				h = st.hSaveAsChk
			}
			pSendMessageW.Call(h, bmSetCheck, 1, 0)
		}

		appendLog(st, "批量替换助手 v3.1.6 已就绪（支持 .docx/.xlsx/.doc/.xls 及宏文档，纯本地无需 Office）。\r\n请选择文件夹并填写替换规则。\r\n")
		layoutControls(st)
		startupLog("WM_CREATE：控件与布局完成")
		return 0

	case wmSize:
		layoutControls(st)
		return 0

	// v3.1.2 修复「白屏 + 未响应」：不再拦截
	// WM_ERASEBKGND / WM_PAINT / WM_CTLCOLOR* / WM_DRAWITEM，
	// 全部交给 DefWindowProc，用注册类时的系统画刷（COLOR_WINDOW）擦背景、
	// 用系统标准控件外观绘制。自绘渐变 + BeginPaint/EndPaint + GradientFill
	// 在部分 Windows 环境会让窗口过程长时间不返回，是白屏未响应的高发区。

	case wmCommand:
		id := uint32(wParam & 0xFFFF)
		code := uint32(wParam>>16) & 0xFFFF
		switch {
		case id == idBrowseBtn && code == bnClicked:
			// 不在此处同步弹模态对话框（会重入 Go 回调导致死锁），
			// 改为独立线程选择，结果由 wmAppFolderPicked 回传。
			startFolderPicker(st, 1)
		case id == idSaveAsBrowse && code == bnClicked:
			startFolderPicker(st, 2)
		case id == idUndoBtn && code == bnClicked:
			startUndo(st)
		case id == idStartBtn && code == bnClicked:
			if !st.running {
				startTask(st)
			}
		case id == idAddPair && code == bnClicked:
			addPairRow(st, "", "")
			layoutControls(st)
		case id >= idPairFromBase && (id-idPairFromBase)%3 == 2 && code == bnClicked:
			removePairRow(st, id)
		case id == idRecursive || id == idReplaceContent || id == idRenameFiles || id == idSaveAsChk:
			if code == bnClicked {
				// 标准复选框由系统维护勾选状态，这里只同步到业务状态
				var hChk uintptr
				switch id {
				case idRecursive:
					hChk = st.hRecursive
				case idReplaceContent:
					hChk = st.hContent
				case idRenameFiles:
					hChk = st.hRename
				case idSaveAsChk:
					hChk = st.hSaveAsChk
				}
				state, _, _ := pSendMessageW.Call(hChk, bmGetCheck, 0, 0)
				st.checked[id] = state != 0
			}
		}
		return 0

	case wmAppFolderPicked:
		st.mu.Lock()
		path, errMsg, which := st.browsePath, st.browseErr, st.browseWhich
		st.browsePath, st.browseErr = "", ""
		st.mu.Unlock()
		if errMsg != "" {
			appendLog(st, "✖ 选择文件夹失败："+errMsg+"\r\n")
		} else if path != "" {
			if which == 2 {
				setWindowText(st.hSaveAsDir, path)
				appendLog(st, "已选择另存目录："+path+"\r\n")
			} else {
				setWindowText(st.hDir, path)
				appendLog(st, "已选择文件夹："+path+"\r\n")
			}
		}
		return 0

	case wmAppUndoDone:
		st.mu.Lock()
		res, at, errMsg := st.undoResults, st.undoTime, st.undoErr
		st.undoResults, st.undoTime, st.undoErr = nil, "", ""
		st.mu.Unlock()
		pEnableWindow.Call(st.hUndo, 1)
		if errMsg != "" {
			appendLog(st, "✖ 撤销失败："+errMsg+"\r\n")
			setWindowText(st.hStatus, "撤销失败")
			return 0
		}
		ok := 0
		for _, r := range res {
			if r.OK {
				ok++
				appendLog(st, "✔ 撤销 "+r.Message+"\r\n")
			} else {
				appendLog(st, "⏭ 撤销 "+r.Message+"\r\n")
			}
		}
		appendLog(st, "已撤销 "+at+" 的操作：成功 "+itoa(ok)+" 项\r\n")
		setWindowText(st.hStatus, "撤销完成：成功 "+itoa(ok)+" 项")
		return 0

	case wmAppItem:
		drainItems(st)
		return 0

	case wmAppDone:
		drainItems(st)
		st.mu.Lock()
		done, failed, skipped := st.done, st.failed, st.skipped
		st.mu.Unlock()
		st.running = false
		pEnableWindow.Call(st.hStart, 1)
		summary := "成功 " + itoa(done) + " 项，跳过 " + itoa(skipped) + " 项，失败 " + itoa(failed) + " 项"
		setWindowText(st.hStatus, "完成："+summary)
		appendLog(st, "━━━━━━━━━━━━━━━━━━━━━━━━━━━━\r\n")
		appendLog(st, "全部完成："+summary+"\r\n")
		return 0

	case wmClose:
		pPostQuitMessage.Call(0)
		return 0

	case wmDestroy:
		if st.hPanelBrush != 0 {
			pDeleteObject.Call(st.hPanelBrush)
		}
		// hFont 是 GetStockObject 得到的系统共享对象，绝不能 DeleteObject
		pPostQuitMessage.Call(0)
		return 0
	}
	ret, _, _ := pDefWindowProcW.Call(hwnd, uintptr(msg), wParam, lParam)
	return ret
}

// 单实例：已有窗口置前
func enumWindowsProc(hwnd, lParam uintptr) uintptr {
	visible, _, _ := pIsWindowVisible.Call(hwnd)
	if visible != 0 {
		var buf [256]uint16
		pGetClassNameW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
		cls := syscall.UTF16ToString(buf[:])
		if cls == "批量替换助手_Window" {
			pSetForegroundWindow.Call(hwnd)
			return 0
		}
	}
	return 1
}

// ---------- 应用入口（Windows） ----------
func runApp() error {
	startupLog("进入 runApp")

	// v3.1.2：去掉 SetProcessDpiAwarenessContext 与 Global\ 单实例互斥体。
	// 前者在部分系统会拖慢启动；后者会让「上一次卡死的残留实例」挡住新 exe，
	// 表现为双击后什么都没发生。现在每次启动都开一个全新窗口。

	hInst, _, _ := pGetModuleHandleW.Call(0)
	startupLog("取模块句柄完成")
	className := utf16ptr("批量替换助手_Window")
	title := utf16ptr("批量替换助手 v" + appVersion + " · 本地桌面版")

	// IDC_ARROW(32512) 是资源 ID，不是 HANDLE；必须 LoadCursorW 转成真正的光标句柄，
	// 否则 RegisterClassExW 会返回 ERROR_INVALID_PARAMETER(87)，后续 CreateWindow 失败。
	hCursor, _, _ := pLoadCursorW.Call(0, 32512)
	if hCursor == 0 {
		hCursor, _, _ = pLoadCursorW.Call(hInst, 32512)
	}

	wndProcCB = syscall.NewCallback(wndProc)
	wc := WNDCLASSEXW{
		cbSize:        uint32(unsafe.Sizeof(WNDCLASSEXW{})),
		lpfnWndProc:   wndProcCB,
		hInstance:     hInst,
		hCursor:       hCursor,
		hbrBackground: 6, // COLOR_WINDOW + 1：由系统擦背景，不自己画
		lpszClassName: className,
	}
	classCB = uintptr(unsafe.Pointer(wc.lpszClassName)) // 保活（className 指针）
	classAtom, _, regErr := pRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
	// ERROR_CLASS_ALREADY_EXISTS(1410) 是正常的：同一进程重复注册不影响创建窗口
	if classAtom == 0 {
		const errorClassAlreadyExists = syscall.Errno(1410)
		if errno, ok := regErr.(syscall.Errno); ok && errno != errorClassAlreadyExists {
			return &uiError{fmt.Sprintf("注册窗口类失败（错误码 %d）", errno)}
		}
	}

	gState = &appState{}
	hwnd, _, createErr := pCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(title)),
		0x00CF0000 | 0x02000000, // WS_OVERLAPPEDWINDOW | WS_CLIPCHILDREN（减少子控件区域重绘）
		0x80000000, 0x80000000, 920, 800,
		0, 0, hInst, 0,
	)
	if hwnd == 0 {
		if errno, ok := createErr.(syscall.Errno); ok && errno != 0 {
			return &uiError{fmt.Sprintf("创建窗口失败（错误码 %d）", errno)}
		}
		return &uiError{"创建窗口失败"}
	}

	// v3.1.2：固定 920x800 物理像素，不做 DPI 换算。
	startupLog("主窗口创建成功，准备显示")

	pShowWindow.Call(hwnd, 5) // SW_SHOW（旧值 9=SW_RESTORE 对新建窗口语义含糊）
	pUpdateWindow.Call(hwnd)  // 同步触发一次 WM_PAINT，现在由 DefWindowProc 秒回
	startupLog("窗口已显示，即将进入消息循环")

	var msg MSG
	firstMsg := true
	for {
		r, _, getErr := pGetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if r == 0 {
			break
		}
		if firstMsg {
			firstMsg = false
			startupLog("消息循环已收到第一条消息（窗口可交互）")
		}
		if r == ^uintptr(0) {
			if errno, ok := getErr.(syscall.Errno); ok && errno != 0 {
				return &uiError{fmt.Sprintf("消息循环失败（错误码 %d）", errno)}
			}
			return &uiError{"消息循环失败"}
		}
		pTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		pDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
	}
	_ = classCB
	return nil
}
