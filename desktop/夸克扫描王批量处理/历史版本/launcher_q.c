/*
 * QuarkBatch - Windows 单文件启动器  (v2：校验信息前置)
 *
 * 文件布局: [本 PE][32 字节 trailer][payload.zip][允许被追加的任意尾部数据]
 * trailer: "FAR1" | uint64 payload_size | uint64 payload_crc32 |
 *          uint64 expected_total_size | 4 字节保留
 *
 * 为什么把 trailer 挪到前面（旧版放在文件最后）：
 *   · 旧版只要文件末尾被下载工具 / 网盘 / 杀软追加了任何数据，
 *     读到的最后 32 字节就不是 FAR1，直接弹「校验尾标识缺失」；
 *     哪怕 payload 本身一个字节都没少也跑不起来。
 *   · 新版从文件开头 256KB 内找 FAR1（PE stub 只有几十 KB），
 *     payload 按「长度」读取，末尾追加的数据一律忽略。
 *   · 若真被截断，会明确弹出「实际 X 字节 / 应为 Y 字节 / 缺 Z 字节」，
 *     不再只给一句含糊的「可能被截断」。
 *
 * 启动流程：CRC32 自检 → 解压内置运行时到 %TEMP% → 启动 pythonw.exe app.py
 * 参数 /verify : 只校验并弹窗显示 CRC 信息。
 */
#include <windows.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <wchar.h>

#define TRAILER_SIZE 32
#define CHUNK 65536
#define SCAN_LIMIT (256 * 1024)

static uint32_t g_table[256];
static int g_table_ready = 0;

static void crc_init(void) {
    uint32_t i; int k;
    for (i = 0; i < 256; i++) {
        uint32_t c = i;
        for (k = 0; k < 8; k++) c = (c & 1) ? (0xEDB88320u ^ (c >> 1)) : (c >> 1);
        g_table[i] = c;
    }
    g_table_ready = 1;
}
static uint32_t crc_update(uint32_t c, const unsigned char *p, size_t n) {
    size_t i;
    if (!g_table_ready) crc_init();
    for (i = 0; i < n; i++) c = g_table[(c ^ p[i]) & 0xff] ^ (c >> 8);
    return c;
}
static void die(const wchar_t *msg, const wchar_t *extra) {
    wchar_t b[2048];
    _snwprintf(b, 2048, L"%ls\n%ls", msg, extra ? extra : L"");
    b[2047] = 0;
    MessageBoxW(NULL, b, L"QuarkBatch · 完整性校验", MB_ICONERROR | MB_OK | MB_SETFOREGROUND);
    ExitProcess(1);
}
static int exists(const wchar_t *p) {
    return GetFileAttributesW(p) != INVALID_FILE_ATTRIBUTES;
}
static void join(wchar_t *out, size_t cap, const wchar_t *a, const wchar_t *b) {
    _snwprintf(out, (int)cap, L"%ls\\%ls", a, b);
    out[cap - 1] = 0;
}

static int real_main(void) {
    wchar_t self[32768], exedir[32768], tmp[1024], work[32768];
    wchar_t zippath[32768], apppath[32768], pywpath[32768], okpath[32768], msg[2048];
    wchar_t cmd[32768], sysdir[1024], tarpath[1024], qtplugins[32768], qtplatforms[32768];
    wchar_t oldpath[32768], newpath[32768];
    unsigned char t[TRAILER_SIZE], buf[CHUNK];
    uint64_t psize = 0, pcrc = 0, expected = 0;
    int64_t off, left, tp = -1;
    int64_t fsz = 0, total_read = 0;
    uint32_t crc;
    HANDLE h, fout = INVALID_HANDLE_VALUE;
    LARGE_INTEGER li;
    DWORD got, written, code = 1;
    STARTUPINFOW si;
    PROCESS_INFORMATION pi;
    int cached, verify = 0, ok;
    wchar_t *cl;
    unsigned char *head = NULL;
    DWORD scan, got_all = 0, i, search_from = 0;
    int crc_mismatch = 0, incomplete = 0;
    uint64_t inc_psize = 0, inc_expected = 0;
    int64_t inc_read = 0, inc_left = 0;

    cl = GetCommandLineW();
    if (cl && (wcsstr(cl, L"/verify") || wcsstr(cl, L"--verify") || wcsstr(cl, L"-verify"))) verify = 1;

    if (!GetModuleFileNameW(NULL, self, 32768)) die(L"无法获取自身路径", L"");
    self[32767] = 0;
    wcscpy(exedir, self);
    {
        wchar_t *slash = wcsrchr(exedir, L'\\');
        if (slash) *slash = 0;
    }

    h = CreateFileW(self, GENERIC_READ, FILE_SHARE_READ | FILE_SHARE_WRITE,
                    NULL, OPEN_EXISTING, FILE_ATTRIBUTE_NORMAL, NULL);
    if (h == INVALID_HANDLE_VALUE) die(L"无法打开自身文件", self);
    {
        LARGE_INTEGER s;
        if (!GetFileSizeEx(h, &s)) die(L"无法读取文件大小", self);
        fsz = s.QuadPart;
    }
    if (fsz < TRAILER_SIZE + 64) die(L"文件过小，不是完整程序", self);

    /* ---- 从文件开头找 trailer（STUB 只有几十 KB；末尾追加数据不影响） ---- */
    scan = (DWORD)((fsz < SCAN_LIMIT) ? fsz : SCAN_LIMIT);
    head = (unsigned char *)malloc(scan + 8);
    if (!head) die(L"内存不足", L"");
    li.QuadPart = 0;
    SetFilePointerEx(h, li, NULL, FILE_BEGIN);
    while (got_all < scan) {
        if (!ReadFile(h, head + got_all, scan - got_all, &got, NULL) || got == 0) break;
        got_all += got;
    }

    for (;;) {
        /* 找下一个合法候选：PE 里也可能出现 "FAR1" 字面量，
           用 payload 长度 + 期望总大小两个字段把它过滤掉。 */
        tp = -1;
        for (i = search_from; i + TRAILER_SIZE <= got_all; i++) {
            uint64_t sz = 0, exp = 0;
            if (memcmp(head + i, "FAR1", 4) != 0) continue;
            memcpy(&sz, head + i + 4, 8);
            memcpy(&exp, head + i + 20, 8);
            if (sz == 0 || exp == 0) continue;
            if (exp < (uint64_t)((int64_t)i + TRAILER_SIZE + (int64_t)sz)) continue;
            /* 文件可能被截断得很厉害，expected 会大于实际大小，这里放宽上界；
               只挡住随机字节造成的天文数字。 */
            if (exp > (uint64_t)4 * 1024 * 1024 * 1024ULL) continue;
            tp = i;
            memcpy(&psize, head + i + 4, 8);
            memcpy(&pcrc, head + i + 12, 8);
            memcpy(&expected, head + i + 20, 8);
            break;
        }
        if (tp < 0) {
            if (incomplete) {
                _snwprintf(msg, 2048,
                           L"文件不完整：内置数据被截断了。\n\n"
                           L"应读取 : %llu 字节\n"
                           L"实读取 : %lld 字节\n"
                           L"还缺   : %lld 字节\n\n"
                           L"当前文件大小 : %lld 字节\n"
                           L"完整文件大小应为 : %llu 字节\n\n"
                           L"请删掉本文件重新下载 / 复制，完成后核对大小。\n"
                           L"别用聊天工具或网盘转存大文件，也先关掉杀软实时防护。",
                           (unsigned long long)inc_psize, (long long)inc_read,
                           (long long)inc_left, (long long)fsz,
                           (unsigned long long)inc_expected);
            } else if (crc_mismatch) {
                _snwprintf(msg, 2048,
                           L"CRC32 校验失败：文件内容与内置校验值不符。\n\n"
                           L"当前文件实际大小 : %lld 字节\n"
                           L"完整文件大小应为 : %llu 字节\n\n"
                           L"这通常是文件被安全软件改写，或下载 / 复制过程出错。\n"
                           L"请删掉本文件重新完整获取，并核对大小与 SHA256。",
                           (long long)fsz, (unsigned long long)expected);
            } else {
                _snwprintf(msg, 2048,
                           L"找不到内置的校验信息。\n\n"
                           L"当前文件实际大小 : %lld 字节\n\n"
                           L"这通常是下载 / 复制没完成，或文件被安全软件改写过。\n"
                           L"请删掉本文件，重新完整下载一次，并核对大小与 SHA256 后再运行。",
                           (long long)fsz);
            }
            msg[2047] = 0;
            free(head); CloseHandle(h);
            MessageBoxW(NULL, msg, L"QuarkBatch · 完整性校验", MB_ICONERROR | MB_OK | MB_SETFOREGROUND);
            ExitProcess(1);
        }
        off = tp + TRAILER_SIZE;

        if (!GetTempPathW(1024, tmp)) wcscpy(tmp, L"C:\\Windows\\Temp\\");
        _snwprintf(work, 32768, L"%lsQuarkBatch_%08X", tmp, (unsigned)(pcrc & 0xffffffffu));
        work[32767] = 0;
        if (!exists(work)) CreateDirectoryW(work, NULL);
        join(zippath, 32768, work, L"payload.zip");
        join(apppath, 32768, work, L"app.py");
        join(pywpath, 32768, work, L"pythonw.exe");
        join(okpath, 32768, work, L"unpack_ok.txt");
        cached = exists(apppath) && exists(pywpath) && exists(okpath);

        /* ---- 按长度读 payload；末尾被追加的数据自动忽略 ---- */
        li.QuadPart = off;
        if (!SetFilePointerEx(h, li, NULL, FILE_BEGIN)) die(L"定位内置数据失败", self);
        fout = INVALID_HANDLE_VALUE;
        if (!cached) {
            fout = CreateFileW(zippath, GENERIC_WRITE, 0, NULL, CREATE_ALWAYS,
                               FILE_ATTRIBUTE_NORMAL, NULL);
            if (fout == INVALID_HANDLE_VALUE) die(L"无法写入临时目录", zippath);
        }
        crc = 0xFFFFFFFFu;
        left = (int64_t)psize;
        total_read = 0;
        while (left > 0) {
            DWORD want = (left > CHUNK) ? CHUNK : (DWORD)left;
            if (!ReadFile(h, buf, want, &got, NULL) || got == 0) break;
            crc = crc_update(crc, buf, got);
            if (fout != INVALID_HANDLE_VALUE) {
                if (!WriteFile(fout, buf, got, &written, NULL) || written != got) {
                    CloseHandle(fout); CloseHandle(h);
                    die(L"写入临时文件失败（磁盘空间不足？）", zippath);
                }
            }
            left -= got;
            total_read += got;
        }
        crc ^= 0xFFFFFFFFu;
        if (fout != INVALID_HANDLE_VALUE) CloseHandle(fout);

        if (crc == (uint32_t)(pcrc & 0xffffffffu) && left == 0) break;  /* 校验通过 */
        if (left > 0) {
            /* 记下来，但先继续找下一个候选（前面可能误命中了 PE 里的字面量） */
            incomplete = 1;
            inc_psize = psize; inc_read = total_read;
            inc_left = left; inc_expected = expected;
        } else {
            crc_mismatch = 1;   /* 长度够但 CRC 不符 */
        }
        search_from = (DWORD)tp + 1;
    }
    free(head);
    CloseHandle(h);

    if (verify) {
        _snwprintf(msg, 2048,
                   L"CRC32 校验通过 ✔\n\n"
                   L"内置数据 CRC32 : %08X\n"
                   L"内置数据大小   : %llu 字节\n"
                   L"EXE 实际大小   : %lld 字节\n"
                   L"EXE 应为大小   : %llu 字节\n\n"
                   L"文件完整，未被篡改。",
                   (unsigned)crc, (unsigned long long)psize,
                   (long long)fsz, (unsigned long long)expected);
        msg[2047] = 0;
        MessageBoxW(NULL, msg, L"QuarkBatch · CRC 校验", MB_ICONINFORMATION | MB_OK);
        return 0;
    }

    if (!cached) {
        if (!GetSystemDirectoryW(sysdir, 1024)) wcscpy(sysdir, L"C:\\Windows\\System32");
        _snwprintf(tarpath, 1024, L"%ls\\tar.exe", sysdir);
        tarpath[1023] = 0;
        _snwprintf(cmd, 32768, L"\"%ls\" -xf \"%ls\" -C \"%ls\"", tarpath, zippath, work);
        cmd[32767] = 0;
        memset(&si, 0, sizeof(si)); si.cb = sizeof(si);
        memset(&pi, 0, sizeof(pi));
        ok = CreateProcessW(NULL, cmd, NULL, NULL, FALSE, CREATE_NO_WINDOW,
                            NULL, work, &si, &pi);
        if (ok) {
            WaitForSingleObject(pi.hProcess, 300000);
            GetExitCodeProcess(pi.hProcess, &code);
            CloseHandle(pi.hThread); CloseHandle(pi.hProcess);
        } else code = 1;

        if (code != 0 || !exists(apppath)) {
            _snwprintf(cmd, 32768,
                       L"powershell.exe -NoProfile -ExecutionPolicy Bypass -Command \"Expand-Archive -LiteralPath '%ls' -DestinationPath '%ls' -Force\"",
                       zippath, work);
            cmd[32767] = 0;
            memset(&si, 0, sizeof(si)); si.cb = sizeof(si);
            memset(&pi, 0, sizeof(pi));
            if (CreateProcessW(NULL, cmd, NULL, NULL, FALSE, CREATE_NO_WINDOW,
                               NULL, work, &si, &pi)) {
                WaitForSingleObject(pi.hProcess, 300000);
                CloseHandle(pi.hThread); CloseHandle(pi.hProcess);
            }
        }
        if (!exists(apppath) || !exists(pywpath)) {
            _snwprintf(msg, 2048,
                       L"内置运行时解压失败。\n\n"
                       L"解压目录：%ls\n\n"
                       L"通常是磁盘剩余空间不足（需约 400 MB）或临时目录被安全软件拦截。\n"
                       L"清理 C 盘 / 关闭杀软实时防护后重试。", work);
            msg[2047] = 0;
            MessageBoxW(NULL, msg, L"QuarkBatch · 运行时解压", MB_ICONERROR | MB_OK);
            ExitProcess(1);
        }
        {
            HANDLE f = CreateFileW(okpath, GENERIC_WRITE, 0, NULL, CREATE_ALWAYS,
                                   FILE_ATTRIBUTE_NORMAL, NULL);
            if (f != INVALID_HANDLE_VALUE) { CloseHandle(f); }
        }
    }

    SetEnvironmentVariableW(L"FAR_EXE_DIR", exedir);
    SetEnvironmentVariableW(L"FAR_SELF", self);
    SetEnvironmentVariableW(L"FAR_BUNDLE_DIR", work);
    _snwprintf(qtplugins, 32768, L"%ls\\Lib\\site-packages\\PySide6\\plugins", work);
    qtplugins[32767] = 0;
    _snwprintf(qtplatforms, 32768, L"%ls\\platforms", qtplugins);
    qtplatforms[32767] = 0;
    SetEnvironmentVariableW(L"QT_PLUGIN_PATH", qtplugins);
    SetEnvironmentVariableW(L"QT_QPA_PLATFORM_PLUGIN_PATH", qtplatforms);
    SetEnvironmentVariableW(L"PYTHONIOENCODING", L"utf-8");
    SetEnvironmentVariableW(L"PYTHONUTF8", L"1");
    {
        DWORD n = GetEnvironmentVariableW(L"PATH", oldpath, 32768);
        if (n == 0) oldpath[0] = 0;
        _snwprintf(newpath, 32768,
                   L"%ls\\Lib\\site-packages\\PySide6;%ls\\Lib\\site-packages\\pywin32_system32;%ls\\Lib\\site-packages\\shiboken6;%ls\\Lib\\site-packages\\pymupdf;%ls",
                   work, work, work, work, oldpath);
        newpath[32767] = 0;
        SetEnvironmentVariableW(L"PATH", newpath);
    }

    _snwprintf(cmd, 32768, L"\"%ls\" \"%ls\"", pywpath, apppath);
    cmd[32767] = 0;
    memset(&si, 0, sizeof(si)); si.cb = sizeof(si);
    memset(&pi, 0, sizeof(pi));
    if (!CreateProcessW(pywpath, cmd, NULL, NULL, FALSE, 0, NULL, exedir, &si, &pi))
        die(L"无法启动内置 Python 运行时", pywpath);
    WaitForSingleObject(pi.hProcess, INFINITE);
    CloseHandle(pi.hThread);
    CloseHandle(pi.hProcess);
    return 0;
}

int WINAPI WinMain(HINSTANCE a, HINSTANCE b, LPSTR c, int d) {
    (void)a; (void)b; (void)c; (void)d;
    return real_main();
}
int main(int argc, char **argv) {
    (void)argc; (void)argv;
    return real_main();
}
