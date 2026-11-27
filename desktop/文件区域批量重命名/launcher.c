/*
 * FileAreaRenamer - Windows 单文件启动器
 * 文件布局: [本 PE] [payload.zip] [32 字节 trailer]
 * trailer:  "FAR1" | uint64 payload_size | uint64 payload_crc32 | uint64 version | 4 字节保留
 * 启动时对 payload 做 CRC32 自检，损坏/被篡改则拒绝运行。
 * 参数 /verify : 只校验并弹窗显示 CRC 信息。
 */
#include <windows.h>
#include <stdint.h>
#include <stdio.h>
#include <string.h>
#include <wchar.h>

#define TRAILER_SIZE 32
#define CHUNK 65536

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
    MessageBoxW(NULL, b, L"FileAreaRenamer · 完整性校验", MB_ICONERROR | MB_OK | MB_SETFOREGROUND);
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
    wchar_t zippath[32768], apppath[32768], pywpath[32768];
    wchar_t cmd[32768], sysdir[1024], tarpath[1024], qtplugins[32768], qtplatforms[32768];
    wchar_t oldpath[32768], newpath[32768], msg[2048];
    unsigned char t[TRAILER_SIZE], buf[CHUNK];
    uint64_t psize = 0, pcrc = 0, ver = 0;
    int64_t off, left;
    int64_t fsz = 0;
    uint32_t crc;
    HANDLE h, fout = INVALID_HANDLE_VALUE;
    LARGE_INTEGER li;
    DWORD got, written, code = 1;
    STARTUPINFOW si;
    PROCESS_INFORMATION pi;
    int cached, verify = 0, ok;
    wchar_t *cl;

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

    li.QuadPart = fsz - TRAILER_SIZE;
    if (!SetFilePointerEx(h, li, NULL, FILE_BEGIN)) die(L"读取尾部失败", self);
    if (!ReadFile(h, t, TRAILER_SIZE, &got, NULL) || got != TRAILER_SIZE)
        die(L"读取校验尾失败", self);
    if (memcmp(t, "FAR1", 4) != 0) die(L"校验尾标识缺失，文件可能被截断", self);
    memcpy(&psize, t + 4, 8);
    memcpy(&pcrc, t + 12, 8);
    memcpy(&ver, t + 20, 8);
    if (psize == 0 || psize > (uint64_t)(fsz - TRAILER_SIZE)) die(L"校验尾数据非法", self);
    off = fsz - TRAILER_SIZE - (int64_t)psize;

    if (!GetTempPathW(1024, tmp)) wcscpy(tmp, L"C:\\Windows\\Temp\\");
    _snwprintf(work, 32768, L"%lsFileAreaRenamer_%08X", tmp, (unsigned)(pcrc & 0xffffffffu));
    work[32767] = 0;
    if (!exists(work)) CreateDirectoryW(work, NULL);
    join(zippath, 32768, work, L"payload.zip");
    join(apppath, 32768, work, L"app.py");
    join(pywpath, 32768, work, L"pythonw.exe");
    cached = exists(apppath) && exists(pywpath);

    li.QuadPart = off;
    if (!SetFilePointerEx(h, li, NULL, FILE_BEGIN)) die(L"定位内置数据失败", self);
    if (!cached) {
        fout = CreateFileW(zippath, GENERIC_WRITE, 0, NULL, CREATE_ALWAYS,
                           FILE_ATTRIBUTE_NORMAL, NULL);
        if (fout == INVALID_HANDLE_VALUE) die(L"无法写入临时目录", zippath);
    }
    crc = 0xFFFFFFFFu;
    left = (int64_t)psize;
    while (left > 0) {
        DWORD want = (left > CHUNK) ? CHUNK : (DWORD)left;
        if (!ReadFile(h, buf, want, &got, NULL) || got == 0) {
            CloseHandle(h);
            die(L"读取内置数据失败", self);
        }
        crc = crc_update(crc, buf, got);
        if (fout != INVALID_HANDLE_VALUE) {
            if (!WriteFile(fout, buf, got, &written, NULL) || written != got) {
                CloseHandle(fout); CloseHandle(h);
                die(L"写入临时文件失败", zippath);
            }
        }
        left -= got;
    }
    crc ^= 0xFFFFFFFFu;
    if (fout != INVALID_HANDLE_VALUE) CloseHandle(fout);
    CloseHandle(h);

    if (crc != (uint32_t)(pcrc & 0xffffffffu))
        die(L"CRC32 校验失败：程序已损坏或被改动，请重新获取。", L"");

    if (verify) {
        _snwprintf(msg, 2048,
                   L"CRC32 校验通过 ✔\n\n内置数据 CRC32 : %08X\n内置数据大小 : %llu 字节\nEXE 总大小 : %lld 字节\n版本标记 : %llu\n\n文件完整，未被篡改。",
                   (unsigned)crc, (unsigned long long)psize, (long long)fsz,
                   (unsigned long long)ver);
        msg[2047] = 0;
        MessageBoxW(NULL, msg, L"FileAreaRenamer · CRC 校验", MB_ICONINFORMATION | MB_OK);
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
            WaitForSingleObject(pi.hProcess, 180000);
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
        if (!exists(apppath) || !exists(pywpath)) die(L"内置运行时解压失败，请检查磁盘空间或临时目录权限。", work);
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
                   L"%ls\\Lib\\site-packages\\pywin32_system32;%ls\\Lib\\site-packages\\PySide6;%ls\\Lib\\site-packages\\shiboken6;%ls\\Lib\\site-packages\\pymupdf;%ls\\Lib\\site-packages\\onnxruntime\\capi;%ls",
                   work, work, work, work, work, oldpath);
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
