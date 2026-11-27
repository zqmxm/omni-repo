/*
 * QuarkBatch Lite v2.0 —— 夸克扫描王 · 批量文件入口（原生 Win32 版）
 *
 * 为什么有这一版：
 *   内嵌网页版必须自带 Chromium（Qt6WebEngineCore.dll 81MB），整包 135.7MB，
 *   在「单文件 8.5MB 截断」的传输通道里永远下不完整。
 *   这一版不打包任何运行时：只用 Windows 自带的 Win32 API + .NET 之外的系统组件，
 *   编译产物只有几百 KB，不可能再被截断。
 *
 * 边界（写死，不越线）：
 *   · 登录 = 用系统 Edge/Chrome 打开 scan.quark.cn，由用户本人扫码 / 验证码登录。
 *     登录态存在浏览器自己的 profile 里，软件不读取、不保存、不上传任何凭据。
 *   · 网页里「粘贴 → 点去水印 / 去手写 → 点下载 PDF」由用户本人完成。
 *     软件不注入 JS、不模拟点击、不抓接口、不绕过会员。
 *   · 软件只做：批量入口（队列 + 把当前文件放进剪贴板 + 资源管理器选中）、
 *     批量出口（下载落盘后自动归集到输出文件夹并改名）、自动切下一个。
 *
 * 依赖：Win10/11 自带 user32/gdi32/comctl32/shell32/ole32/comdlg32/shlwapi
 * 编译：zig cc -target x86_64-windows-gnu -Os -Wl,--subsystem,windows
 */

#include <windows.h>
#include <commctrl.h>
#include <shlobj.h>
#include <shlwapi.h>
#include <stdio.h>
#include <stdarg.h>
#include <wchar.h>
#include <string.h>

#define APPNAME       L"QuarkBatch 稳定版 v2.1"
#define DEFAULTPAGE   L"https://scan.quark.cn/"
#define MAXJOB        2048
#define MAXSEEN       4096
#define LEFT_W        430

/* ---------------- 控件 ID ---------------- */
#define ID_ADDFILES   1001
#define ID_ADDDIR     1002
#define ID_REMOVE     1003
#define ID_CLEAR      1004
#define ID_LIST       1010
#define ID_EDOUT      1020
#define ID_PICKOUT    1021
#define ID_OPENOUT    1022
#define ID_EDURL      1030
#define ID_OPENPAGE   1031
#define ID_REFRESH    1032
#define ID_AUTONEXT   1040
#define ID_AUTOCLIP   1041
#define ID_START      1050
#define ID_NEXT       1051
#define ID_SKIP       1052
#define ID_DONE       1053
#define ID_STOP       1054
#define ID_LOCATE     1060
#define ID_CLIP       1061
#define ID_LOG        1070
#define ID_LOGIN      1080
#define ID_EXT        1081
#define ID_LSTATE     1082
#define ID_ADDR       1083
#define ID_RIGHTBOX   1084
#define ID_RIGHTTXT   1085
#define ID_CRC        1086
#define ID_MORE       1090
#define ID_EDTPL      1091
#define ID_SAVECFG    1092
#define ID_CHKPOLL    1093

/* ---------------- 全局状态 ---------------- */
typedef struct {
    wchar_t path[MAX_PATH];
    wchar_t name[MAX_PATH];
    wchar_t size[32];
    wchar_t pages[16];
    wchar_t status[32];
    wchar_t outname[MAX_PATH];
    wchar_t note[128];
} Job;

static Job   g_jobs[MAXJOB];
static int   g_njobs = 0;
static int   g_cur = -1;
static int   g_running = 0;
static int   g_collected = 0;

static wchar_t g_out[MAX_PATH] = L"";
static wchar_t g_url[1024] = DEFAULTPAGE;
static wchar_t g_tpl[256] = L"{stem}_已处理";
static wchar_t g_ini[MAX_PATH] = L"";
static wchar_t g_self[MAX_PATH] = L"";
static int     g_autonext = 1, g_autoclip = 1, g_poll = 1;

static wchar_t g_seen[MAXSEEN][MAX_PATH];
static int     g_nseen = 0;
static ULONGLONG g_startFT = 0;

static HWND g_hwnd, g_list, g_log, g_edout, g_edurl, g_lstate, g_addr;
static HWND g_rightbox, g_righttxt, g_crc, g_edtpl;
static HWND g_chknext, g_chkclip, g_chkpoll;
static HFONT g_font, g_fontb;
static HWND g_lbltpl, g_btnsave;

static void snapshot_downloads(void);
static void next_job(void);
static const wchar_t *basename_tmp(const wchar_t *p);

/* ---------------- 小工具 ---------------- */
static void wcsncpy_s_(wchar_t *d, size_t cap, const wchar_t *s) {
    if (!s) { d[0] = 0; return; }
    wcsncpy(d, s, cap - 1); d[cap - 1] = 0;
}
static void basename_w(const wchar_t *p, wchar_t *out, size_t cap) {
    const wchar_t *s = wcsrchr(p, L'\\');
    wcsncpy_s_(out, cap, s ? s + 1 : p);
}
static void stem_w(const wchar_t *name, wchar_t *out, size_t cap) {
    wcsncpy_s_(out, cap, name);
    wchar_t *e = wcsrchr(out, L'.');
    if (e) *e = 0;
}
static void ts(wchar_t *b, size_t cap) {
    SYSTEMTIME t; GetLocalTime(&t);
    _snwprintf(b, cap, L"[%02d:%02d:%02d] ", t.wHour, t.wMinute, t.wSecond);
    b[cap - 1] = 0;
}
static void logline(const wchar_t *s) {
    if (!g_log) return;
    wchar_t b[2048]; ts(b, 2048);
    wcsncat(b, s, 2048 - wcslen(b) - 8);
    wcsncat(b, L"\r\n", 2048 - wcslen(b) - 4);
    int n = GetWindowTextLengthW(g_log);
    SendMessageW(g_log, EM_SETSEL, n, n);
    SendMessageW(g_log, EM_REPLACESEL, FALSE, (LPARAM)b);
    SendMessageW(g_log, WM_VSCROLL, SB_BOTTOM, 0);
}
static void logf(const wchar_t *fmt, ...) {
    wchar_t b[1024];
    va_list ap; va_start(ap, fmt);
    _vsnwprintf(b, 1024, fmt, ap);
    b[1023] = 0;
    va_end(ap);
    logline(b);
}

static int is_pdf(const wchar_t *p) {
    const wchar_t *e = wcsrchr(p, L'.');
    return e && _wcsicmp(e, L".pdf") == 0;
}

static wchar_t *clean_name(wchar_t *s) {
    /* 去掉 Windows 文件名非法字符，保留中文 */
    static const wchar_t *bad = L"<>:\"/\\|?*";
    wchar_t *r = s, *w = s;
    while (*r) {
        if (*r < 32 || wcschr(bad, *r)) { r++; continue; }
        *w++ = *r++;
    }
    *w = 0;
    /* 首尾空格与点 */
    while (wcslen(s) && (s[wcslen(s) - 1] == L' ' || s[wcslen(s) - 1] == L'.'))
        s[wcslen(s) - 1] = 0;
    wchar_t *p = s;
    while (*p == L' ' || *p == L'.') p++;
    if (p != s) memmove(s, p, (wcslen(p) + 1) * sizeof(wchar_t));
    if (!s[0]) wcscpy(s, L"扫描件");
    return s;
}

static void replace_tpl(wchar_t *dst, size_t cap, const wchar_t *tpl,
                        const wchar_t *stem) {
    const wchar_t *p = tpl; dst[0] = 0;
    while (*p) {
        if (wcsncmp(p, L"{stem}", 6) == 0) {
            wcsncat(dst, stem, cap - wcslen(dst) - 2);
            p += 6;
        } else {
            size_t n = wcslen(dst);
            if (n + 2 >= cap) break;
            dst[n] = *p++; dst[n + 1] = 0;
        }
    }
    clean_name(dst);
    if (wcslen(dst) > 120) dst[120] = 0;
}

static ULONGLONG ft_to_u64(const FILETIME *f) {
    ULARGE_INTEGER u; u.LowPart = f->dwLowDateTime; u.HighPart = f->dwHighDateTime;
    return u.QuadPart;
}
static ULONGLONG now_ft(void) {
    FILETIME f; GetSystemTimeAsFileTime(&f); return ft_to_u64(&f);
}
static int seen_has(const wchar_t *n) {
    for (int i = 0; i < g_nseen; i++)
        if (_wcsicmp(g_seen[i], n) == 0) return 1;
    return 0;
}
static void seen_add(const wchar_t *n) {
    if (seen_has(n)) return;
    if (g_nseen < MAXSEEN) { wcsncpy_s_(g_seen[g_nseen], MAX_PATH, n); g_nseen++; }
}
static int file_exists(const wchar_t *p) {
    return GetFileAttributesW(p) != INVALID_FILE_ATTRIBUTES;
}

/* ---------------- PDF 页数（尽力解析，失败显示 —） ---------------- */
static int pdf_pages(const wchar_t *path) {
    HANDLE h = CreateFileW(path, GENERIC_READ, FILE_SHARE_READ, NULL,
                           OPEN_EXISTING, FILE_ATTRIBUTE_NORMAL, NULL);
    if (h == INVALID_HANDLE_VALUE) return 0;
    DWORD sz = GetFileSize(h, NULL);
    if (sz > 4 * 1024 * 1024) sz = 4 * 1024 * 1024;
    char *b = (char *)malloc(sz + 1);
    DWORD got = 0; int best = 0;
    if (b && ReadFile(h, b, sz, &got, NULL)) {
        b[got] = 0;
        char *p = b;
        while ((p = strstr(p, "/Count")) != NULL) {
            p += 6;
            while (*p == ' ' || *p == '\r' || *p == '\n' || *p == '\t') p++;
            if (*p >= '0' && *p <= '9') {
                int v = 0;
                while (*p >= '0' && *p <= '9') { v = v * 10 + (*p - '0'); p++; }
                if (v > best) best = v;
            }
        }
    }
    free(b); CloseHandle(h);
    return best;
}

static void size_str(const wchar_t *path, wchar_t *out, size_t cap) {
    WIN32_FILE_ATTRIBUTE_DATA d;
    if (!GetFileAttributesExW(path, GetFileExInfoStandard, &d)) {
        wcscpy(out, L"—"); return;
    }
    ULARGE_INTEGER u; u.LowPart = d.nFileSizeLow; u.HighPart = d.nFileSizeHigh;
    double k = (double)u.QuadPart / 1024.0;
    if (k < 1024) _snwprintf(out, cap, L"%.1fKB", k);
    else _snwprintf(out, cap, L"%.1fMB", k / 1024.0);
    out[cap - 1] = 0;
}

/* ---------------- 配置 ---------------- */
static void ini_path(void) {
    wchar_t base[MAX_PATH];
    if (GetEnvironmentVariableW(L"APPDATA", base, MAX_PATH) == 0)
        GetEnvironmentVariableW(L"USERPROFILE", base, MAX_PATH);
    _snwprintf(g_ini, MAX_PATH, L"%s\\QuarkBatch", base);
    CreateDirectoryW(g_ini, NULL);
    wcsncat(g_ini, L"\\config.ini", MAX_PATH - wcslen(g_ini) - 2);
}
static void cfg_load(void) {
    ini_path();
    GetPrivateProfileStringW(L"QB", L"out_dir", L"", g_out, MAX_PATH, g_ini);
    GetPrivateProfileStringW(L"QB", L"web_url", DEFAULTPAGE, g_url, 1024, g_ini);
    GetPrivateProfileStringW(L"QB", L"tpl", L"{stem}_已处理", g_tpl, 256, g_ini);
    g_autonext = GetPrivateProfileIntW(L"QB", L"auto_next", 1, g_ini);
    g_autoclip = GetPrivateProfileIntW(L"QB", L"auto_clip", 1, g_ini);
    g_poll     = GetPrivateProfileIntW(L"QB", L"poll", 1, g_ini);
}
static void cfg_save(void) {
    ini_path();
    GetWindowTextW(g_edout, g_out, MAX_PATH);
    GetWindowTextW(g_edurl, g_url, 1024);
    if (!g_url[0]) wcscpy(g_url, DEFAULTPAGE);
    GetWindowTextW(g_edtpl, g_tpl, 256);
    if (!g_tpl[0]) wcscpy(g_tpl, L"{stem}_已处理");
    g_autonext = (SendMessageW(g_chknext, BM_GETCHECK, 0, 0) == BST_CHECKED);
    g_autoclip = (SendMessageW(g_chkclip, BM_GETCHECK, 0, 0) == BST_CHECKED);
    g_poll     = (SendMessageW(g_chkpoll, BM_GETCHECK, 0, 0) == BST_CHECKED);
    WritePrivateProfileStringW(L"QB", L"out_dir", g_out, g_ini);
    WritePrivateProfileStringW(L"QB", L"web_url", g_url, g_ini);
    WritePrivateProfileStringW(L"QB", L"tpl", g_tpl, g_ini);
    wchar_t n[16];
    _snwprintf(n, 16, L"%d", g_autonext); WritePrivateProfileStringW(L"QB", L"auto_next", n, g_ini);
    _snwprintf(n, 16, L"%d", g_autoclip); WritePrivateProfileStringW(L"QB", L"auto_clip", n, g_ini);
    _snwprintf(n, 16, L"%d", g_poll);     WritePrivateProfileStringW(L"QB", L"poll", n, g_ini);
}

/* ---------------- ListView 宽字符封装 ----------------
   mingw 的 ListView_SetItemText / InsertItem 宏内部用的是 ANSI 的 LV_ITEM，
   直接传 wchar_t* 会类型不符。这里显式走 W 版消息。 */
static void lv_insert_item(HWND lv, int i, const wchar_t *s) {
    LVITEMW it; memset(&it, 0, sizeof(it));
    it.mask = LVIF_TEXT; it.iItem = i; it.pszText = (LPWSTR)s;
    SendMessageW(lv, LVM_INSERTITEMW, 0, (LPARAM)&it);
}
static void lv_set_text(HWND lv, int i, int sub, const wchar_t *s) {
    LVITEMW it; memset(&it, 0, sizeof(it));
    it.iSubItem = sub; it.pszText = (LPWSTR)s;
    SendMessageW(lv, LVM_SETITEMTEXTW, (WPARAM)i, (LPARAM)&it);
}
static void lv_insert_col(HWND lv, int i, const wchar_t *s, int cx) {
    LVCOLUMNW c; memset(&c, 0, sizeof(c));
    c.mask = LVCF_TEXT | LVCF_WIDTH | LVCF_SUBITEM;
    c.iSubItem = i; c.pszText = (LPWSTR)s; c.cx = cx;
    SendMessageW(lv, LVM_INSERTCOLUMNW, (WPARAM)i, (LPARAM)&c);
}

/* ---------------- 队列 ---------------- */
static int job_exists(const wchar_t *p) {
    for (int i = 0; i < g_njobs; i++)
        if (_wcsicmp(g_jobs[i].path, p) == 0) return 1;
    return 0;
}
static void job_add(const wchar_t *p) {
    if (!is_pdf(p) || g_njobs >= MAXJOB || job_exists(p)) return;
    if (!file_exists(p)) return;
    Job *j = &g_jobs[g_njobs++];
    wcsncpy_s_(j->path, MAX_PATH, p);
    basename_w(p, j->name, MAX_PATH);
    size_str(p, j->size, 32);
    int pg = pdf_pages(p);
    if (pg > 0) _snwprintf(j->pages, 16, L"%d", pg); else wcscpy(j->pages, L"—");
    wcscpy(j->status, L"待处理");
    j->outname[0] = 0; j->note[0] = 0;
}
static void list_rebuild(void) {
    if (!g_list) return;
    SendMessageW(g_list, WM_SETREDRAW, FALSE, 0);
    ListView_DeleteAllItems(g_list);
    for (int i = 0; i < g_njobs; i++) {
        wchar_t num[16]; _snwprintf(num, 16, L"%d", i + 1);
        lv_insert_item(g_list, i, num);
        lv_set_text(g_list, i, 1, g_jobs[i].name);
        lv_set_text(g_list, i, 2, g_jobs[i].pages);
        lv_set_text(g_list, i, 3, g_jobs[i].size);
        lv_set_text(g_list, i, 4, g_jobs[i].status);
    }
    if (g_cur >= 0 && g_cur < g_njobs) {
        ListView_SetItemState(g_list, g_cur, LVIS_SELECTED | LVIS_FOCUSED,
                              LVIS_SELECTED | LVIS_FOCUSED);
        ListView_EnsureVisible(g_list, g_cur, FALSE);
    }
    SendMessageW(g_list, WM_SETREDRAW, TRUE, 0);
    InvalidateRect(g_list, NULL, TRUE);
}
static void add_paths_from_drop(HDROP h) {
    UINT n = DragQueryFileW(h, 0xFFFFFFFF, NULL, 0);
    int added = 0;
    for (UINT i = 0; i < n; i++) {
        wchar_t p[MAX_PATH]; DragQueryFileW(h, i, p, MAX_PATH);
        DWORD a = GetFileAttributesW(p);
        if (a & FILE_ATTRIBUTE_DIRECTORY) {
            wchar_t pat[MAX_PATH]; _snwprintf(pat, MAX_PATH, L"%s\\*.pdf", p);
            WIN32_FIND_DATAW fd; HANDLE f = FindFirstFileW(pat, &fd);
            if (f != INVALID_HANDLE_VALUE) {
                do {
                    if (fd.dwFileAttributes & FILE_ATTRIBUTE_DIRECTORY) continue;
                    wchar_t full[MAX_PATH];
                    _snwprintf(full, MAX_PATH, L"%s\\%s", p, fd.cFileName);
                    int before = g_njobs; job_add(full);
                    if (g_njobs > before) added++;
                } while (FindNextFileW(f, &fd));
                FindClose(f);
            }
        } else {
            int before = g_njobs; job_add(p);
            if (g_njobs > before) added++;
        }
    }
    if (added) { list_rebuild(); logf(L"已加入 %d 个 PDF，共 %d 个", added, g_njobs); }
}

/* ---------------- 文件对话框 ---------------- */
static void add_files_dialog(void) {
    wchar_t buf[65536] = L"";
    OPENFILENAMEW of; memset(&of, 0, sizeof(of));
    of.lStructSize = sizeof(of);
    of.hwndOwner = g_hwnd;
    of.lpstrFilter = L"PDF 文件 (*.pdf)\0*.pdf\0所有文件\0*.*\0\0";
    of.lpstrFile = buf; of.nMaxFile = 65536;
    of.Flags = OFN_EXPLORER | OFN_ALLOWMULTISELECT | OFN_FILEMUSTEXIST | OFN_HIDEREADONLY;
    of.lpstrTitle = L"选择 PDF 文件";
    if (!GetOpenFileNameW(&of)) return;
    wchar_t dir[MAX_PATH];
    wcsncpy_s_(dir, MAX_PATH, buf);
    wchar_t *p = buf + wcslen(buf) + 1;
    int added = 0;
    if (*p == 0) {
        int b = g_njobs; job_add(dir); if (g_njobs > b) added++;
    } else {
        while (*p) {
            wchar_t full[MAX_PATH];
            _snwprintf(full, MAX_PATH, L"%s\\%s", dir, p);
            int b = g_njobs; job_add(full); if (g_njobs > b) added++;
            p += wcslen(p) + 1;
        }
    }
    if (added) { list_rebuild(); logf(L"已加入 %d 个 PDF，共 %d 个", added, g_njobs); }
}
static void add_dir_dialog(void) {
    BROWSEINFOW bi; memset(&bi, 0, sizeof(bi));
    bi.hwndOwner = g_hwnd;
    bi.lpszTitle = L"选择装有 PDF 的文件夹";
    bi.ulFlags = BIF_RETURNONLYFSDIRS | BIF_NEWDIALOGSTYLE;
    LPITEMIDLIST pidl = SHBrowseForFolderW(&bi);
    if (!pidl) return;
    wchar_t d[MAX_PATH];
    if (SHGetPathFromIDListW(pidl, d)) {
        wchar_t pat[MAX_PATH]; _snwprintf(pat, MAX_PATH, L"%s\\*.pdf", d);
        WIN32_FIND_DATAW fd; HANDLE f = FindFirstFileW(pat, &fd);
        int added = 0;
        if (f != INVALID_HANDLE_VALUE) {
            do {
                if (fd.dwFileAttributes & FILE_ATTRIBUTE_DIRECTORY) continue;
                wchar_t full[MAX_PATH];
                _snwprintf(full, MAX_PATH, L"%s\\%s", d, fd.cFileName);
                int b = g_njobs; job_add(full); if (g_njobs > b) added++;
            } while (FindNextFileW(f, &fd));
            FindClose(f);
        }
        if (added) { list_rebuild(); logf(L"从文件夹加入 %d 个 PDF，共 %d 个", added, g_njobs); }
        else logline(L"该文件夹里没有 PDF");
    }
    CoTaskMemFree(pidl);
}
static void pick_out_dialog(void) {
    BROWSEINFOW bi; memset(&bi, 0, sizeof(bi));
    bi.hwndOwner = g_hwnd;
    bi.lpszTitle = L"选择成品 PDF 输出文件夹";
    bi.ulFlags = BIF_RETURNONLYFSDIRS | BIF_NEWDIALOGSTYLE;
    LPITEMIDLIST pidl = SHBrowseForFolderW(&bi);
    if (!pidl) return;
    wchar_t d[MAX_PATH];
    if (SHGetPathFromIDListW(pidl, d)) {
        SetWindowTextW(g_edout, d);
        GetWindowTextW(g_edout, g_out, MAX_PATH);
        snapshot_downloads();
        logf(L"输出文件夹：%s", d);
    }
    CoTaskMemFree(pidl);
}

/* ---------------- 剪贴板 / 资源管理器 ---------------- */
static void select_in_explorer(const wchar_t *p) {
    wchar_t args[2048];
    _snwprintf(args, 2048, L"/select,\"%s\"", p);
    ShellExecuteW(NULL, L"open", L"explorer.exe", args, NULL, SW_SHOWNORMAL);
    logf(L"已在资源管理器选中：%s", basename_tmp(p));
}
static const wchar_t *basename_tmp(const wchar_t *p) {
    const wchar_t *s = wcsrchr(p, L'\\');
    return s ? s + 1 : p;
}
static void clip_current(void) {
    if (g_cur < 0 || g_cur >= g_njobs) { logline(L"当前没有文件"); return; }
    const wchar_t *path = g_jobs[g_cur].path;
    size_t bytes = sizeof(DROPFILES) + (wcslen(path) + 2) * sizeof(wchar_t);
    HGLOBAL h = GlobalAlloc(GHND | GMEM_DDESHARE, bytes);
    if (!h) return;
    unsigned char *base = (unsigned char *)GlobalLock(h);
    DROPFILES df; memset(&df, 0, sizeof(df));
    df.pFiles = sizeof(DROPFILES); df.fWide = TRUE;
    memcpy(base, &df, sizeof(DROPFILES));
    wchar_t *names = (wchar_t *)(base + sizeof(DROPFILES));
    wcscpy(names, path);
    names[wcslen(path) + 1] = 0;      /* 双 NUL 结尾 */
    GlobalUnlock(h);
    HGLOBAL ht = GlobalAlloc(GHND | GMEM_DDESHARE,
                             (wcslen(path) + 1) * sizeof(wchar_t));
    if (ht) {
        wchar_t *t = (wchar_t *)GlobalLock(ht);
        wcscpy(t, path); GlobalUnlock(ht);
    }
    if (OpenClipboard(g_hwnd)) {
        EmptyClipboard();
        SetClipboardData(CF_HDROP, h);
        if (ht) SetClipboardData(CF_UNICODETEXT, ht);
        CloseClipboard();
    }
    logf(L"已把文件放进剪贴板：%s（在网页里 Ctrl+V 粘贴）", g_jobs[g_cur].name);
    if (g_autoclip) select_in_explorer(path);
}

/* ---------------- 打开网页（系统浏览器） ---------------- */
static int find_browser(wchar_t *out, size_t cap) {
    const wchar_t *cands[] = {
        L"C:\\Program Files (x86)\\Microsoft\\Edge\\Application\\msedge.exe",
        L"C:\\Program Files\\Microsoft\\Edge\\Application\\msedge.exe",
        L"C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe",
        L"C:\\Program Files (x86)\\Google\\Chrome\\Application\\chrome.exe",
        NULL
    };
    for (int i = 0; cands[i]; i++)
        if (file_exists(cands[i])) { wcsncpy_s_(out, cap, cands[i]); return 1; }
    return 0;
}
static void open_web(int login) {
    GetWindowTextW(g_edurl, g_url, 1024);
    if (!g_url[0]) wcscpy(g_url, DEFAULTPAGE);
    wchar_t exe[MAX_PATH];
    if (find_browser(exe, MAX_PATH)) {
        /* 用 --app 打开成独立窗口；不指定 user-data-dir，复用你平时的登录态 */
        wchar_t cmd[4096];
        _snwprintf(cmd, 4096, L"\"%s\" --app=\"%s\" --window-size=1100,820", exe, g_url);
        STARTUPINFOW si; PROCESS_INFORMATION pi;
        memset(&si, 0, sizeof(si)); si.cb = sizeof(si);
        memset(&pi, 0, sizeof(pi));
        if (CreateProcessW(NULL, cmd, NULL, NULL, FALSE, 0, NULL, NULL, &si, &pi)) {
            CloseHandle(pi.hThread); CloseHandle(pi.hProcess);
        } else {
            ShellExecuteW(NULL, L"open", g_url, NULL, NULL, SW_SHOWNORMAL);
        }
    } else {
        ShellExecuteW(NULL, L"open", g_url, NULL, NULL, SW_SHOWNORMAL);
    }
    SetWindowTextW(g_addr, g_url);
    if (login) {
        SetWindowTextW(g_lstate, L"● 已打开浏览器，请在窗口里登录");
        logline(L"已打开夸克扫描王网页。请用「手机号验证码」或「夸克 App 扫码」登录你的会员账号。");
        logline(L"软件不读取密码 / 验证码 / Cookie；登录态留在浏览器自己的 profile 里，扫码一次即可。");
    } else {
        logf(L"已打开页面：%s", g_url);
    }
}

/* ---------------- 下载快照 / 归集 ---------------- */
static void watch_dirs(wchar_t d1[MAX_PATH], wchar_t d2[MAX_PATH]) {
    GetWindowTextW(g_edout, g_out, MAX_PATH);
    wcsncpy_s_(d1, MAX_PATH, g_out);
    d2[0] = 0;
    wchar_t up[MAX_PATH];
    if (GetEnvironmentVariableW(L"USERPROFILE", up, MAX_PATH))
        _snwprintf(d2, MAX_PATH, L"%s\\Downloads", up);
}
static void snapshot_downloads(void) {
    g_nseen = 0;
    wchar_t d1[MAX_PATH], d2[MAX_PATH]; watch_dirs(d1, d2);
    wchar_t *dirs[2] = { d1, d2 };
    for (int k = 0; k < 2; k++) {
        if (!dirs[k][0] || !file_exists(dirs[k])) continue;
        wchar_t pat[MAX_PATH]; _snwprintf(pat, MAX_PATH, L"%s\\*.pdf", dirs[k]);
        WIN32_FIND_DATAW fd; HANDLE f = FindFirstFileW(pat, &fd);
        if (f == INVALID_HANDLE_VALUE) continue;
        do {
            if (!(fd.dwFileAttributes & FILE_ATTRIBUTE_DIRECTORY)) seen_add(fd.cFileName);
        } while (FindNextFileW(f, &fd));
        FindClose(f);
    }
}
static void target_name(Job *j, wchar_t *out, size_t cap) {
    wchar_t stem[MAX_PATH], base[MAX_PATH];
    stem_w(j->name, stem, MAX_PATH);
    replace_tpl(base, MAX_PATH, g_tpl, stem);
    _snwprintf(out, cap, L"%s\\%s.pdf", g_out, base);
    out[cap - 1] = 0;
    if (!file_exists(out)) return;
    for (int k = 1; k < 1000; k++) {
        _snwprintf(out, cap, L"%s\\%s（%d）.pdf", g_out, base, k);
        out[cap - 1] = 0;
        if (!file_exists(out)) return;
    }
}
static void collect_done(const wchar_t *srcname, const wchar_t *how) {
    if (g_cur < 0 || g_cur >= g_njobs || g_collected) return;
    g_collected = 1;
    Job *j = &g_jobs[g_cur];
    wchar_t dst[MAX_PATH]; target_name(j, dst, MAX_PATH);
    wchar_t bn[MAX_PATH]; basename_w(dst, bn, MAX_PATH);
    wcsncpy_s_(j->outname, MAX_PATH, bn);
    wcsncpy_s_(j->note, 128, how);
    wcscpy(j->status, L"已完成");
    list_rebuild();
    logf(L"已归集 → %s", bn);
    seen_add(srcname);
    if (g_autonext && g_running) next_job();
}
static void poll_downloads(void) {
    if (!g_running || !g_poll || g_collected) return;
    if (g_cur < 0 || g_cur >= g_njobs) return;
    if (!g_out[0]) return;
    CreateDirectoryW(g_out, NULL);
    wchar_t d1[MAX_PATH], d2[MAX_PATH]; watch_dirs(d1, d2);
    wchar_t *dirs[2] = { d1, d2 };
    wchar_t bestPath[MAX_PATH] = L"", bestName[MAX_PATH] = L"";
    ULONGLONG bestFT = 0;
    ULONGLONG limit = g_startFT > 20000000ULL ? g_startFT - 20000000ULL : 0;
    for (int k = 0; k < 2; k++) {
        if (!dirs[k][0] || !file_exists(dirs[k])) continue;
        wchar_t pat[MAX_PATH]; _snwprintf(pat, MAX_PATH, L"%s\\*.pdf", dirs[k]);
        WIN32_FIND_DATAW fd; HANDLE f = FindFirstFileW(pat, &fd);
        if (f == INVALID_HANDLE_VALUE) continue;
        do {
            if (fd.dwFileAttributes & FILE_ATTRIBUTE_DIRECTORY) continue;
            if (seen_has(fd.cFileName)) continue;
            ULARGE_INTEGER sz; sz.LowPart = fd.nFileSizeLow; sz.HighPart = fd.nFileSizeHigh;
            if (sz.QuadPart < 1024) continue;
            ULONGLONG ft = ft_to_u64(&fd.ftLastWriteTime);
            if (ft < limit) continue;
            if (ft >= bestFT) {
                bestFT = ft;
                wcsncpy_s_(bestName, MAX_PATH, fd.cFileName);
                _snwprintf(bestPath, MAX_PATH, L"%s\\%s", dirs[k], fd.cFileName);
            }
        } while (FindNextFileW(f, &fd));
        FindClose(f);
    }
    if (!bestPath[0]) return;
    wchar_t dst[MAX_PATH]; target_name(&g_jobs[g_cur], dst, MAX_PATH);
    if (_wcsicmp(bestPath, dst) != 0) {
        if (!CopyFileW(bestPath, dst, FALSE)) {
            logf(L"（兼容模式）归集失败：复制出错 %s", bestName);
            return;
        }
        DeleteFileW(bestPath);       /* 尽力删源，删不掉也已加入 seen */
    }
    seen_add(bestName);
    logf(L"（兼容模式）检测到新下载：%s", bestName);
    collect_done(bestName, L"下载目录轮询归集");
}

/* ---------------- 状态机 ---------------- */
static void set_running(int on) {
    g_running = on;
    EnableWindow(GetDlgItem(g_hwnd, ID_START), !on);
    EnableWindow(GetDlgItem(g_hwnd, ID_STOP), on);
}
static void start_batch(void) {
    GetWindowTextW(g_edout, g_out, MAX_PATH);
    if (!g_njobs) { logline(L"❌ 先导入 PDF"); return; }
    if (!g_out[0]) { logline(L"❌ 先选「输出文件夹」"); return; }
    if (!CreateDirectoryW(g_out, NULL) && !file_exists(g_out)) {
        logline(L"❌ 输出文件夹不可用"); return;
    }
    cfg_save();
    g_startFT = now_ft();
    snapshot_downloads();
    for (int i = 0; i < g_njobs; i++) {
        if (wcscmp(g_jobs[i].status, L"已完成") != 0) {
            wcscpy(g_jobs[i].status, L"待处理");
            g_jobs[i].outname[0] = 0; g_jobs[i].note[0] = 0;
        }
    }
    g_cur = -1;
    set_running(1);
    logline(L"—— 开始批量辅助 ——");
    next_job();
}
static void next_job(void) {
    int nxt = -1;
    for (int i = g_cur + 1; i < g_njobs; i++)
        if (wcscmp(g_jobs[i].status, L"待处理") == 0) { nxt = i; break; }
    if (nxt < 0) {
        g_cur = -1; list_rebuild();
        logline(L"✔ 队列全部处理完，点「打开输出文件夹」看成品");
        set_running(0);
        return;
    }
    g_cur = nxt; g_collected = 0;
    wcscpy(g_jobs[nxt].status, L"处理中");
    g_jobs[nxt].outname[0] = 0;
    list_rebuild();
    logf(L"[%d/%d] 在网页 Ctrl+V 粘贴 → 点去水印 / 去手写 → 点下载 PDF。", nxt + 1, g_njobs);
    logline(L"下载完成后软件会自动归集改名，并自动切到下一个。");
    clip_current();
}
static void skip_current(void) {
    if (g_cur < 0 || g_cur >= g_njobs) { logline(L"当前没有文件"); return; }
    g_collected = 1;
    wcscpy(g_jobs[g_cur].status, L"跳过");
    wcscpy(g_jobs[g_cur].note, L"手动跳过");
    logf(L"已跳过：%s", g_jobs[g_cur].name);
    if (g_autonext && g_running) next_job(); else list_rebuild();
}
static void mark_done(void) {
    if (g_cur < 0 || g_cur >= g_njobs) { logline(L"当前没有文件"); return; }
    g_collected = 1;
    wcscpy(g_jobs[g_cur].status, L"已完成");
    wcscpy(g_jobs[g_cur].note, L"手动标记完成");
    logf(L"已标记完成：%s", g_jobs[g_cur].name);
    if (g_autonext && g_running) next_job(); else list_rebuild();
}
static void remove_selected(void) {
    int idx[2048], n = 0;
    int it = ListView_GetNextItem(g_list, -1, LVNI_SELECTED);
    while (it >= 0 && n < 2048) {
        idx[n++] = it;
        it = ListView_GetNextItem(g_list, it, LVNI_SELECTED);
    }
    for (int k = n - 1; k >= 0; k--) {
        int r = idx[k];
        if (r < 0 || r >= g_njobs) continue;
        memmove(&g_jobs[r], &g_jobs[r + 1], (g_njobs - r - 1) * sizeof(Job));
        g_njobs--;
        if (g_cur == r) g_cur = -1;
        else if (g_cur > r) g_cur--;
    }
    if (n) { list_rebuild(); logf(L"已移除 %d 项", n); }
}

/* ---------------- CRC32 ---------------- */
static unsigned int crc32_file(const wchar_t *p) {
    unsigned int tbl[256];
    for (unsigned int i = 0; i < 256; i++) {
        unsigned int c = i;
        for (int k = 0; k < 8; k++) c = (c & 1) ? (0xEDB88320u ^ (c >> 1)) : (c >> 1);
        tbl[i] = c;
    }
    HANDLE h = CreateFileW(p, GENERIC_READ, FILE_SHARE_READ, NULL,
                           OPEN_EXISTING, FILE_ATTRIBUTE_NORMAL, NULL);
    if (h == INVALID_HANDLE_VALUE) return 0;
    unsigned int c = 0xFFFFFFFFu;
    unsigned char buf[65536]; DWORD got;
    while (ReadFile(h, buf, sizeof(buf), &got, NULL) && got) {
        for (DWORD i = 0; i < got; i++) c = tbl[(c ^ buf[i]) & 0xff] ^ (c >> 8);
    }
    CloseHandle(h);
    return c ^ 0xFFFFFFFFu;
}

/* ---------------- 界面 ---------------- */
static HWND mkbtn(int id, const wchar_t *t, int x, int y, int w, int h, HWND par) {
    return CreateWindowW(L"BUTTON", t, WS_CHILD | WS_VISIBLE | BS_PUSHBUTTON,
                         x, y, w, h, par, (HMENU)(INT_PTR)id, NULL, NULL);
}
static HWND mkedit(int id, const wchar_t *t, int x, int y, int w, int h,
                   HWND par, int readonly) {
    return CreateWindowW(L"EDIT", t,
        WS_CHILD | WS_VISIBLE | WS_BORDER | ES_AUTOHSCROLL |
        (readonly ? ES_READONLY : 0),
        x, y, w, h, par, (HMENU)(INT_PTR)id, NULL, NULL);
}
static HWND mklabel(const wchar_t *t, int x, int y, int w, int h, HWND par, int id) {
    return CreateWindowW(L"STATIC", t, WS_CHILD | WS_VISIBLE | SS_LEFT,
                         x, y, w, h, par, (HMENU)(INT_PTR)id, NULL, NULL);
}
static HWND mkchk(int id, const wchar_t *t, int x, int y, int w, int h, HWND par) {
    return CreateWindowW(L"BUTTON", t, WS_CHILD | WS_VISIBLE | BS_AUTOCHECKBOX,
                         x, y, w, h, par, (HMENU)(INT_PTR)id, NULL, NULL);
}

static BOOL CALLBACK setfont_cb(HWND h, LPARAM lp) {
    SendMessageW(h, WM_SETFONT, (WPARAM)lp, TRUE);
    return TRUE;
}

static void layout(int W, int H) {
    if (!g_hwnd || !g_list || !g_log) return;   /* WM_SIZE 可能早于 build_ui */
    if (W < 700 || H < 500) return;
    int LW = LEFT_W;
    int RW = W - LW - 22;
    /* 左侧 */
    MoveWindow(GetDlgItem(g_hwnd, ID_ADDFILES), 8, 8, 100, 26, TRUE);
    MoveWindow(GetDlgItem(g_hwnd, ID_ADDDIR), 112, 8, 100, 26, TRUE);
    MoveWindow(GetDlgItem(g_hwnd, ID_REMOVE), 216, 8, 90, 26, TRUE);
    MoveWindow(GetDlgItem(g_hwnd, ID_CLEAR), 310, 8, 80, 26, TRUE);
    int listY = 42, listH = (int)(H * 0.26);
    MoveWindow(g_list, 8, listY, LW - 16, listH, TRUE);
    int y = listY + listH + 8;
    /* 路径组 */
    MoveWindow(GetDlgItem(g_hwnd, 1201), 8, y, LW - 16, 78, TRUE);
    MoveWindow(GetDlgItem(g_hwnd, 1211), 16, y + 20, 66, 20, TRUE);
    MoveWindow(g_edout, 84, y + 17, LW - 16 - 84 - 76, 22, TRUE);
    MoveWindow(GetDlgItem(g_hwnd, ID_PICKOUT), LW - 84, y + 17, 60, 22, TRUE);
    MoveWindow(GetDlgItem(g_hwnd, 1212), 16, y + 48, 66, 20, TRUE);
    MoveWindow(g_edurl, 84, y + 45, LW - 16 - 84 - 138, 22, TRUE);
    MoveWindow(GetDlgItem(g_hwnd, ID_OPENPAGE), LW - 138, y + 45, 62, 22, TRUE);
    MoveWindow(GetDlgItem(g_hwnd, ID_REFRESH), LW - 72, y + 45, 52, 22, TRUE);
    y += 86;
    MoveWindow(g_chknext, 8, y, LW - 16, 20, TRUE); y += 23;
    MoveWindow(g_chkclip, 8, y, LW - 16, 20, TRUE); y += 26;
    int bw = (LW - 16 - 4 * 6) / 5;
    HWND bs[5] = { GetDlgItem(g_hwnd, ID_START), GetDlgItem(g_hwnd, ID_NEXT),
                   GetDlgItem(g_hwnd, ID_SKIP), GetDlgItem(g_hwnd, ID_DONE),
                   GetDlgItem(g_hwnd, ID_STOP) };
    for (int i = 0; i < 5; i++) MoveWindow(bs[i], 8 + i * (bw + 6), y, bw, 28, TRUE);
    y += 32;
    int aw = (LW - 16 - 2 * 6) / 3;
    HWND as[3] = { GetDlgItem(g_hwnd, ID_LOCATE), GetDlgItem(g_hwnd, ID_CLIP),
                   GetDlgItem(g_hwnd, ID_OPENOUT) };
    for (int i = 0; i < 3; i++) MoveWindow(as[i], 8 + i * (aw + 6), y, aw, 28, TRUE);
    y += 32;
    MoveWindow(GetDlgItem(g_hwnd, ID_MORE), 8, y, LW - 16, 22, TRUE); y += 24;
    /* 更多设置展开时占 60px；隐藏时日志上移 */
    int moreOn = IsWindowVisible(g_edtpl);
    int moreH = moreOn ? 60 : 0;
    MoveWindow(g_lbltpl, 12, y, 78, 20, TRUE);
    MoveWindow(g_edtpl, 92, y, LW - 16 - 92 - 8, 22, TRUE);
    MoveWindow(g_chkpoll, 12, y + 26, LW - 24, 20, TRUE);
    MoveWindow(g_btnsave, 12, y + 50, 90, 22, TRUE);
    y += moreH + 4;
    int logH = H - y - 76;
    if (logH < 60) logH = 60;
    MoveWindow(g_log, 8, y, LW - 16, logH, TRUE);
    int ny = y + logH + 6;
    MoveWindow(GetDlgItem(g_hwnd, 1221), 8, ny, LW - 16, 18, TRUE);
    MoveWindow(GetDlgItem(g_hwnd, 1222), 8, ny + 18, LW - 16, 18, TRUE);
    MoveWindow(g_crc, 8, ny + 38, LW - 16, 18, TRUE);

    /* 右侧 */
    int RX = LW + 6;
    MoveWindow(GetDlgItem(g_hwnd, 1231), RX, 8, 320, 22, TRUE);
    MoveWindow(g_lstate, RX + 326, 8, 200, 22, TRUE);
    MoveWindow(GetDlgItem(g_hwnd, ID_LOGIN), RX + RW - 170, 6, 170, 28, TRUE);
    MoveWindow(GetDlgItem(g_hwnd, ID_EXT), RX + RW - 170, 38, 170, 24, TRUE);
    MoveWindow(g_addr, RX, 40, RW - 180, 22, TRUE);
    int boxY = 70, boxH = H - boxY - 70;
    if (boxH < 100) boxH = 100;
    MoveWindow(g_rightbox, RX, boxY, RW, boxH, TRUE);
    MoveWindow(g_righttxt, RX + 20, boxY + 20, RW - 40, boxH - 40, TRUE);
    MoveWindow(GetDlgItem(g_hwnd, 1232), RX, H - 62, RW, 56, TRUE);
}

static LRESULT CALLBACK WndProc(HWND h, UINT m, WPARAM w, LPARAM l) {
    switch (m) {
    case WM_CREATE: {
        INITCOMMONCONTROLSEX ic; ic.dwSize = sizeof(ic);
        ic.dwICC = ICC_LISTVIEW_CLASSES | ICC_WIN95_CLASSES;
        InitCommonControlsEx(&ic);
        return 0;
    }
    case WM_SIZE:
        layout(LOWORD(l), HIWORD(l));
        return 0;
    case WM_DROPFILES:
        add_paths_from_drop((HDROP)w);
        DragFinish((HDROP)w);
        return 0;
    case WM_TIMER:
        if (w == 1) poll_downloads();
        return 0;
    case WM_COMMAND: {
        int id = LOWORD(w);
        switch (id) {
        case ID_ADDFILES: add_files_dialog(); break;
        case ID_ADDDIR: add_dir_dialog(); break;
        case ID_REMOVE: remove_selected(); break;
        case ID_CLEAR:
            g_njobs = 0; g_cur = -1; set_running(0); list_rebuild();
            logline(L"已清空队列"); break;
        case ID_PICKOUT: pick_out_dialog(); break;
        case ID_OPENOUT:
            GetWindowTextW(g_edout, g_out, MAX_PATH);
            if (g_out[0]) { CreateDirectoryW(g_out, NULL);
                ShellExecuteW(NULL, L"open", g_out, NULL, NULL, SW_SHOWNORMAL); }
            else logline(L"还没选输出文件夹");
            break;
        case ID_OPENPAGE: open_web(0); break;
        case ID_REFRESH: open_web(0); break;
        case ID_LOGIN: open_web(1); break;
        case ID_EXT:
            GetWindowTextW(g_edurl, g_url, 1024);
            ShellExecuteW(NULL, L"open", g_url[0] ? g_url : DEFAULTPAGE,
                          NULL, NULL, SW_SHOWNORMAL);
            logline(L"已用系统默认浏览器打开（排障用）");
            break;
        case ID_START: start_batch(); break;
        case ID_NEXT: if (g_running) next_job(); else logline(L"点「开始」后才进入批量流程"); break;
        case ID_SKIP: skip_current(); break;
        case ID_DONE: mark_done(); break;
        case ID_STOP: set_running(0); logline(L"■ 已停止（队列与已完成文件保留）"); break;
        case ID_LOCATE:
            if (g_cur >= 0 && g_cur < g_njobs) select_in_explorer(g_jobs[g_cur].path);
            else logline(L"当前没有文件");
            break;
        case ID_CLIP: clip_current(); break;
        case ID_SAVECFG: cfg_save(); logline(L"✔ 设置已保存（%APPDATA%\\QuarkBatch\\config.ini）"); break;
        case ID_MORE: {
            int on = SendMessageW(GetDlgItem(h, ID_MORE), BM_GETCHECK, 0, 0) == BST_CHECKED;
            ShowWindow(g_lbltpl, on ? SW_SHOW : SW_HIDE);
            ShowWindow(g_edtpl, on ? SW_SHOW : SW_HIDE);
            ShowWindow(g_chkpoll, on ? SW_SHOW : SW_HIDE);
            ShowWindow(g_btnsave, on ? SW_SHOW : SW_HIDE);
            SetWindowTextW(GetDlgItem(h, ID_MORE),
                on ? L"▾ 更多设置（命名模板 / 兼容模式）" : L"▸ 更多设置（命名模板 / 兼容模式）");
            RECT rc; GetClientRect(h, &rc);
            layout(rc.right, rc.bottom);
            break;
        }
        case ID_AUTONEXT: g_autonext = SendMessageW(g_chknext, BM_GETCHECK, 0, 0) == BST_CHECKED; break;
        case ID_AUTOCLIP: g_autoclip = SendMessageW(g_chkclip, BM_GETCHECK, 0, 0) == BST_CHECKED; break;
        case ID_CHKPOLL: g_poll = SendMessageW(g_chkpoll, BM_GETCHECK, 0, 0) == BST_CHECKED; break;
        }
        return 0;
    }
    case WM_DESTROY:
        cfg_save();
        KillTimer(h, 1);
        PostQuitMessage(0);
        return 0;
    }
    return DefWindowProcW(h, m, w, l);
}

static void build_ui(HWND h) {
    g_hwnd = h;
    /* 左侧 */
    mkbtn(ID_ADDFILES, L"添加 PDF 文件", 8, 8, 100, 26, h);
    mkbtn(ID_ADDDIR, L"添加文件夹", 112, 8, 100, 26, h);
    mkbtn(ID_REMOVE, L"移除选中", 216, 8, 90, 26, h);
    mkbtn(ID_CLEAR, L"清空队列", 310, 8, 80, 26, h);

    g_list = CreateWindowW(WC_LISTVIEWW, L"",
        WS_CHILD | WS_VISIBLE | WS_BORDER | LVS_REPORT | LVS_SINGLESEL,
        8, 42, LEFT_W - 16, 200, h, (HMENU)(INT_PTR)ID_LIST, NULL, NULL);
    SendMessageW(g_list, LVM_SETEXTENDEDLISTVIEWSTYLE, 0,
                 LVS_EX_FULLROWSELECT | LVS_EX_GRIDLINES | LVS_EX_DOUBLEBUFFER);
    const wchar_t *cols[] = { L"#", L"文件名", L"页", L"大小", L"状态" };
    int cw[] = { 36, 186, 40, 62, 78 };
    for (int i = 0; i < 5; i++) lv_insert_col(g_list, i, cols[i], cw[i]);

    CreateWindowW(L"BUTTON", L"路径", WS_CHILD | WS_VISIBLE | BS_GROUPBOX,
                  8, 250, LEFT_W - 16, 78, h, (HMENU)(INT_PTR)1201, NULL, NULL);
    mklabel(L"输出文件夹", 16, 270, 66, 20, h, 1211);
    g_edout = mkedit(ID_EDOUT, L"", 84, 267, LEFT_W - 16 - 84 - 76, 22, h, 0);
    mkbtn(ID_PICKOUT, L"选择…", LEFT_W - 84, 267, 60, 22, h);
    mklabel(L"网页地址", 16, 298, 66, 20, h, 1212);
    g_edurl = mkedit(ID_EDURL, DEFAULTPAGE, 84, 295,
                     LEFT_W - 16 - 84 - 138, 22, h, 0);
    mkbtn(ID_OPENPAGE, L"打开页面", LEFT_W - 138, 295, 62, 22, h);
    mkbtn(ID_REFRESH, L"刷新", LEFT_W - 72, 295, 52, 22, h);

    g_chknext = mkchk(ID_AUTONEXT, L"归集成功后自动切到下一个", 8, 336, LEFT_W - 16, 20, h);
    g_chkclip = mkchk(ID_AUTOCLIP, L"切到下一个时自动把文件放进剪贴板并在资源管理器选中",
                      8, 359, LEFT_W - 16, 20, h);

    mkbtn(ID_START, L"开始", 8, 385, 76, 28, h);
    mkbtn(ID_NEXT, L"下一个 →", 90, 385, 76, 28, h);
    mkbtn(ID_SKIP, L"跳过当前", 172, 385, 76, 28, h);
    mkbtn(ID_DONE, L"标记完成", 254, 385, 76, 28, h);
    mkbtn(ID_STOP, L"■ 停止", 336, 385, 76, 28, h);
    EnableWindow(GetDlgItem(h, ID_STOP), FALSE);

    mkbtn(ID_LOCATE, L"定位当前文件", 8, 417, 130, 28, h);
    mkbtn(ID_CLIP, L"复制文件到剪贴板", 144, 417, 140, 28, h);
    mkbtn(ID_OPENOUT, L"打开输出文件夹", 290, 417, 122, 28, h);

    CreateWindowW(L"BUTTON", L"▸ 更多设置（命名模板 / 兼容模式）",
        WS_CHILD | WS_VISIBLE | BS_AUTOCHECKBOX | BS_PUSHLIKE,
        8, 449, LEFT_W - 16, 22, h, (HMENU)(INT_PTR)ID_MORE, NULL, NULL);
    /* 这些控件必须直接挂在主窗口下，否则 WM_COMMAND 发不到 WndProc */
    g_lbltpl = mklabel(L"输出命名模板", 12, 475, 78, 20, h, 1213);
    g_edtpl = mkedit(ID_EDTPL, L"{stem}_已处理", 92, 475,
                     LEFT_W - 16 - 92 - 8, 22, h, 0);
    g_chkpoll = mkchk(ID_CHKPOLL, L"兼容模式：轮询系统下载文件夹自动归集",
                      12, 501, LEFT_W - 24, 20, h);
    g_btnsave = mkbtn(ID_SAVECFG, L"保存设置", 12, 525, 90, 22, h);
    ShowWindow(g_lbltpl, SW_HIDE);
    ShowWindow(g_edtpl, SW_HIDE);
    ShowWindow(g_chkpoll, SW_HIDE);
    ShowWindow(g_btnsave, SW_HIDE);

    g_log = CreateWindowW(L"EDIT", L"",
        WS_CHILD | WS_VISIBLE | WS_BORDER | WS_VSCROLL | ES_MULTILINE |
        ES_READONLY | ES_AUTOVSCROLL,
        8, 553, LEFT_W - 16, 110, h, (HMENU)(INT_PTR)ID_LOG, NULL, NULL);
    mklabel(L"网页内「粘贴/拖入 → 点去水印或去手写 → 点下载 PDF」由你本人完成。",
            8, 667, LEFT_W - 16, 18, h, 1221);
    mklabel(L"软件不保存账号、不读 Cookie、不注入网页、不模拟点击。仅处理有权限改的资料。",
            8, 685, LEFT_W - 16, 18, h, 1222);
    g_crc = mklabel(L"CRC32：计算中…", 8, 703, LEFT_W - 16, 18, h, ID_CRC);

    /* 右侧 */
    int RX = LEFT_W + 6;
    mklabel(L"夸克扫描王 · 批量文件入口（稳定版）", RX, 8, 320, 22, h, 1231);
    g_lstate = mklabel(L"● 未登录", RX + 330, 8, 200, 22, h, ID_LSTATE);
    mkbtn(ID_LOGIN, L"登录 / 打开夸克扫描王", RX + 560, 6, 170, 28, h);
    mkbtn(ID_EXT, L"用系统浏览器打开", RX + 560, 38, 170, 24, h);
    g_addr = mkedit(ID_ADDR, DEFAULTPAGE, RX, 40, 550, 22, h, 1);

    g_rightbox = CreateWindowW(L"BUTTON", L"网页操作区",
        WS_CHILD | WS_VISIBLE | BS_GROUPBOX,
        RX, 70, 730, 560, h, (HMENU)(INT_PTR)ID_RIGHTBOX, NULL, NULL);
    g_righttxt = mklabel(
        L"\r\n\r\n"
        L"        ① 点右上「登录 / 打开夸克扫描王」\r\n"
        L"           浏览器里用手机号验证码 / 夸克 App 扫码登录你的会员账号（一次即可）\r\n\r\n"
        L"        ② 导入 PDF → 选输出文件夹 → 点「开始」\r\n"
        L"           软件已把第 1 个文件放进剪贴板，并在资源管理器里选中\r\n\r\n"
        L"        ③ 在网页里 Ctrl+V 粘贴 → 点「去水印 / 去手写」→ 点「下载 PDF」\r\n\r\n"
        L"        ④ 下载一落地，软件自动归集到输出文件夹并改名，\r\n"
        L"           然后自动切到下一个、备好下一个文件\r\n\r\n"
        L"        ────────────────────────────────────────────\r\n"
        L"        登录态留在系统浏览器里，扫码一次即可；软件不读取任何账号信息。\r\n"
        L"        若网页不接 Ctrl+V 粘贴，点上传按钮手动选，或用「定位当前文件」。",
        RX + 20, 90, 690, 520, h, ID_RIGHTTXT);
    mklabel(L"↑ 稳定版不接管网页、不猜按钮文字，官网怎么改版都不影响；exe 只有几十 KB。\r\n"
            L"网页内的「去水印 / 去手写 / 下载」由你本人点；软件只负责队列入口与成品出口。",
            RX, 636, 730, 56, h, 1232);

    /* 字体 / 勾选 / 配置 */
    g_font = CreateFontW(-14, 0, 0, 0, FW_NORMAL, 0, 0, 0, DEFAULT_CHARSET,
        OUT_DEFAULT_PRECIS, CLIP_DEFAULT_PRECIS, CLEARTYPE_QUALITY,
        DEFAULT_PITCH | FF_DONTCARE, L"Microsoft YaHei");
    g_fontb = CreateFontW(-14, 0, 0, 0, FW_BOLD, 0, 0, 0, DEFAULT_CHARSET,
        OUT_DEFAULT_PRECIS, CLIP_DEFAULT_PRECIS, CLEARTYPE_QUALITY,
        DEFAULT_PITCH | FF_DONTCARE, L"Microsoft YaHei");
    EnumChildWindows(h, setfont_cb, (LPARAM)g_font);
    SendMessageW(GetDlgItem(h, ID_LOGIN), WM_SETFONT, (WPARAM)g_fontb, TRUE);
    cfg_load();
    SetWindowTextW(g_edout, g_out);
    SetWindowTextW(g_edurl, g_url);
    SetWindowTextW(g_edtpl, g_tpl);
    SetWindowTextW(g_addr, g_url);
    SendMessageW(g_chknext, BM_SETCHECK, g_autonext ? BST_CHECKED : BST_UNCHECKED, 0);
    SendMessageW(g_chkclip, BM_SETCHECK, g_autoclip ? BST_CHECKED : BST_UNCHECKED, 0);
    SendMessageW(g_chkpoll, BM_SETCHECK, g_poll ? BST_CHECKED : BST_UNCHECKED, 0);
    SetTimer(h, 1, 1200, NULL);
    DragAcceptFiles(h, TRUE);

    unsigned int crcval = crc32_file(g_self);
    wchar_t b[256];
    _snwprintf(b, 256, L"整文件 CRC32：%08X  ·  %s", crcval, APPNAME);
    SetWindowTextW(g_crc, b);

    RECT rc; GetClientRect(h, &rc);
    layout(rc.right, rc.bottom);
}

int WINAPI WinMain(HINSTANCE hi, HINSTANCE hp, LPSTR cmd, int show) {
    (void)hp; (void)cmd;
    OleInitialize(NULL);
    GetModuleFileNameW(NULL, g_self, MAX_PATH);
    wchar_t *cl = GetCommandLineW();
    if (cl && (wcsstr(cl, L"/verify") || wcsstr(cl, L"--verify"))) {
        unsigned int c = crc32_file(g_self);
        WIN32_FILE_ATTRIBUTE_DATA d;
        unsigned long long sz = 0;
        if (GetFileAttributesExW(g_self, GetFileExInfoStandard, &d)) {
            ULARGE_INTEGER u; u.LowPart = d.nFileSizeLow; u.HighPart = d.nFileSizeHigh;
            sz = u.QuadPart;
        }
        wchar_t b[1024];
        _snwprintf(b, 1024,
            L"QuarkBatch 稳定版 完整性校验\r\n\r\n"
            L"整文件 CRC32 : %08X\r\n"
            L"文件大小     : %llu 字节\r\n"
            L"版本         : %s\r\n\r\n"
            L"原生 Win32 单文件，无外部运行时依赖。\r\n"
            L"稳定版：不接管网页，仅队列 / 备料 / 归集 / 切下一个。", c, sz, APPNAME);
        MessageBoxW(NULL, b, L"QuarkBatch · CRC 校验", MB_ICONINFORMATION | MB_OK);
        return 0;
    }

    WNDCLASSW wc; memset(&wc, 0, sizeof(wc));
    wc.lpfnWndProc = WndProc;
    wc.hInstance = hi;
    wc.hCursor = LoadCursorW(NULL, (LPCWSTR)IDC_ARROW);
    wc.hbrBackground = (HBRUSH)(COLOR_BTNFACE + 1);
    wc.lpszClassName = L"QuarkBatchLiteWnd";
    if (!RegisterClassW(&wc)) {
        MessageBoxW(NULL, L"窗口注册失败", L"QuarkBatch", MB_ICONERROR);
        return 1;
    }
    HWND h = CreateWindowW(L"QuarkBatchLiteWnd",
        L"夸克扫描王 · 批量文件入口（稳定版）  —  QuarkBatch v2.1",
        WS_OVERLAPPEDWINDOW, CW_USEDEFAULT, CW_USEDEFAULT, 1180, 760,
        NULL, NULL, hi, NULL);
    if (!h) return 1;
    build_ui(h);
    ShowWindow(h, show);
    UpdateWindow(h);

    logline(L"【稳定版 v2.1】这一版不接管网页、不猜按钮文字、不需要调试端口 —— 确定能跑。");
    logline(L"① 点「登录 / 打开夸克扫描王」→ 在浏览器里扫码登录你的会员（一次即可）");
    logline(L"② 导入 PDF → 选输出文件夹 → 点「开始」");
    logline(L"③ 软件把当前文件放进剪贴板并在资源管理器选中；网页里 Ctrl+V → 点去水印/去手写 → 点下载 PDF");
    logline(L"④ 下载一落地，软件自动归集到输出文件夹并改名，自动切到下一个、备好下一个文件");
    logline(L"※ 网页里那三下由你本人点；队列、备料、归集改名、切下一个全部自动，不受官网改版影响。");
    logline(L"※ 若网页不接 Ctrl+V，点上传按钮手动选，或用「定位当前文件」快速找到。");

    MSG msg;
    while (GetMessageW(&msg, NULL, 0, 0) > 0) {
        TranslateMessage(&msg);
        DispatchMessageW(&msg);
    }
    OleUninitialize();
    return 0;
}
