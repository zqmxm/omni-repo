/*
 * QuarkBatch Auto v3.0 —— 夸克扫描王 · 登录后全自动批量处理（原生 Win32 版）
 *
 * 用户诉求：「登入后就不用去浏览器操作了」——登录那一下仍由本人扫码，
 * 登录完成后全程由软件接管，自动塞文件、自动点去水印/去手写、自动点下载、
 * 自动归集改名、自动切下一个，用户不再切回浏览器。
 *
 * 实现方式（86KB 级体积里做全自动）：
 *   · 不打包 Chromium / Python / Qt（否则 135MB，会被 8.5MB 通道截断）。
 *   · 用系统 Edge / Chrome 带 --remote-debugging-port + 独立 profile 启动，
 *     用户在那个窗口里扫码登录一次；登录态持久化在 profile，重开软件不用再登。
 *   · 之后软件用 CDP（原生 WinSock2 手写 WebSocket + Runtime.evaluate /
 *     DOM.setFileInputFiles）接管「用户自己那个已登录的页面」，代为执行
 *     上传、点功能按钮、点下载。等价于本人手动点，不伪造接口、不绕过会员。
 *   · 下载成品仍靠轮询下载目录归集到输出文件夹并改名。
 *
 * 与之前版本的边界差异（必须如实说明）：
 *   之前写的是「不注入 JS、不模拟点击」；本版为了全自动，会在你已登录的
 *   页面上注入点击脚本并代为点击。它依赖夸克网页当前按钮文字，官网改版后
 *   用界面上的「测试当前页面」把实际文字填进「更多设置」即可，不用改程序。
 *   不抓包、不伪造请求、不读取/保存账号密码或 Cookie、不绕过会员校验。
 *   自动化操作网页存在账号风控风险，请适度限速、勿高并发。
 *
 * 若 CDP 接管失败（端口被占/浏览器不配合），自动降级为上一版半自动模式：
 * 软件备好当前文件，网页里那几下由你点，归集与切下一个仍然自动。
 *
 * 依赖：Win10/11 自带 user32/gdi32/comctl32/shell32/ole32/comdlg32/ws2_32 + UCRT
 * 编译：zig cc -target x86_64-windows-gnu -Os -Wl,--subsystem,windows -lws2_32
 */

#include <winsock2.h>     /* 必须在 windows.h 之前，避免拉入旧 winsock.h */
#include <ws2tcpip.h>
#include <windows.h>
#include <commctrl.h>
#include <commdlg.h>
#include <shellapi.h>
#include <shlobj.h>
#include <stdio.h>
#include <stdarg.h>
#include <wchar.h>
#include <string.h>
#include <process.h>

#include "qb_cdp.h"

#define APPNAME       L"QuarkBatch Auto v3.0"
#define DEFAULTPAGE   L"https://scan.quark.cn/"
#define MAXJOB        2048
#define MAXSEEN       4096
#define LEFT_W        430
#define WM_APP_LOG      (WM_APP + 1)
#define WM_APP_REFRESH  (WM_APP + 2)
#define WM_APP_AUTOST   (WM_APP + 3)

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
#define ID_CONFIRM    1094   /* 我已登录完成 */
#define ID_PROBE      1095   /* 测试当前页面 / 校准 */
#define ID_CMBMODE    1096   /* 处理模式 */
#define ID_LBLAUTO    1097   /* 接管状态 */
#define ID_CHKAUTO    1098   /* 是否全自动 */
#define ID_EDHW       1101   /* 去手写 按钮文字 */
#define ID_EDWM       1102   /* 去水印 按钮文字 */
#define ID_EDEXP      1103   /* 导出 按钮文字 */
#define ID_EDBUSY     1104   /* 处理中 提示文字 */
#define ID_EDPORT     1105   /* 调试端口 */

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
static wchar_t g_workurl[1024] = L"";      /* 登录后记下的工作页地址 */
static wchar_t g_tpl[256] = L"{stem}_已处理";
static wchar_t g_ini[MAX_PATH] = L"";
static wchar_t g_self[MAX_PATH] = L"";
static wchar_t g_profile[MAX_PATH] = L"";
static wchar_t g_btn_hw[128] = L"去手写";
static wchar_t g_btn_wm[128] = L"去水印";
static wchar_t g_btn_exp[128] = L"导出PDF";
static wchar_t g_btn_busy[128] = L"处理中";
static int     g_autonext = 1, g_autoclip = 1, g_poll = 1;
static int     g_automode = 1;             /* 1=全自动 0=半自动 */
static int     g_port = CDP_PORT;
static int     g_mode = 0;                 /* 0仅去手写 1仅去水印 2先去手写再去水印 */
static volatile int g_cdp_on = 0;         /* 已接管已登录页面 */
static volatile int g_auto_running = 0;   /* 自动线程在跑 */
static volatile int g_stop = 0;

static wchar_t g_seen[MAXSEEN][MAX_PATH];
static int     g_nseen = 0;
static ULONGLONG g_startFT = 0;

static HWND g_hwnd, g_list, g_log, g_edout, g_edurl, g_lstate, g_addr;
static HWND g_rightbox, g_righttxt, g_crc, g_edtpl;
static HWND g_chknext, g_chkclip, g_chkpoll;
static HFONT g_font, g_fontb;
static HWND g_lbltpl, g_btnsave;
static HWND g_cmbmode, g_lblauto, g_chkauto;
static HWND g_edhw, g_edwm, g_edexp, g_edbusy, g_edport;
static HWND g_lblhw, g_lblwm, g_lblexp, g_lblbusy, g_lblport, g_lblmode;

static void snapshot_downloads(void);
static void next_job(void);
static const wchar_t *basename_tmp(const wchar_t *p);
static void post_log(const wchar_t *s);
static void watch_dirs(wchar_t d1[MAX_PATH], wchar_t d2[MAX_PATH]);
static void target_name(Job *j, wchar_t *out, size_t cap);
static void cfg_save(void);

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
    GetPrivateProfileStringW(L"QB", L"work_url", L"", g_workurl, 1024, g_ini);
    GetPrivateProfileStringW(L"QB", L"tpl", L"{stem}_已处理", g_tpl, 256, g_ini);
    GetPrivateProfileStringW(L"QB", L"btn_handwrite", L"去手写", g_btn_hw, 128, g_ini);
    GetPrivateProfileStringW(L"QB", L"btn_watermark", L"去水印", g_btn_wm, 128, g_ini);
    GetPrivateProfileStringW(L"QB", L"btn_export", L"导出PDF", g_btn_exp, 128, g_ini);
    GetPrivateProfileStringW(L"QB", L"btn_busy", L"处理中", g_btn_busy, 128, g_ini);
    g_autonext = GetPrivateProfileIntW(L"QB", L"auto_next", 1, g_ini);
    g_autoclip = GetPrivateProfileIntW(L"QB", L"auto_clip", 1, g_ini);
    g_poll     = GetPrivateProfileIntW(L"QB", L"poll", 1, g_ini);
    g_automode = GetPrivateProfileIntW(L"QB", L"automode", 1, g_ini);
    g_port     = GetPrivateProfileIntW(L"QB", L"port", CDP_PORT, g_ini);
    g_mode     = GetPrivateProfileIntW(L"QB", L"mode", 0, g_ini);
    if (g_port < 1024 || g_port > 65535) g_port = CDP_PORT;
    if (g_mode < 0 || g_mode > 2) g_mode = 0;
    /* 专用浏览器 profile，登录态持久化在这里 */
    {
        wchar_t base[MAX_PATH];
        if (GetEnvironmentVariableW(L"APPDATA", base, MAX_PATH) == 0)
            GetEnvironmentVariableW(L"USERPROFILE", base, MAX_PATH);
        _snwprintf(g_profile, MAX_PATH, L"%s\\QuarkBatch\\browser_profile", base);
        CreateDirectoryW(g_profile, NULL);
    }
}
static void cfg_save(void) {
    ini_path();
    /* cfg_save 可能在 build_ui 之前/线程里被调用，所有取控件值都要判空 */
    if (g_edout)  GetWindowTextW(g_edout, g_out, MAX_PATH);
    if (g_edurl) { GetWindowTextW(g_edurl, g_url, 1024);
                   if (!g_url[0]) wcscpy(g_url, DEFAULTPAGE); }
    if (g_edtpl) { GetWindowTextW(g_edtpl, g_tpl, 256);
                   if (!g_tpl[0]) wcscpy(g_tpl, L"{stem}_已处理"); }
    if (g_edhw)   GetWindowTextW(g_edhw, g_btn_hw, 128);
    if (g_edwm)   GetWindowTextW(g_edwm, g_btn_wm, 128);
    if (g_edexp)  GetWindowTextW(g_edexp, g_btn_exp, 128);
    if (g_edbusy) GetWindowTextW(g_edbusy, g_btn_busy, 128);
    if (g_edport) {
        wchar_t p[32]; GetWindowTextW(g_edport, p, 32);
        int v = _wtoi(p);
        if (v >= 1024 && v <= 65535) g_port = v;
    }
    if (g_chknext) g_autonext = (SendMessageW(g_chknext, BM_GETCHECK, 0, 0) == BST_CHECKED);
    if (g_chkclip) g_autoclip = (SendMessageW(g_chkclip, BM_GETCHECK, 0, 0) == BST_CHECKED);
    if (g_chkpoll) g_poll     = (SendMessageW(g_chkpoll, BM_GETCHECK, 0, 0) == BST_CHECKED);
    if (g_chkauto) g_automode = (SendMessageW(g_chkauto, BM_GETCHECK, 0, 0) == BST_CHECKED);
    if (g_cmbmode) { int i = (int)SendMessageW(g_cmbmode, CB_GETCURSEL, 0, 0);
                     if (i >= 0 && i <= 2) g_mode = i; }
    WritePrivateProfileStringW(L"QB", L"out_dir", g_out, g_ini);
    WritePrivateProfileStringW(L"QB", L"web_url", g_url, g_ini);
    WritePrivateProfileStringW(L"QB", L"work_url", g_workurl, g_ini);
    WritePrivateProfileStringW(L"QB", L"tpl", g_tpl, g_ini);
    WritePrivateProfileStringW(L"QB", L"btn_handwrite", g_btn_hw, g_ini);
    WritePrivateProfileStringW(L"QB", L"btn_watermark", g_btn_wm, g_ini);
    WritePrivateProfileStringW(L"QB", L"btn_export", g_btn_exp, g_ini);
    WritePrivateProfileStringW(L"QB", L"btn_busy", g_btn_busy, g_ini);
    wchar_t n[16];
    _snwprintf(n, 16, L"%d", g_autonext); WritePrivateProfileStringW(L"QB", L"auto_next", n, g_ini);
    _snwprintf(n, 16, L"%d", g_autoclip); WritePrivateProfileStringW(L"QB", L"auto_clip", n, g_ini);
    _snwprintf(n, 16, L"%d", g_poll);     WritePrivateProfileStringW(L"QB", L"poll", n, g_ini);
    _snwprintf(n, 16, L"%d", g_automode); WritePrivateProfileStringW(L"QB", L"automode", n, g_ini);
    _snwprintf(n, 16, L"%d", g_port);     WritePrivateProfileStringW(L"QB", L"port", n, g_ini);
    _snwprintf(n, 16, L"%d", g_mode);     WritePrivateProfileStringW(L"QB", L"mode", n, g_ini);
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
static void u8_to_w(const char *s, wchar_t *out, size_t cap) {
    MultiByteToWideChar(CP_UTF8, 0, s, -1, out, (int)cap);
    out[cap - 1] = 0;
}
static void post_log(const wchar_t *s) {
    if (!g_hwnd) return;
    wchar_t *p = _wcsdup(s);
    if (p) PostMessageW(g_hwnd, WM_APP_LOG, 0, (LPARAM)p);
}
static void set_auto_label(const wchar_t *s) {
    if (g_lblauto) SetWindowTextW(g_lblauto, s);
}

static int port_alive(int port) {
    char *r = cdp_http_get(port, "/json/version");
    if (!r) return 0;
    int ok = (strstr(r, "\"Browser\"") != NULL ||
              strstr(r, "webSocketDebuggerUrl") != NULL);
    free(r);
    return ok;
}

/* 启动带 CDP 调试端口的浏览器窗口（专用 profile，登录态持久化） */
static void launch_debug_browser(int login) {
    if (g_edurl) { GetWindowTextW(g_edurl, g_url, 1024);
                   if (!g_url[0]) wcscpy(g_url, DEFAULTPAGE); }
    if (port_alive(g_port)) {
        logf(L"调试端口 %d 已有浏览器窗口，直接复用（不另开）", g_port);
        if (login) {
            SetWindowTextW(g_lstate, L"● 窗口已就绪，请在里面登录");
            logline(L"如已登录过，点「我已登录完成」让软件接管。");
        }
        return;
    }
    wchar_t exe[MAX_PATH];
    if (!find_browser(exe, MAX_PATH)) {
        logline(L"❌ 没找到 Edge / Chrome。请安装 Edge（Win10/11 自带）后重试。");
        set_auto_label(L"接管失败：未找到浏览器（半自动）");
        return;
    }
    wchar_t cmd[8192];
    _snwprintf(cmd, 8192,
        L"\"%s\" --remote-debugging-port=%d --user-data-dir=\"%s\" "
        L"--remote-allow-origins=* --no-first-run --no-default-browser-check "
        L"--disable-background-networking --window-size=1180,860 "
        L"--app=\"%s\"",
        exe, g_port, g_profile, g_url);
    STARTUPINFOW si; PROCESS_INFORMATION pi;
    memset(&si, 0, sizeof(si)); si.cb = sizeof(si);
    memset(&pi, 0, sizeof(pi));
    if (!CreateProcessW(NULL, cmd, NULL, NULL, FALSE, 0, NULL, NULL, &si, &pi)) {
        logline(L"❌ 启动浏览器失败，改用半自动：网页里那几下由你点。");
        set_auto_label(L"接管失败：浏览器未启动（半自动）");
        return;
    }
    CloseHandle(pi.hThread); CloseHandle(pi.hProcess);
    for (int i = 0; i < 40; i++) {
        if (port_alive(g_port)) break;
        Sleep(250);
    }
    SetWindowTextW(g_addr, g_url);
    if (login) {
        SetWindowTextW(g_lstate, L"● 已打开，请在窗口里扫码登录");
        logline(L"已打开夸克扫描王（软件专用窗口，带调试端口）。");
        logline(L"👉 用「手机号验证码」或「夸克 App 扫码」登录你的会员账号，只需这一次。");
        logline(L"   登录态存于专用 profile，重开软件不用再登；软件不读取密码/验证码/Cookie。");
        logline(L"   登录完成后回到本软件点「我已登录完成」。");
    } else {
        logf(L"已打开页面：%s", g_url);
    }
}

/* 连到已登录页面的 WebSocket */
static SOCKET cdp_attach(int *id) {
    char url[1024] = "";
    char *ws = cdp_find_page_ws(g_port, url, sizeof(url));
    if (!ws) return INVALID_SOCKET;
    SOCKET s = cdp_ws_connect(g_port, ws);
    if (s != INVALID_SOCKET && url[0]) {
        wchar_t w[1024]; u8_to_w(url, w, 1024);
        /* 记录当前页面为工作页，自动批量时每轮回到这里 */
        if (wcsstr(w, L"quark.cn")) {
            wcsncpy_s_(g_workurl, 1024, w);
            SetWindowTextW(g_addr, w);
        }
    }
    free(ws);
    *id = 0;
    return s;
}

static void cdp_close(SOCKET s) {
    if (s != INVALID_SOCKET) closesocket(s);
}

/* 「我已登录完成」→ 接管并探测 */
static void do_confirm(void) {
    if (!port_alive(g_port)) {
        g_cdp_on = 0;
        logline(L"⚠ 调试端口没响应。请先点「登录 / 打开夸克扫描王」启动专用窗口。");
        set_auto_label(L"未接管（半自动）：请先点登录");
        return;
    }
    int id = 0;
    SOCKET s = cdp_attach(&id);
    if (s == INVALID_SOCKET) {
        g_cdp_on = 0;
        logline(L"⚠ WebSocket 握手失败，自动降级为半自动模式（归集/切下一个仍自动）。");
        set_auto_label(L"接管失败（半自动）");
        return;
    }
    char v[512];
    int ok = cdp_eval(s, &id,
        "(function(){var t=(document.body&&document.body.innerText)||'';"
        "return /登录|扫码|验证码/.test(t)?'NEED_LOGIN':'LOGGED'})()", v, sizeof(v));
    cdp_close(s);
    if (ok && strcmp(v, "LOGGED") == 0) {
        g_cdp_on = 1;
        SetWindowTextW(g_lstate, L"● 已登录 · 软件已接管");
        set_auto_label(L"● 全自动：已接管已登录页面");
        logline(L"✔ 已确认登录并接管。现在导入 PDF、选输出文件夹、点「开始」即可，不用再碰浏览器。");
        logf(L"   工作页已记录：%s", g_workurl[0] ? g_workurl : g_url);
        logline(L"   ⚠ 请确认浏览器当前停在「文件工具中心 / 去除手写 / 去除水印」那一页：");
        logline(L"     软件会把这一页记为工作页，自动批量每轮都回到它；若停在官网首页会找不到上传框。");
        logline(L"   若自动点击找不到按钮，点「测试当前页面」把实际文字填进「更多设置」。");
        cfg_save();
    } else {
        g_cdp_on = 0;
        SetWindowTextW(g_lstate, L"● 页面仍有登录提示");
        set_auto_label(L"未接管：请先扫码登录");
        logline(L"⚠ 页面上还能看到「登录/扫码/验证码」，请先在浏览器窗口里登录完成。");
        logline(L"   如果其实已登录，忽略本条，直接点「开始」试一次；不行再点「测试当前页面」。");
    }
}

/* 「测试当前页面 / 校准」：列出真实按钮文字与文件输入框 */
static void do_probe(void) {
    if (!port_alive(g_port)) {
        logline(L"⚠ 调试端口没响应，请先点「登录 / 打开夸克扫描王」。");
        return;
    }
    int id = 0;
    SOCKET s = cdp_attach(&id);
    if (s == INVALID_SOCKET) { logline(L"⚠ 连不上网页，检查浏览器窗口是否还开着。"); return; }
    char *j = cdp_probe(s, &id);
    cdp_close(s);
    if (!j) { logline(L"⚠ 探测失败（页面可能还在加载），稍后重试。"); return; }

    wchar_t w[32768]; u8_to_w(j, w, 32768);
    logline(L"——— 当前页面探测结果（照着改「更多设置」里的按钮文字）———");
    /* JSON 里的字符串被 cdp_json_str 解过一层，这里按分隔符粗拆后逐段显示 */
    wchar_t *p = w;
    while (*p) {
        wchar_t line[512]; size_t n = 0;
        while (*p && *p != L',' && n < 500) line[n++] = *p++;
        line[n] = 0;
        if (*p == L',') p++;
        if (n > 2) logline(line);
    }
    logline(L"——— 重点看 buttons 里的实际文字，以及 fileInputs 是否非空 ———");
    free(j);
}

/* ---------------- 全自动批量线程 ---------------- */
static int cdp_navigate(SOCKET s, int *id, const wchar_t *url) {
    char u8[2048];
    WideCharToMultiByte(CP_UTF8, 0, url, -1, u8, sizeof(u8), NULL, NULL);
    char *e = cdp_json_escape(u8);
    if (!e) return 0;
    size_t pn = strlen(e) + 64;
    char *params = (char *)malloc(pn);
    if (!params) { free(e); return 0; }
    _snprintf(params, pn, "{\"url\":\"%s\"}", e);
    char *r = cdp_call(s, id, "Page.navigate", params);
    free(params); free(e);
    int ok = (r != NULL && strstr(r, "\"error\"") == NULL);
    free(r);
    return ok;
}

static int cdp_wait_ready(SOCKET s, int *id, int timeoutSec) {
    char exp8[256], busy8[256];
    WideCharToMultiByte(CP_UTF8, 0, g_btn_exp, -1, exp8, sizeof(exp8), NULL, NULL);
    WideCharToMultiByte(CP_UTF8, 0, g_btn_busy, -1, busy8, sizeof(busy8), NULL, NULL);
    char *e1 = cdp_json_escape(exp8), *e2 = cdp_json_escape(busy8);
    if (!e1 || !e2) { free(e1); free(e2); return 0; }
    char js[1024];
    _snprintf(js, sizeof(js),
        "(function(){var t=(document.body&&document.body.innerText)||'';"
        "return (t.indexOf(\"%s\")>=0||t.indexOf(\"下载\")>=0||t.indexOf(\"导出\")>=0)"
        "&& (t.indexOf(\"%s\")<0)?'READY':'WAIT'})()", e1, e2);
    free(e1); free(e2);
    char out[64];
    for (int i = 0; i < timeoutSec; i++) {
        if (g_stop) return 0;
        if (cdp_eval(s, id, js, out, sizeof(out)) && strcmp(out, "READY") == 0)
            return 1;
        Sleep(1000);
    }
    return 0;
}

/* 线程内归集：找新下载 → 复制到输出目录并改名 → 返回成品名 */
static int thread_collect(wchar_t *outName, size_t cap) {
    wchar_t d1[MAX_PATH], d2[MAX_PATH];
    watch_dirs(d1, d2);
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
    if (!bestPath[0]) return 0;
    wchar_t dst[MAX_PATH];
    target_name(&g_jobs[g_cur], dst, MAX_PATH);
    if (_wcsicmp(bestPath, dst) != 0) {
        if (!CopyFileW(bestPath, dst, FALSE)) return 0;
        DeleteFileW(bestPath);
    }
    seen_add(bestName);
    wchar_t bn[MAX_PATH]; basename_w(dst, bn, MAX_PATH);
    seen_add(bn);                 /* 防止成品被下一轮当作新下载 */
    wcsncpy_s_(outName, cap, bn);
    return 1;
}

static unsigned __stdcall auto_batch_thread(void *arg) {
    (void)arg;
    int id = 0;
    SOCKET s = cdp_attach(&id);
    if (s == INVALID_SOCKET) {
        post_log(L"❌ 无法接管网页，已降级：请点「登录」重启专用窗口，或改用半自动「下一个 →」。");
        PostMessageW(g_hwnd, WM_APP_AUTOST, 0, 0);
        return 0;
    }
    wchar_t msg[512];
    const wchar_t *modeName = g_mode == 0 ? L"仅去手写" :
                              g_mode == 1 ? L"仅去水印" : L"先去手写再去水印";
    _snwprintf(msg, 512, L"开始全自动：%d 个 PDF ｜ %s ｜ 端口 %d",
               g_njobs, modeName, g_port);
    post_log(msg);

    for (int i = 0; i < g_njobs; i++) {
        if (g_stop) break;
        if (wcscmp(g_jobs[i].status, L"待处理") != 0) continue;
        g_cur = i;
        wcscpy(g_jobs[i].status, L"处理中");
        g_jobs[i].outname[0] = 0;
        PostMessageW(g_hwnd, WM_APP_REFRESH, 0, 0);
        _snwprintf(msg, 512, L"[%d/%d] %s", i + 1, g_njobs, g_jobs[i].name);
        post_log(msg);

        /* 每轮回工作页，保证状态干净 */
        const wchar_t *work = g_workurl[0] ? g_workurl : g_url;
        cdp_navigate(s, &id, work);
        Sleep(2200);

        /* 塞文件 */
        char oid[256];
        if (!cdp_input_object(s, &id, oid, sizeof(oid))) {
            wcscpy(g_jobs[i].status, L"✘ 失败");
            wcsncpy_s_(g_jobs[i].note, 128, L"页面上找不到文件输入框，点「测试当前页面」");
            post_log(L"   ✘ 找不到 input[type=file]，用「测试当前页面」看 fileInputs；本文件跳过");
            PostMessageW(g_hwnd, WM_APP_REFRESH, 0, 0);
            continue;
        }
        if (!cdp_set_files(s, &id, oid, g_jobs[i].path)) {
            wcscpy(g_jobs[i].status, L"✘ 失败");
            wcsncpy_s_(g_jobs[i].note, 128, L"上传失败");
            post_log(L"   ✘ 塞文件失败，跳过");
            PostMessageW(g_hwnd, WM_APP_REFRESH, 0, 0);
            continue;
        }
        post_log(L"   已塞入文件输入框，等待上传…");
        Sleep(2500);

        /* 点功能按钮 */
        const wchar_t *btns[2]; int nb = 0;
        if (g_mode == 0) { btns[nb++] = g_btn_hw; }
        else if (g_mode == 1) { btns[nb++] = g_btn_wm; }
        else { btns[nb++] = g_btn_hw; btns[nb++] = g_btn_wm; }
        int clicked = 1;
        for (int b = 0; b < nb; b++) {
            char r[64] = "";
            if (!cdp_click_text(s, &id, btns[b], r, sizeof(r)) || strcmp(r, "OK") != 0) {
                _snwprintf(msg, 512,
                    L"   ✘ 页面上找不到「%s」按钮，请点「测试当前页面」把实际文字填进「更多设置」",
                    btns[b]);
                post_log(msg);
                clicked = 0; break;
            }
            _snwprintf(msg, 512, L"   已点「%s」", btns[b]);
            post_log(msg);
            Sleep(2200);
        }
        if (!clicked) {
            wcscpy(g_jobs[i].status, L"✘ 失败");
            wcsncpy_s_(g_jobs[i].note, 128, L"按钮文字不匹配，需校准");
            PostMessageW(g_hwnd, WM_APP_REFRESH, 0, 0);
            continue;
        }

        /* 等处理完 */
        if (!cdp_wait_ready(s, &id, 90)) {
            post_log(L"   ⚠ 等待处理完成超时，仍尝试点导出");
        }
        char r[64] = "";
        int expOk = 0;
        if (cdp_click_text(s, &id, g_btn_exp, r, sizeof(r)) && strcmp(r, "OK") == 0)
            expOk = 1;
        if (!expOk) {
            const wchar_t *alts[] = { L"下载PDF", L"下载 PDF", L"导出 PDF", L"下载", L"导出" };
            for (int k = 0; k < 5 && !expOk; k++) {
                r[0] = 0;
                if (cdp_click_text(s, &id, alts[k], r, sizeof(r)) && strcmp(r, "OK") == 0)
                    expOk = 1;
            }
        }
        if (!expOk) {
            post_log(L"   ✘ 没找到导出/下载按钮，请用「测试当前页面」校准，本文件跳过");
            wcscpy(g_jobs[i].status, L"✘ 失败");
            wcsncpy_s_(g_jobs[i].note, 128, L"导出按钮未命中");
            PostMessageW(g_hwnd, WM_APP_REFRESH, 0, 0);
            continue;
        }
        _snwprintf(msg, 512, L"   已点「%s」，等待下载落盘…", g_btn_exp);
        post_log(msg);

        /* 等下载并归集 */
        wchar_t outName[MAX_PATH] = L"";
        int got = 0;
        for (int t = 0; t < 90 && !g_stop; t++) {
            if (thread_collect(outName, MAX_PATH)) { got = 1; break; }
            Sleep(1000);
        }
        if (got) {
            wcscpy(g_jobs[i].status, L"已完成");
            wcsncpy_s_(g_jobs[i].outname, MAX_PATH, outName);
            wcsncpy_s_(g_jobs[i].note, 128, L"网页自动处理并下载归集");
            _snwprintf(msg, 512, L"   → %s", outName);
            post_log(msg);
        } else {
            wcscpy(g_jobs[i].status, L"✘ 失败");
            wcsncpy_s_(g_jobs[i].note, 128, L"没等到下载，可点「标记完成」或检查网页");
            post_log(L"   ✘ 没等到下载完成的 PDF，跳过");
        }
        PostMessageW(g_hwnd, WM_APP_REFRESH, 0, 0);
    }
    cdp_close(s);
    post_log(g_stop ? L"⏹ 已停止" : L"✔ 全自动批量结束，点「打开输出文件夹」看成品");
    PostMessageW(g_hwnd, WM_APP_AUTOST, 0, 0);
    return 0;
}

/* ---------------- 下载快照 / 归集 ---------------- */
static void watch_dirs(wchar_t d1[MAX_PATH], wchar_t d2[MAX_PATH]) {
    /* 用全局 g_out（start_batch / pick_out_dialog 已同步），避免后台线程跨线程读控件 */
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
    g_stop = 0;
    set_running(1);

    if (g_automode && g_cdp_on) {
        logline(L"—— 开始全自动批量（登录后不再需要操作浏览器）——");
        g_auto_running = 1;
        set_auto_label(L"● 全自动运行中…");
        uintptr_t th = _beginthreadex(NULL, 0, auto_batch_thread, NULL, 0, NULL);
        if (!th) {
            g_auto_running = 0;
            logline(L"⚠ 线程启动失败，已降级为半自动：网页里那几下由你点。");
            set_auto_label(L"降级为半自动");
            next_job();
        } else {
            CloseHandle((HANDLE)th);
        }
    } else if (g_automode && !g_cdp_on) {
        logline(L"—— 半自动模式（还没接管已登录页面）——");
        logline(L"   想全自动：点「登录 / 打开夸克扫描王」→ 扫码 → 点「我已登录完成」。");
        set_auto_label(L"未接管（半自动）：先登录并点「我已登录完成」");
        next_job();
    } else {
        logline(L"—— 半自动模式（已手动关闭全自动）——");
        set_auto_label(L"半自动模式");
        next_job();
    }
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
    /* 处理模式行 */
    MoveWindow(g_lblmode, 8, y + 3, 44, 20, TRUE);
    MoveWindow(g_cmbmode, 54, y, 150, 22, TRUE);
    MoveWindow(g_chkauto, 212, y + 1, LW - 220, 20, TRUE);
    y += 26;
    /* 登录 / 校准行 */
    int hw = (LW - 16 - 2 * 6) / 3;
    MoveWindow(GetDlgItem(g_hwnd, ID_CONFIRM), 8, y, hw, 26, TRUE);
    MoveWindow(GetDlgItem(g_hwnd, ID_PROBE), 8 + (hw + 6), y, hw, 26, TRUE);
    MoveWindow(GetDlgItem(g_hwnd, ID_EXT), 8 + 2 * (hw + 6), y, hw, 26, TRUE);
    y += 30;
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
    MoveWindow(g_lblauto, 8, y, LW - 16, 18, TRUE);
    y += 20;
    MoveWindow(GetDlgItem(g_hwnd, ID_MORE), 8, y, LW - 16, 22, TRUE); y += 24;
    /* 校准面板：6 行输入 + 兼容勾选 + 保存 */
    int moreOn = IsWindowVisible(g_edtpl);
    int moreH = moreOn ? 206 : 0;
    struct { HWND l; HWND e; int dy; } rows[] = {
        { g_lbltpl,  g_edtpl,  0 },
        { g_lblhw,   g_edhw,   26 },
        { g_lblwm,   g_edwm,   52 },
        { g_lblexp,  g_edexp,  78 },
        { g_lblbusy, g_edbusy, 104 },
        { g_lblport, g_edport, 130 },
    };
    for (int i = 0; i < 6; i++) {
        MoveWindow(rows[i].l, 12, y + rows[i].dy + 3, 86, 20, TRUE);
        MoveWindow(rows[i].e, 102, y + rows[i].dy,
                   i == 5 ? 80 : LW - 16 - 102 - 8, 22, TRUE);
    }
    MoveWindow(g_chkpoll, 12, y + 156, LW - 24, 20, TRUE);
    MoveWindow(g_btnsave, 12, y + 180, 90, 22, TRUE);
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
    MoveWindow(GetDlgItem(g_hwnd, ID_LOGIN), RX + RW - 180, 6, 180, 28, TRUE);
    MoveWindow(g_addr, RX, 40, RW, 22, TRUE);
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
        /* 全自动线程在跑时，归集由线程自己做，定时器不抢状态机 */
        if (w == 1 && !g_auto_running) poll_downloads();
        return 0;
    case WM_APP_LOG: {
        wchar_t *p = (wchar_t *)l;
        if (p) { logline(p); free(p); }
        return 0;
    }
    case WM_APP_REFRESH:
        list_rebuild();
        return 0;
    case WM_APP_AUTOST:
        g_auto_running = 0;
        g_collected = 1;
        set_running(0);
        set_auto_label(g_cdp_on ? L"● 全自动：已接管（空闲）"
                                : L"未接管（半自动）");
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
        case ID_OPENPAGE: launch_debug_browser(0); break;
        case ID_REFRESH: launch_debug_browser(0); break;
        case ID_LOGIN: launch_debug_browser(1); break;
        case ID_CONFIRM: do_confirm(); break;
        case ID_PROBE: do_probe(); break;
        case ID_EXT:
            GetWindowTextW(g_edurl, g_url, 1024);
            ShellExecuteW(NULL, L"open", g_url[0] ? g_url : DEFAULTPAGE,
                          NULL, NULL, SW_SHOWNORMAL);
            logline(L"已用系统默认浏览器打开（仅排障；此窗口无调试端口，软件无法接管）");
            break;
        case ID_START: start_batch(); break;
        case ID_NEXT: if (g_running && !g_auto_running) next_job();
                      else logline(L"全自动运行中由软件自动切换；或先点「开始」"); break;
        case ID_SKIP: if (!g_auto_running) skip_current();
                      else logline(L"全自动运行中，请用「停止」后手动跳过"); break;
        case ID_DONE: if (!g_auto_running) mark_done();
                      else logline(L"全自动运行中，请用「停止」后手动标记"); break;
        case ID_STOP:
            g_stop = 1;
            set_running(0);
            logline(L"■ 已停止（当前文件处理完即停，队列与已完成文件保留）");
            break;
        case ID_CHKAUTO:
            g_automode = SendMessageW(g_chkauto, BM_GETCHECK, 0, 0) == BST_CHECKED;
            logline(g_automode ? L"已切换为全自动模式（需先「我已登录完成」）"
                               : L"已切换为半自动模式（网页里那几下由你点）");
            set_auto_label(g_automode ? (g_cdp_on ? L"● 全自动：已接管（空闲）"
                                                  : L"全自动待接管：先登录并点「我已登录完成」")
                                      : L"半自动模式");
            break;
        case ID_CMBMODE:
            if (HIWORD(w) == CBN_SELCHANGE) {
                int i = (int)SendMessageW(g_cmbmode, CB_GETCURSEL, 0, 0);
                if (i >= 0 && i <= 2) g_mode = i;
            }
            break;
        case ID_LOCATE:
            if (g_cur >= 0 && g_cur < g_njobs) select_in_explorer(g_jobs[g_cur].path);
            else logline(L"当前没有文件");
            break;
        case ID_CLIP: clip_current(); break;
        case ID_SAVECFG: cfg_save(); logline(L"✔ 设置已保存（%APPDATA%\\QuarkBatch\\config.ini）"); break;
        case ID_MORE: {
            int on = SendMessageW(GetDlgItem(h, ID_MORE), BM_GETCHECK, 0, 0) == BST_CHECKED;
            HWND items[] = { g_lbltpl, g_edtpl, g_lblhw, g_edhw, g_lblwm, g_edwm,
                             g_lblexp, g_edexp, g_lblbusy, g_edbusy,
                             g_lblport, g_edport, g_chkpoll, g_btnsave };
            for (int i = 0; i < (int)(sizeof(items) / sizeof(items[0])); i++)
                if (items[i]) ShowWindow(items[i], on ? SW_SHOW : SW_HIDE);
            SetWindowTextW(GetDlgItem(h, ID_MORE),
                on ? L"▾ 更多设置（按钮文字校准 / 兼容模式）"
                   : L"▸ 更多设置（按钮文字校准 / 兼容模式）");
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

    /* 处理模式 + 全自动开关 */
    g_lblmode = mklabel(L"处理：", 8, 385, 44, 20, h, 1241);
    g_cmbmode = CreateWindowW(WC_COMBOBOXW, L"",
        WS_CHILD | WS_VISIBLE | WS_VSCROLL | CBS_DROPDOWNLIST,
        54, 382, 150, 120, h, (HMENU)(INT_PTR)ID_CMBMODE, NULL, NULL);
    SendMessageW(g_cmbmode, CB_ADDSTRING, 0, (LPARAM)L"仅去手写");
    SendMessageW(g_cmbmode, CB_ADDSTRING, 0, (LPARAM)L"仅去水印");
    SendMessageW(g_cmbmode, CB_ADDSTRING, 0, (LPARAM)L"先去手写再去水印");
    g_chkauto = mkchk(ID_CHKAUTO, L"全自动接管（登录一次后不再碰浏览器）",
                      212, 383, LEFT_W - 220, 20, h);

    /* 登录 / 校准 / 排障 */
    mkbtn(ID_CONFIRM, L"我已登录完成", 8, 411, 130, 26, h);
    mkbtn(ID_PROBE, L"测试当前页面 / 校准", 144, 411, 140, 26, h);
    mkbtn(ID_EXT, L"用系统浏览器打开", 290, 411, 122, 26, h);

    mkbtn(ID_START, L"开始", 8, 441, 76, 28, h);
    mkbtn(ID_NEXT, L"下一个 →", 90, 441, 76, 28, h);
    mkbtn(ID_SKIP, L"跳过当前", 172, 441, 76, 28, h);
    mkbtn(ID_DONE, L"标记完成", 254, 441, 76, 28, h);
    mkbtn(ID_STOP, L"■ 停止", 336, 441, 76, 28, h);
    EnableWindow(GetDlgItem(h, ID_STOP), FALSE);

    mkbtn(ID_LOCATE, L"定位当前文件", 8, 473, 130, 28, h);
    mkbtn(ID_CLIP, L"复制文件到剪贴板", 144, 473, 140, 28, h);
    mkbtn(ID_OPENOUT, L"打开输出文件夹", 290, 473, 122, 28, h);

    g_lblauto = mklabel(L"● 全自动：待登录接管", 8, 505, LEFT_W - 16, 18, h, ID_LBLAUTO);

    CreateWindowW(L"BUTTON", L"▸ 更多设置（按钮文字校准 / 兼容模式）",
        WS_CHILD | WS_VISIBLE | BS_AUTOCHECKBOX | BS_PUSHLIKE,
        8, 527, LEFT_W - 16, 22, h, (HMENU)(INT_PTR)ID_MORE, NULL, NULL);
    /* 这些控件必须直接挂在主窗口下，否则 WM_COMMAND 发不到 WndProc */
    g_lbltpl  = mklabel(L"输出命名模板", 12, 553, 86, 20, h, 1213);
    g_edtpl   = mkedit(ID_EDTPL, L"{stem}_已处理", 102, 553,
                       LEFT_W - 16 - 102 - 8, 22, h, 0);
    g_lblhw   = mklabel(L"去手写 按钮", 12, 579, 86, 20, h, 1242);
    g_edhw    = mkedit(ID_EDHW, L"去手写", 102, 579, LEFT_W - 16 - 102 - 8, 22, h, 0);
    g_lblwm   = mklabel(L"去水印 按钮", 12, 605, 86, 20, h, 1243);
    g_edwm    = mkedit(ID_EDWM, L"去水印", 102, 605, LEFT_W - 16 - 102 - 8, 22, h, 0);
    g_lblexp  = mklabel(L"导出 按钮", 12, 631, 86, 20, h, 1244);
    g_edexp   = mkedit(ID_EDEXP, L"导出PDF", 102, 631, LEFT_W - 16 - 102 - 8, 22, h, 0);
    g_lblbusy = mklabel(L"处理中 提示", 12, 657, 86, 20, h, 1245);
    g_edbusy  = mkedit(ID_EDBUSY, L"处理中", 102, 657, LEFT_W - 16 - 102 - 8, 22, h, 0);
    g_lblport = mklabel(L"调试端口", 12, 683, 86, 20, h, 1246);
    g_edport  = mkedit(ID_EDPORT, L"9222", 102, 683, 80, 22, h, 0);
    g_chkpoll = mkchk(ID_CHKPOLL, L"兼容模式：轮询系统下载文件夹自动归集",
                      12, 709, LEFT_W - 24, 20, h);
    g_btnsave = mkbtn(ID_SAVECFG, L"保存设置", 12, 733, 90, 22, h);
    {
        HWND items[] = { g_lbltpl, g_edtpl, g_lblhw, g_edhw, g_lblwm, g_edwm,
                         g_lblexp, g_edexp, g_lblbusy, g_edbusy,
                         g_lblport, g_edport, g_chkpoll, g_btnsave };
        for (int i = 0; i < (int)(sizeof(items) / sizeof(items[0])); i++)
            ShowWindow(items[i], SW_HIDE);
    }

    g_log = CreateWindowW(L"EDIT", L"",
        WS_CHILD | WS_VISIBLE | WS_BORDER | WS_VSCROLL | ES_MULTILINE |
        ES_READONLY | ES_AUTOVSCROLL,
        8, 553, LEFT_W - 16, 110, h, (HMENU)(INT_PTR)ID_LOG, NULL, NULL);
    mklabel(L"登录后软件在已登录页面上自动上传/点击/下载；改版后用「测试当前页面」校准按钮文字。",
            8, 667, LEFT_W - 16, 18, h, 1221);
    mklabel(L"不抓接口、不绕过会员、不保存密码/Cookie。仅处理有权限改的资料；自动化有风控风险。",
            8, 685, LEFT_W - 16, 18, h, 1222);
    g_crc = mklabel(L"CRC32：计算中…", 8, 703, LEFT_W - 16, 18, h, ID_CRC);

    /* 右侧 */
    int RX = LEFT_W + 6;
    mklabel(L"夸克扫描王 · 登录后全自动批量处理", RX, 8, 340, 22, h, 1231);
    g_lstate = mklabel(L"● 未登录", RX + 346, 8, 200, 22, h, ID_LSTATE);
    mkbtn(ID_LOGIN, L"登录 / 打开夸克扫描王", RX + 560, 6, 180, 28, h);
    g_addr = mkedit(ID_ADDR, DEFAULTPAGE, RX, 40, 730, 22, h, 1);

    g_rightbox = CreateWindowW(L"BUTTON", L"全自动流程",
        WS_CHILD | WS_VISIBLE | BS_GROUPBOX,
        RX, 70, 730, 560, h, (HMENU)(INT_PTR)ID_RIGHTBOX, NULL, NULL);
    g_righttxt = mklabel(
        L"\r\n\r\n"
        L"        ① 点右上「登录 / 打开夸克扫描王」\r\n"
        L"           软件开一个带调试端口的专用 Edge / Chrome 窗口\r\n"
        L"           你用手机号验证码 / 夸克 App 扫码登录会员（只此一次）\r\n\r\n"
        L"        ② 在浏览器里点到「文件工具中心 / 去除手写 / 去除水印」那一页，\r\n"
        L"           再回本软件点「我已登录完成」→ 软件接管并记下这一页为工作页\r\n"
        L"           （停在官网首页会找不到上传框）\r\n\r\n"
        L"        ③ 导入 PDF → 选输出文件夹 → 选处理模式 → 点「开始」\r\n"
        L"           之后全程不用再碰浏览器：\r\n"
        L"           自动塞文件 → 自动点去水印/去手写 → 自动点下载\r\n"
        L"           → 自动归集改名 → 自动切到下一个\r\n\r\n"
        L"        ────────────────────────────────────────────\r\n"
        L"        登录态存在软件专用 profile，重开不用再登；软件不读取账号信息。\r\n"
        L"        官网改版导致点不到按钮时，点「测试当前页面」看真实文字，\r\n"
        L"        填进「▸ 更多设置」里对应的输入框，点「保存设置」即可。\r\n"
        L"        接管失败时自动降级半自动（网页里那几下由你点，其余仍自动）。",
        RX + 20, 90, 690, 520, h, ID_RIGHTTXT);
    mklabel(L"↑ 不内嵌 Chromium，改用系统 Edge/Chrome + 调试端口接管，exe 仍只有一百多 KB。\r\n"
            L"软件会在你已登录的页面上代为点击；不抓包、不伪造请求、不绕过会员校验。",
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
    SetWindowTextW(g_edhw, g_btn_hw);
    SetWindowTextW(g_edwm, g_btn_wm);
    SetWindowTextW(g_edexp, g_btn_exp);
    SetWindowTextW(g_edbusy, g_btn_busy);
    {
        wchar_t p[32]; _snwprintf(p, 32, L"%d", g_port);
        SetWindowTextW(g_edport, p);
    }
    SetWindowTextW(g_addr, g_workurl[0] ? g_workurl : g_url);
    SendMessageW(g_cmbmode, CB_SETCURSEL, (WPARAM)g_mode, 0);
    SendMessageW(g_chknext, BM_SETCHECK, g_autonext ? BST_CHECKED : BST_UNCHECKED, 0);
    SendMessageW(g_chkclip, BM_SETCHECK, g_autoclip ? BST_CHECKED : BST_UNCHECKED, 0);
    SendMessageW(g_chkpoll, BM_SETCHECK, g_poll ? BST_CHECKED : BST_UNCHECKED, 0);
    SendMessageW(g_chkauto, BM_SETCHECK, g_automode ? BST_CHECKED : BST_UNCHECKED, 0);
    SetWindowTextW(g_lblauto, g_automode
        ? L"● 全自动：待登录接管（点「我已登录完成」）"
        : L"半自动模式");
    SetTimer(h, 1, 1200, NULL);
    DragAcceptFiles(h, TRUE);
    cdp_wsa_init();

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
            L"QuarkBatch Auto 完整性校验\r\n\r\n"
            L"整文件 CRC32 : %08X\r\n"
            L"文件大小     : %llu 字节\r\n"
            L"版本         : %s\r\n\r\n"
            L"原生 Win32 单文件，无外部运行时依赖。\r\n"
            L"全自动：登录后由软件接管已登录页面，代为上传/点击/下载。", c, sz, APPNAME);
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
        L"夸克扫描王 · 登录后全自动批量处理  —  QuarkBatch Auto v3.0",
        WS_OVERLAPPEDWINDOW, CW_USEDEFAULT, CW_USEDEFAULT, 1180, 800,
        NULL, NULL, hi, NULL);
    if (!h) return 1;
    build_ui(h);
    ShowWindow(h, show);
    UpdateWindow(h);

    logline(L"欢迎使用。① 点「登录 / 打开夸克扫描王」→ 在弹出的浏览器窗口里扫码登录会员（只此一次）");
    logline(L"② 回到本软件点「我已登录完成」，软件接管那个已登录页面");
    logline(L"③ 导入 PDF → 选输出文件夹 → 选处理模式 → 点「开始」，之后不用再碰浏览器");
    logline(L"   自动塞文件 → 点去水印/去手写 → 点下载 → 归集改名 → 切到下一个");
    logline(L"※ 全自动会在已登录页面上代为点击；不抓包、不伪造请求、不保存密码/Cookie、不绕过会员。");
    logline(L"※ 官网改版点不到按钮时，用「测试当前页面」看真实文字，填进「更多设置」保存。");
    logline(L"※ 接管失败会自动降级半自动；自动化操作有账号风控风险，请适度限速。");

    MSG msg;
    while (GetMessageW(&msg, NULL, 0, 0) > 0) {
        TranslateMessage(&msg);
        DispatchMessageW(&msg);
    }
    OleUninitialize();
    return 0;
}
