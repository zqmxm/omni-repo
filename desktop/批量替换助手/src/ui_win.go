//go:build windows

package main

// ui_win.go —— 原生 Win32 窗口（零第三方依赖，纯 syscall）
// 双击 exe 直接弹出桌面窗口，不经过浏览器、不监听端口。
// 文件夹选择用 SHBrowseForFolderW 拿完整绝对路径；复用 Execute() 同进程执行。

import (
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"unsafe"
)

// runtimeKeepAlive 防止模态调用期间参数被 GC 回收
func runtimeKeepAlive(v ...interface{}) { runtime.KeepAlive(v) }

// ---------- 常量 ----------
const (
	wmAppItem = 0x0400 + 1 // 工作线程 -> UI：一条结果
	wmAppDone = 0x0400 + 2 // 工作线程 -> UI：全部完成

	idDirEdit = 1000 + iota
	idBrowseBtn
	idRecursive
	idReplaceContent
	idRenameFiles
	idStartBtn
	idLogEdit
	idAddPair
	idPairFromBase = 1100 // 第 i 行：1100+i*3=原内容, +1=替换为, +2=删除

	// 控件样式
	wsChild     = 0x40000000
	wsVisible   = 0x10000000
	wsTabstop   = 0x00010000
	wsVScroll   = 0x00200000
	wsBorder    = 0x00800000
	esMultiline = 0x0004
	esReadonly  = 0x0800
	esAutoHScroll = 0x0080
	bsOwnerDraw = 0x0000000C
	ssLeft      = 0x0000

	// 消息
	wmCreate        = 0x0001
	wmDestroy       = 0x0002
	wmSize          = 0x0005
	wmPaint         = 0x000F
	wmClose         = 0x0010
	wmCommand       = 0x0111
	wmDrawItem      = 0x002B
	wmCtlColorEdit  = 0x0133
	wmCtlColorStatic = 0x0138
	wmSetFont       = 0x0030

	bnClicked = 0x0000

	// Edit 消息
	emSetSel      = 0x00B1
	emReplaceSel  = 0x00C2
	emScrollCaret = 0x00B7

	// DrawItem 状态
	odsChecked = 0x0008
	odsSelected = 0x0001
	odsFocus   = 0x0004

	// 绘制
	transparent = 1
	dtCenter    = 0x0001
	dtVcenter   = 0x0004
	dtSingleline = 0x0080

	// Browse 文件夹标志
	bifReturnOnlyFSDirs = 0x00000001
	bifNewDialogStyle   = 0x00000040
	bifEditbox          = 0x00000010
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
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      POINT
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
	X, Y     int32
	Red      uint16
	Green    uint16
	Blue     uint16
	Alpha    uint16
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
	hwnd       uintptr
	hDir       uintptr
	hBrowse    uintptr
	hRecursive uintptr
	hContent   uintptr
	hRename    uintptr
	hStart     uintptr
	hLog       uintptr
	hAdd       uintptr
	hStatus    uintptr
	hFont      uintptr
	hFontBold  uintptr
	hLblDir    uintptr
	hLblRules  uintptr
	hPanelBrush uintptr // WM_CTLCOLOR 复用的画刷，避免每次创建新画刷导致 GDI 泄漏

	rows    []pairRow
	checked map[uint32]bool // 复选框/自绘按钮状态

	running bool
	done    int
	failed  int

	mu      sync.Mutex
	pending []Item
}

// ---------- DLL / Proc ----------
var (
	u32     = syscall.NewLazyDLL("user32.dll")
	g32     = syscall.NewLazyDLL("gdi32.dll")
	shell32 = syscall.NewLazyDLL("shell32.dll")
	ole32   = syscall.NewLazyDLL("ole32.dll")
	dwmapi  = syscall.NewLazyDLL("dwmapi.dll")
	k32     = syscall.NewLazyDLL("kernel32.dll")

	pRegisterClassExW   = u32.NewProc("RegisterClassExW")
	pCreateWindowExW    = u32.NewProc("CreateWindowExW")
	pShowWindow         = u32.NewProc("ShowWindow")
	pUpdateWindow       = u32.NewProc("UpdateWindow")
	pGetMessageW        = u32.NewProc("GetMessageW")
	pTranslateMessage   = u32.NewProc("TranslateMessage")
	pDispatchMessageW   = u32.NewProc("DispatchMessageW")
	pDefWindowProcW     = u32.NewProc("DefWindowProcW")
	pPostQuitMessage    = u32.NewProc("PostQuitMessage")
	pSendMessageW       = u32.NewProc("SendMessageW")
	pPostMessageW       = u32.NewProc("PostMessageW")
	pSetWindowTextW     = u32.NewProc("SetWindowTextW")
	pGetWindowTextW     = u32.NewProc("GetWindowTextW")
	pGetWindowTextLenW  = u32.NewProc("GetWindowTextLengthW")
	pMoveWindow         = u32.NewProc("MoveWindow")
	pGetClientRect      = u32.NewProc("GetClientRect")
	pInvalidateRect     = u32.NewProc("InvalidateRect")
	pEnableWindow       = u32.NewProc("EnableWindow")
	pSetFocus           = u32.NewProc("SetFocus")
	pGetDC              = u32.NewProc("GetDC")
	pReleaseDC          = u32.NewProc("ReleaseDC")
	pLoadCursorW        = u32.NewProc("LoadCursorW")
	pGetSystemMetrics   = u32.NewProc("GetSystemMetrics")
	pGetDpiForWindow    = u32.NewProc("GetDpiForWindow")
	pSetProcessDpiAware = u32.NewProc("SetProcessDpiAwarenessContext")
	pEnumWindows        = u32.NewProc("EnumWindows")
	pGetClassNameW      = u32.NewProc("GetClassNameW")
	pIsWindowVisible    = u32.NewProc("IsWindowVisible")
	pSetForegroundWindow = u32.NewProc("SetForegroundWindow")
	pDestroyWindow      = u32.NewProc("DestroyWindow")
	pSetWindowLongPtr   = u32.NewProc("SetWindowLongPtrW")
	pCreateMutexW       = k32.NewProc("CreateMutexW")
	pGetLastError       = k32.NewProc("GetLastError")
	pCloseHandle        = k32.NewProc("CloseHandle")
	pGetModuleHandleW   = k32.NewProc("GetModuleHandleW")

	pCreateSolidBrush = g32.NewProc("CreateSolidBrush")
	pDeleteObject     = g32.NewProc("DeleteObject")
	pCreateFontW      = g32.NewProc("CreateFontW")
	pSelectObject     = g32.NewProc("SelectObject")
	pSetTextColor     = g32.NewProc("SetTextColor")
	pSetBkColor       = g32.NewProc("SetBkColor")
	pSetBkMode        = g32.NewProc("SetBkMode")
	pFillRect         = g32.NewProc("FillRect")
	pFrameRect        = g32.NewProc("FrameRect")
	pGradientFill     = g32.NewProc("GradientFill")
	pDrawTextW        = g32.NewProc("DrawTextW")

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
	colorBgTop    = rgb(12, 22, 38)   // 深蓝黑
	colorBgBottom = rgb(26, 42, 66)   // 稍亮
	colorPanel    = rgb(18, 32, 54)   // 输入框底色
	colorText     = rgb(224, 234, 245)
	colorMuted    = rgb(140, 160, 185)
	colorAccent   = rgb(0, 200, 255)  // 青色强调
	colorAccent2  = rgb(0, 120, 190)
	colorOK       = rgb(0, 230, 150)
	colorFail     = rgb(255, 90, 90)
	colorSkip     = rgb(150, 165, 185)
	colorBtnTop   = rgb(0, 150, 210)
	colorBtnBot   = rgb(0, 95, 155)
)

// 全局状态（WndProc 回调必须长期存活）
var (
	gState   *appState
	wndProcCB uintptr
	enumCB   uintptr
	classCB  uintptr
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

func scale(st *appState) float64 {
	if st == nil || st.hwnd == 0 {
		return 1
	}
	dpi, _, _ := pGetDpiForWindow.Call(st.hwnd)
	if dpi == 0 {
		return 1
	}
	return float64(dpi) / 96.0
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
	for _, r := range st.rows {
		from := strings.TrimSpace(getWindowText(r.hFrom))
		to := getWindowText(r.hTo)
		if from == "" || to == "" {
			continue
		}
		req.Pairs = append(req.Pairs, Pair{From: from, To: to})
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

	// 快照所需数据到工作 goroutine，避免 UI 线程竞争
	go func(req ReplaceReq) {
		done, failed := Execute(req, func(it Item) {
			st.mu.Lock()
			st.pending = append(st.pending, it)
			st.mu.Unlock()
			pPostMessageW.Call(st.hwnd, wmAppItem, 0, 0)
		})
		st.mu.Lock()
		st.done, st.failed = done, failed
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

func paintBackground(hwnd uintptr) {
	hdc, _, _ := pGetDC.Call(hwnd)
	var rc RECT
	pGetClientRect.Call(hwnd, uintptr(unsafe.Pointer(&rc)))
	tr, tg, tb := col(colorBgTop)
	br, bg, bb := col(colorBgBottom)
	verts := [2]TRIVERTEX{
		{X: 0, Y: 0, Red: tr, Green: tg, Blue: tb, Alpha: 0},
		{X: 0, Y: rc.Bottom, Red: br, Green: bg, Blue: bb, Alpha: 0},
	}
	gr := GRADIENT_RECT{UpperLeft: 0, LowerRight: 1}
	pGradientFill.Call(hdc, uintptr(unsafe.Pointer(&verts[0])), 2, uintptr(unsafe.Pointer(&gr)), 1, 1) // GRADIENT_FILL_RECT_V
	pReleaseDC.Call(hwnd, hdc)
}

// ---------- 布局 ----------
func layoutControls(st *appState) {
	var rc RECT
	pGetClientRect.Call(st.hwnd, uintptr(unsafe.Pointer(&rc)))
	s := scale(st)
	W := float64(rc.Right)
	v := func(x float64) uintptr { return uintptr(int32(x * s)) } // 逻辑像素 -> uintptr

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
	y += float64(n)*(rowH+gap)
	pMoveWindow.Call(st.hAdd, v(margin), v(y), v(120), v(rowH), 1)
	y += rowH + gap

	// 三个复选框
	cbW := 160.0
	pMoveWindow.Call(st.hRecursive, v(margin), v(y), v(cbW), v(rowH), 1)
	pMoveWindow.Call(st.hContent, v(margin+cbW+8), v(y), v(cbW), v(rowH), 1)
	pMoveWindow.Call(st.hRename, v(margin+2*(cbW+8)), v(y), v(cbW), v(rowH), 1)
	y += rowH + gap

	// 开始按钮 + 状态文本
	pMoveWindow.Call(st.hStart, v(margin), v(y), v(140), v(rowH+6), 1)
	pMoveWindow.Call(st.hStatus, v(margin+150), v(y+4), v(W-2*margin-150), v(rowH), 1)
	y += rowH + 6 + gap

	// 日志框（占据剩余空间）
	logH := int32(float64(rc.Bottom)/s - y - margin)
	if logH < 60 {
		logH = 60
	}
	pMoveWindow.Call(st.hLog, v(margin), v(y), v(W-2*margin), uintptr(logH), 1)
}

func addPairRow(st *appState, from, to string) {
	s := scale(st)
	id := len(st.rows)
	hFrom := createWindow("Edit", from, wsChild|wsVisible|wsTabstop|wsBorder, 0, 0, int32(200*s), int32(30*s), st.hwnd, idPairFromBase+uintptr(id*3))
	hTo := createWindow("Edit", to, wsChild|wsVisible|wsTabstop|wsBorder, 0, 0, int32(200*s), int32(30*s), st.hwnd, idPairFromBase+uintptr(id*3+1))
	hDel := createWindow("Button", "删除", wsChild|wsVisible|wsTabstop|bsOwnerDraw, 0, 0, int32(44*s), int32(30*s), st.hwnd, idPairFromBase+uintptr(id*3+2))
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

		// 深色标题栏（DWMWA_USE_IMMERSIVE_DARK_MODE = 20）
		val := uint32(1)
		pDwmSetWindowAttribute.Call(hwnd, 20, uintptr(unsafe.Pointer(&val)), 4)

		// 字体
		st.hFont, _, _ = pCreateFontW.Call(18, 0, 0, 0, 400, 0, 0, 0, 134, 0, 0, 0, uintptr(unsafe.Pointer(utf16ptr("微软雅黑"))))
		st.hFontBold, _, _ = pCreateFontW.Call(20, 0, 0, 0, 700, 0, 0, 0, 134, 0, 0, 0, uintptr(unsafe.Pointer(utf16ptr("微软雅黑"))))

		st.checked = map[uint32]bool{
			idRecursive: true,
			idReplaceContent: true,
			idRenameFiles: false,
		}

		// 静态标签
		st.hLblDir = createWindow("Static", "① 选择要处理的文件夹", ssLeft, 0, 0, 0, 0, hwnd, 0)
		st.hDir = createWindow("Edit", "", wsChild|wsVisible|wsTabstop|wsBorder, 0, 0, 0, 0, hwnd, idDirEdit)
		st.hBrowse = createWindow("Button", "浏览…", wsChild|wsVisible|wsTabstop|bsOwnerDraw, 0, 0, 0, 0, hwnd, idBrowseBtn)
		st.hLblRules = createWindow("Static", "② 替换规则（「替换为」留空的行不参与替换）", ssLeft, 0, 0, 0, 0, hwnd, 0)

		// 5 条预置规则
		for _, v := range []string{"AAAA", "BBBB", "CCCC", "DDDD", "EEEE"} {
			addPairRow(st, v, "")
		}

		st.hAdd = createWindow("Button", "+ 添加一组", wsChild|wsVisible|wsTabstop|bsOwnerDraw, 0, 0, 0, 0, hwnd, idAddPair)
		st.hRecursive = createWindow("Button", "包含子文件夹", wsChild|wsVisible|wsTabstop|bsOwnerDraw, 0, 0, 0, 0, hwnd, idRecursive)
		st.hContent = createWindow("Button", "替换文档内容", wsChild|wsVisible|wsTabstop|bsOwnerDraw, 0, 0, 0, 0, hwnd, idReplaceContent)
		st.hRename = createWindow("Button", "同时替换文件名", wsChild|wsVisible|wsTabstop|bsOwnerDraw, 0, 0, 0, 0, hwnd, idRenameFiles)
		st.hStart = createWindow("Button", "开始替换", wsChild|wsVisible|wsTabstop|bsOwnerDraw, 0, 0, 0, 0, hwnd, idStartBtn)
		st.hStatus = createWindow("Static", "⚠ 替换不可撤销，也不会自动备份，请先备份文件夹", ssLeft, 0, 0, 0, 0, hwnd, 0)
		st.hLog = createWindow("Edit", "", wsChild|wsVisible|wsTabstop|wsBorder|wsVScroll|esMultiline|esReadonly|esAutoHScroll, 0, 0, 0, 0, hwnd, idLogEdit)

		for _, h := range []uintptr{st.hDir, st.hBrowse, st.hAdd, st.hRecursive, st.hContent, st.hRename, st.hStart, st.hStatus, st.hLog, st.hLblDir, st.hLblRules} {
			pSendMessageW.Call(h, wmSetFont, st.hFont, 1)
		}
		pSendMessageW.Call(st.hStart, wmSetFont, st.hFontBold, 1)

		appendLog(st, "批量替换助手 已就绪。请选择文件夹并填写替换规则。\r\n")
		layoutControls(st)
		return 0

	case wmSize:
		layoutControls(st)
		return 0

	case wmPaint:
		paintBackground(hwnd)
		return 0

	case wmCtlColorEdit, wmCtlColorStatic:
		hdc := wParam
		pSetTextColor.Call(hdc, colorText)
		pSetBkColor.Call(hdc, colorPanel)
		return st.hPanelBrush // 复用画刷

	case wmDrawItem:
		dis := (*DRAWITEMSTRUCT)(unsafe.Pointer(lParam))
		id := dis.CtlID
		if id == idBrowseBtn || id == idAddPair || id == idStartBtn || (id >= idPairFromBase && (id-idPairFromBase)%3 == 2) {
			drawButton(st, dis, id == idStartBtn || id == idBrowseBtn)
		} else if id == idRecursive || id == idReplaceContent || id == idRenameFiles {
			drawCheckbox(st, dis)
		}
		return 0

	case wmCommand:
		id := uint32(wParam & 0xFFFF)
		code := uint32(wParam>>16) & 0xFFFF
		switch {
		case id == idBrowseBtn && code == bnClicked:
			path, err := pickFolder(hwnd)
			if err != nil {
				showErrorBox("批量替换助手", "选择文件夹失败："+err.Error())
			} else if path != "" {
				setWindowText(st.hDir, path)
				appendLog(st, "已选择文件夹："+path+"\r\n")
			}
		case id == idStartBtn && code == bnClicked:
			if !st.running {
				startTask(st)
			}
		case id == idAddPair && code == bnClicked:
			addPairRow(st, "", "")
			layoutControls(st)
		case id >= idPairFromBase && (id-idPairFromBase)%3 == 2 && code == bnClicked:
			removePairRow(st, id)
		case id == idRecursive || id == idReplaceContent || id == idRenameFiles:
			if code == bnClicked {
				st.checked[id] = !st.checked[id]
				var hChk uintptr
				switch id {
				case idRecursive:
					hChk = st.hRecursive
				case idReplaceContent:
					hChk = st.hContent
				case idRenameFiles:
					hChk = st.hRename
				}
				pInvalidateRect.Call(hChk, 0, 1)
			}
		}
		return 0

	case wmAppItem:
		drainItems(st)
		return 0

	case wmAppDone:
		drainItems(st)
		st.mu.Lock()
		done, failed := st.done, st.failed
		st.mu.Unlock()
		st.running = false
		pEnableWindow.Call(st.hStart, 1)
		setWindowText(st.hStatus, "完成：成功 "+itoa(done)+" 项，失败/跳过 "+itoa(failed)+" 项")
		appendLog(st, "━━━━━━━━━━━━━━━━━━━━━━━━━━━━\r\n")
		appendLog(st, "全部完成：成功 "+itoa(done)+" 项，失败/跳过 "+itoa(failed)+" 项\r\n")
		return 0

	case wmClose:
		pPostQuitMessage.Call(0)
		return 0

	case wmDestroy:
		if st.hPanelBrush != 0 {
			pDeleteObject.Call(st.hPanelBrush)
		}
		if st.hFont != 0 {
			pDeleteObject.Call(st.hFont)
		}
		if st.hFontBold != 0 {
			pDeleteObject.Call(st.hFontBold)
		}
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
	// 高 DPI 感知
	pSetProcessDpiAware.Call(dpiAwareV2)

	// 单实例：CreateMutexW 的返回 err 即 GetLastError（LazyProc.Call 语义）
	mutexName := utf16ptr("Global\\批量替换助手_SingleInstance")
	hMutex, _, mutexErr := pCreateMutexW.Call(0, 1, uintptr(unsafe.Pointer(mutexName)))
	if errno, ok := mutexErr.(syscall.Errno); ok && errno == syscall.ERROR_ALREADY_EXISTS {
		enumCB = syscall.NewCallback(enumWindowsProc)
		pEnumWindows.Call(enumCB, 0)
		pCloseHandle.Call(hMutex)
		return nil
	}
	_ = hMutex // 进程退出自动释放

	hInst, _, _ := pGetModuleHandleW.Call(0)
	className := utf16ptr("批量替换助手_Window")
	title := utf16ptr("批量替换助手")

	wndProcCB = syscall.NewCallback(wndProc)
	wc := WNDCLASSEXW{
		cbSize:        uint32(unsafe.Sizeof(WNDCLASSEXW{})),
		lpfnWndProc:   wndProcCB,
		hInstance:     hInst,
		hCursor:       32512, // IDC_ARROW
		lpszClassName: className,
	}
	classCB = uintptr(unsafe.Pointer(wc.lpszClassName)) // 保活（className 指针）
	pRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))) // 失败时 CreateWindow 会返回 0

	gState = &appState{}
	hwnd, _, _ := pCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(title)),
		0x00CF0000, // WS_OVERLAPPEDWINDOW
		0x80000000, 0x80000000, 920, 660,
		0, 0, hInst, 0,
	)
	if hwnd == 0 {
		return &uiError{"创建窗口失败"}
	}

	pShowWindow.Call(hwnd, 9) // SW_SHOWDEFAULT
	pUpdateWindow.Call(hwnd)

	var msg MSG
	for {
		r, _, _ := pGetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if r == 0 || r == ^uintptr(0) {
			break
		}
		pTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		pDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
	}
	_ = classCB
	return nil
}
