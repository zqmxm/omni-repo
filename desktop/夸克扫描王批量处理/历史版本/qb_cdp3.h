/*
 * qb_cdp.h —— 最小 Chrome DevTools Protocol 客户端（原生 Win32 / WinSock2）
 *
 * 只实现本项目需要的几件事，不引第三方库、不打包 Python：
 *   · HTTP GET /json/list 拿 page 的 webSocketDebuggerUrl
 *   · WebSocket 握手 + 客户端 masked text frame + 服务端分片/长帧读取
 *   · Runtime.evaluate（按可见文字点按钮 / 读页面状态 / 取 input 的 objectId）
 *   · DOM.setFileInputFiles（把本地 PDF 直接塞进网页文件输入框）
 *
 * 注意：这是「接管用户已登录的浏览器窗口」，不是抓包、不是伪造接口、
 * 不是绕过会员。所有处理仍由用户的会员账号在夸克网站上执行。
 */
#ifndef QB_CDP_H
#define QB_CDP_H

#include <windows.h>
#include <winsock2.h>
#include <ws2tcpip.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#define CDP_PORT       9222
#define CDP_BUF_MAX    (4 * 1024 * 1024)
#define CDP_TIMEOUT_MS 1200
#define CDP_MAX_FRAGMENTS 64       /* 防止分片无限循环把线程挂死 */
#define CDP_MAX_EVENT_SKIP 10     /* 单次调用最多跳过 10 个事件帧 */

static int cdp_wsa_ready = 0;

static void cdp_wsa_init(void) {
    if (cdp_wsa_ready) return;
    WSADATA d;
    if (WSAStartup(MAKEWORD(2, 2), &d) == 0) cdp_wsa_ready = 1;
}

static int cdp_read_exact(SOCKET s, void *buf, int n) {
    unsigned char *p = (unsigned char *)buf;
    int got = 0;
    while (got < n) {
        int r = recv(s, (char *)(p + got), n - got, 0);
        if (r <= 0) return 0;
        got += r;
    }
    return 1;
}

/* ---- GET http://127.0.0.1:PORT/path ，返回 malloc 的 body（含结尾 0） ---- */
static char *cdp_http_get(int port, const char *path) {
    cdp_wsa_init();
    SOCKET s = socket(AF_INET, SOCK_STREAM, IPPROTO_TCP);
    if (s == INVALID_SOCKET) return NULL;
    DWORD tv = CDP_TIMEOUT_MS;
    setsockopt(s, SOL_SOCKET, SO_RCVTIMEO, (const char *)&tv, sizeof(tv));
    setsockopt(s, SOL_SOCKET, SO_SNDTIMEO, (const char *)&tv, sizeof(tv));
    struct sockaddr_in a; memset(&a, 0, sizeof(a));
    a.sin_family = AF_INET; a.sin_port = htons((u_short)port);
    a.sin_addr.s_addr = htonl(INADDR_LOOPBACK);
    if (connect(s, (struct sockaddr *)&a, sizeof(a)) != 0) {
        closesocket(s); return NULL;
    }
    char req[1024];
    int rn = _snprintf(req, sizeof(req),
        "GET %s HTTP/1.1\r\nHost: 127.0.0.1:%d\r\n"
        "Accept: */*\r\nConnection: close\r\n\r\n", path, port);
    if (rn <= 0 || send(s, req, rn, 0) != rn) { closesocket(s); return NULL; }

    size_t cap = 65536, len = 0;
    char *buf = (char *)malloc(cap);
    if (!buf) { closesocket(s); return NULL; }
    for (;;) {
        if (len + 4096 + 1 > cap) {
            cap *= 2;
            char *nb = (char *)realloc(buf, cap);
            if (!nb) break;
            buf = nb;
        }
        int r = recv(s, buf + len, (int)(cap - len - 1), 0);
        if (r <= 0) break;
        len += r;
        buf[len] = 0;
        if (len > CDP_BUF_MAX) break;
    }
    closesocket(s);
    /* 跳过响应头 */
    char *body = strstr(buf, "\r\n\r\n");
    if (body) body += 4; else body = buf;
    /* chunked 时去掉块大小行；/json/list 一般带 Content-Length，这里做宽松处理 */
    char *out = (char *)malloc(strlen(body) + 1);
    if (out) strcpy(out, body);
    free(buf);
    return out;
}

/* ---- 从 /json/list 的 JSON 里挑一个 quark 页面的 ws 路径 ---- */
static char *cdp_find_page_ws(int port, char *outUrl, size_t urlCap) {
    char *j = cdp_http_get(port, "/json/list");
    if (!j) return NULL;
    /* /json/list 是对象数组，字段顺序里 "url" 在 "webSocketDebuggerUrl" 之前，
       所以必须在「本对象范围内」找字段，不能从 ws 字段往后找 url。 */
    char *best = NULL;
    if (outUrl && urlCap) outUrl[0] = 0;

    char *p = j;
    while ((p = strchr(p, '{')) != NULL) {
        char *end = strchr(p, '}');
        if (!end) break;
        char obj[8192];
        size_t on = (size_t)(end - p + 1);
        if (on >= sizeof(obj)) on = sizeof(obj) - 1;
        memcpy(obj, p, on); obj[on] = 0;

        /* 只要 page 类型 */
        if (!strstr(obj, "\"type\"") || !strstr(obj, "\"page\""))
            { p = end + 1; continue; }

        /* url */
        char url[1024]; url[0] = 0;
        char *u = strstr(obj, "\"url\"");
        if (u) {
            char *c = strchr(u, ':');
            if (c) {
                char *q1 = strchr(c, '"');
                if (q1) {
                    char *q2 = strchr(q1 + 1, '"');
                    if (q2) {
                        size_t n = (size_t)(q2 - q1 - 1);
                        if (n >= sizeof(url)) n = sizeof(url) - 1;
                        memcpy(url, q1 + 1, n); url[n] = 0;
                    }
                }
            }
        }
        /* webSocketDebuggerUrl */
        char *wsfield = strstr(obj, "\"webSocketDebuggerUrl\"");
        if (!wsfield) { p = end + 1; continue; }
        char *c = strchr(wsfield, ':');
        if (!c) { p = end + 1; continue; }
        char *q1 = strchr(c, '"');
        if (!q1) { p = end + 1; continue; }
        char *q2 = strchr(q1 + 1, '"');
        if (!q2) { p = end + 1; continue; }
        char ws[1024]; size_t n = (size_t)(q2 - q1 - 1);
        if (n >= sizeof(ws)) n = sizeof(ws) - 1;
        memcpy(ws, q1 + 1, n); ws[n] = 0;
        char *path = strstr(ws, "/devtools/");
        if (!path) { p = end + 1; continue; }

        free(best);
        best = (char *)malloc(strlen(path) + 1);
        if (best) strcpy(best, path);
        if (outUrl && urlCap) {
            size_t un = strlen(url);
            if (un >= urlCap) un = urlCap - 1;
            memcpy(outUrl, url, un); outUrl[un] = 0;
        }
        if (strstr(url, "quark.cn")) break;   /* 夸克页优先，找到就停 */
        p = end + 1;
    }
    free(j);
    return best;
}

/* ---- WebSocket 握手 ---- */
static SOCKET cdp_ws_connect(int port, const char *wspath) {
    cdp_wsa_init();
    SOCKET s = socket(AF_INET, SOCK_STREAM, IPPROTO_TCP);
    if (s == INVALID_SOCKET) return INVALID_SOCKET;
    DWORD tv = CDP_TIMEOUT_MS;
    setsockopt(s, SOL_SOCKET, SO_RCVTIMEO, (const char *)&tv, sizeof(tv));
    setsockopt(s, SOL_SOCKET, SO_SNDTIMEO, (const char *)&tv, sizeof(tv));
    struct sockaddr_in a; memset(&a, 0, sizeof(a));
    a.sin_family = AF_INET; a.sin_port = htons((u_short)port);
    a.sin_addr.s_addr = htonl(INADDR_LOOPBACK);
    if (connect(s, (struct sockaddr *)&a, sizeof(a)) != 0) {
        closesocket(s); return INVALID_SOCKET;
    }
    /* Sec-WebSocket-Key：16 字节随机，做 base64 */
    unsigned char raw[16];
    for (int i = 0; i < 16; i++)
        raw[i] = (unsigned char)((GetTickCount64() >> (i % 8) * 3) + i * 37 + 11);
    static const char *b64 = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";
    char key[32]; int k = 0;
    for (int i = 0; i < 16; i += 3) {
        unsigned int v = (raw[i] << 16) | (i + 1 < 16 ? raw[i + 1] << 8 : 0)
                         | (i + 2 < 16 ? raw[i + 2] : 0);
        key[k++] = b64[(v >> 18) & 63];
        key[k++] = b64[(v >> 12) & 63];
        key[k++] = i + 1 < 16 ? b64[(v >> 6) & 63] : '=';
        key[k++] = i + 2 < 16 ? b64[v & 63] : '=';
    }
    key[k] = 0;
    char req[2048];
    int rn = _snprintf(req, sizeof(req),
        "GET %s HTTP/1.1\r\nHost: 127.0.0.1:%d\r\n"
        "Upgrade: websocket\r\nConnection: Upgrade\r\n"
        "Sec-WebSocket-Key: %s\r\nSec-WebSocket-Version: 13\r\n"
        "Origin: http://127.0.0.1:%d\r\n\r\n",
        wspath, port, key, port);
    if (rn <= 0 || send(s, req, rn, 0) != rn) {
        closesocket(s); return INVALID_SOCKET;
    }
    /* 逐字节读到 \r\n\r\n 为止：按块 recv 会把「握手后紧跟着的帧」一起吞掉，
       导致后续 cdp_ws_recv 从半截帧开始解析而错位。 */
    char hdr[4096]; int hn = 0;
    while (hn < (int)sizeof(hdr) - 1) {
        char c;
        int r = recv(s, &c, 1, 0);
        if (r <= 0) break;
        hdr[hn++] = c; hdr[hn] = 0;
        if (hn >= 4 && memcmp(hdr + hn - 4, "\r\n\r\n", 4) == 0) break;
    }
    if (!strstr(hdr, " 101 ")) {
        closesocket(s); return INVALID_SOCKET;
    }
    return s;
}

/* ---- 发一条 masked text frame ---- */
static int cdp_ws_send(SOCKET s, const char *text) {
    size_t n = strlen(text);
    unsigned char head[16]; int hn = 0;
    head[hn++] = 0x81;                       /* FIN + text */
    if (n < 126) {
        head[hn++] = (unsigned char)(0x80 | n);
    } else if (n < 65536) {
        head[hn++] = 0x80 | 126;
        head[hn++] = (unsigned char)((n >> 8) & 0xff);
        head[hn++] = (unsigned char)(n & 0xff);
    } else {
        head[hn++] = 0x80 | 127;
        unsigned long long m = n;
        for (int i = 7; i >= 0; i--) head[hn++] = (unsigned char)((m >> (i * 8)) & 0xff);
    }
    unsigned char mask[4];
    unsigned long long t = GetTickCount64() ^ (unsigned long long)(INT_PTR)text;
    for (int i = 0; i < 4; i++) mask[i] = (unsigned char)((t >> (i * 8)) ^ (0xA5 + i * 17));
    memcpy(head + hn, mask, 4); hn += 4;
    if (send(s, (const char *)head, hn, 0) != hn) return 0;
    /* 分块 mask 后发送，避免另分配整块内存 */
    char chunk[4096];
    size_t off = 0;
    while (off < n) {
        size_t m = n - off; if (m > sizeof(chunk)) m = sizeof(chunk);
        for (size_t i = 0; i < m; i++)
            chunk[i] = text[off + i] ^ mask[(off + i) & 3];
        if (send(s, chunk, (int)m, 0) != (int)m) return 0;
        off += m;
    }
    return 1;
}

/* ---- 收一条完整（可能分片）文本消息 ---- */
static char *cdp_ws_recv(SOCKET s) {
    size_t cap = 65536, len = 0;
    char *acc = (char *)malloc(cap);
    if (!acc) return NULL;
    acc[0] = 0;
    int fragments = 0;
    for (;;) {
        if (++fragments > CDP_MAX_FRAGMENTS) { free(acc); return NULL; }
        unsigned char h[2];
        if (!cdp_read_exact(s, h, 2)) { free(acc); return NULL; }
        int fin = (h[0] & 0x80) != 0;
        int op  = h[0] & 0x0f;
        int masked = (h[1] & 0x80) != 0;
        unsigned long long plen = h[1] & 0x7f;
        if (plen == 126) {
            unsigned char e[2];
            if (!cdp_read_exact(s, e, 2)) { free(acc); return NULL; }
            plen = ((unsigned long long)e[0] << 8) | e[1];
        } else if (plen == 127) {
            unsigned char e[8];
            if (!cdp_read_exact(s, e, 8)) { free(acc); return NULL; }
            plen = 0;
            for (int i = 0; i < 8; i++) plen = (plen << 8) | e[i];
        }
        unsigned char mk[4] = {0, 0, 0, 0};
        if (masked && !cdp_read_exact(s, mk, 4)) { free(acc); return NULL; }
        if (plen > CDP_BUF_MAX) { free(acc); return NULL; }
        if (len + plen + 1 > cap) {
            size_t nc = cap;
            while (nc < len + plen + 1) nc *= 2;
            char *na = (char *)realloc(acc, nc);
            if (!na) { free(acc); return NULL; }
            acc = na; cap = nc;
        }
        if (plen) {
            if (!cdp_read_exact(s, acc + len, (int)plen)) { free(acc); return NULL; }
            if (masked)
                for (unsigned long long i = 0; i < plen; i++)
                    acc[len + i] = (char)((unsigned char)acc[len + i] ^ mk[i & 3]);
            len += plen;
        }
        acc[len] = 0;
        if (op == 0x8) { free(acc); return NULL; }          /* close */
        if (op == 0x9) {                                     /* ping → pong */
            unsigned char pong[2] = {0x8A, 0x00};
            send(s, (const char *)pong, 2, 0);
            continue;
        }
        if (op == 0x1 || op == 0x0) {
            if (fin) return acc;                             /* 完整消息 */
            continue;                                        /* 等续帧 */
        }
        /* 其它控制帧忽略 */
    }
}

/* ---- JSON 字符串转义 ---- */
static char *cdp_json_escape(const char *s) {
    size_t n = strlen(s);
    char *out = (char *)malloc(n * 6 + 2);
    if (!out) return NULL;
    char *w = out;
    for (const char *p = s; *p; p++) {
        unsigned char c = (unsigned char)*p;
        switch (c) {
        case '"':  *w++ = '\\'; *w++ = '"';  break;
        case '\\': *w++ = '\\'; *w++ = '\\'; break;
        case '\n': *w++ = '\\'; *w++ = 'n';  break;
        case '\r': *w++ = '\\'; *w++ = 'r';  break;
        case '\t': *w++ = '\\'; *w++ = 't';  break;
        default:
            if (c < 32) { w += sprintf(w, "\\u%04x", c); }
            else *w++ = (char)c;
        }
    }
    *w = 0;
    return out;
}

/* 从 JSON 里取 "key":"value"（不处理嵌套，够本项目用） */
static int cdp_json_str(const char *j, const char *key, char *out, size_t cap) {
    char pat[64]; _snprintf(pat, sizeof(pat), "\"%s\"", key);
    const char *p = strstr(j, pat);
    if (!p) return 0;
    p += strlen(pat);
    while (*p == ' ' || *p == ':' || *p == '\t') p++;
    if (*p != '"') return 0;
    p++;
    size_t n = 0;
    while (*p && *p != '"' && n + 1 < cap) {
        if (*p == '\\' && p[1]) {
            p++;
            switch (*p) {
            case 'n': out[n++] = '\n'; break;
            case 'r': out[n++] = '\r'; break;
            case 't': out[n++] = '\t'; break;
            case 'u': {
                /* 简单处理 \uXXXX → UTF-8 */
                unsigned int cp = 0;
                for (int i = 1; i <= 4 && p[i]; i++) {
                    char c = p[i];
                    cp = cp * 16 + (c >= '0' && c <= '9' ? c - '0'
                        : c >= 'a' && c <= 'f' ? c - 'a' + 10
                        : c >= 'A' && c <= 'F' ? c - 'A' + 10 : 0);
                }
                p += 4;
                if (cp < 0x80) out[n++] = (char)cp;
                else if (cp < 0x800) {
                    out[n++] = (char)(0xC0 | (cp >> 6));
                    out[n++] = (char)(0x80 | (cp & 63));
                } else {
                    out[n++] = (char)(0xE0 | (cp >> 12));
                    out[n++] = (char)(0x80 | ((cp >> 6) & 63));
                    out[n++] = (char)(0x80 | (cp & 63));
                }
                break;
            }
            default: out[n++] = *p;
            }
            p++;
        } else {
            out[n++] = *p++;
        }
    }
    out[n] = 0;
    return 1;
}

/* ---- 发一个 CDP 命令，返回匹配 id 的完整响应 JSON（malloc） ---- */
static char *cdp_call(SOCKET s, int *id, const char *method, const char *params) {
    int myid = ++(*id);
    char *buf = (char *)malloc(strlen(method) + strlen(params) + 128);
    if (!buf) return NULL;
    _snprintf(buf, strlen(method) + strlen(params) + 128,
              "{\"id\":%d,\"method\":\"%s\",\"params\":%s}", myid, method, params);
    if (!cdp_ws_send(s, buf)) { free(buf); return NULL; }
    free(buf);
    char idpat[32]; _snprintf(idpat, sizeof(idpat), "\"id\":%d", myid);
    for (int tries = 0; tries < CDP_MAX_EVENT_SKIP; tries++) {
        char *r = cdp_ws_recv(s);
        if (!r) return NULL;
        if (strstr(r, idpat)) return r;      /* 命中本次响应 */
        free(r);                              /* 事件帧，跳过 */
    }
    return NULL;
}

/* ---- Runtime.evaluate：返回 result.result.value 的字符串表示 ---- */
static int cdp_eval(SOCKET s, int *id, const char *exprUtf8,
                    char *out, size_t cap) {
    char *e = cdp_json_escape(exprUtf8);
    if (!e) return 0;
    size_t pn = strlen(e) + 256;
    char *params = (char *)malloc(pn);
    if (!params) { free(e); return 0; }
    _snprintf(params, pn,
        "{\"expression\":\"%s\",\"returnByValue\":true,"
        "\"userGesture\":true}", e);
    char *r = cdp_call(s, id, "Runtime.evaluate", params);
    free(params); free(e);
    if (!r) return 0;
    /* 优先取 value（字符串/布尔/数字），再取 objectId */
    int ok = cdp_json_str(r, "value", out, cap);
    if (!ok) ok = cdp_json_str(r, "objectId", out, cap);
    free(r);
    return ok;
}

/* ---- 拿到 file input 的 objectId ---- */
static int cdp_input_object(SOCKET s, int *id, char *out, size_t cap) {
    const char *expr =
        "(function(){var S='input[type=file]';"
        "var e=document.querySelector(S);"
        "if(!e){var fs=document.querySelectorAll('iframe');"
        "for(var i=0;i<fs.length;i++){try{if(fs[i].contentDocument){"
        "var x=fs[i].contentDocument.querySelector(S);if(x){e=x;break;}}}catch(q){}}}"
        "return e||null;})()";
    char *e = cdp_json_escape(expr);
    if (!e) return 0;
    size_t pn = strlen(e) + 256;
    char *params = (char *)malloc(pn);
    if (!params) { free(e); return 0; }
    _snprintf(params, pn,
        "{\"expression\":\"%s\",\"returnByValue\":false,"
        "\"userGesture\":true}", e);
    char *r = cdp_call(s, id, "Runtime.evaluate", params);
    free(params); free(e);
    if (!r) return 0;
    int ok = cdp_json_str(r, "objectId", out, cap);
    free(r);
    return ok;
}

/* ---- 把本地文件塞进 input ---- */
static int cdp_set_files(SOCKET s, int *id, const char *objectId,
                         const wchar_t *pathUtf16) {
    char pathUtf8[MAX_PATH * 3];
    WideCharToMultiByte(CP_UTF8, 0, pathUtf16, -1, pathUtf8, sizeof(pathUtf8),
                        NULL, NULL);
    char *p = cdp_json_escape(pathUtf8);
    char *o = cdp_json_escape(objectId);
    if (!p || !o) { free(p); free(o); return 0; }
    size_t pn = strlen(p) + strlen(o) + 256;
    char *params = (char *)malloc(pn);
    if (!params) { free(p); free(o); return 0; }
    _snprintf(params, pn,
        "{\"files\":[\"%s\"],\"objectId\":\"%s\"}", p, o);
    char *r = cdp_call(s, id, "DOM.setFileInputFiles", params);
    free(params); free(p); free(o);
    int ok = (r != NULL && strstr(r, "\"error\"") == NULL);
    free(r);
    return ok;
}

/* ---- 按可见文字点网页按钮，返回结果码 ---- */
static int cdp_click_text(SOCKET s, int *id, const wchar_t *textUtf16,
                          char *out, size_t cap) {
    char t8[512];
    WideCharToMultiByte(CP_UTF8, 0, textUtf16, -1, t8, sizeof(t8), NULL, NULL);
    char *t = cdp_json_escape(t8);
    if (!t) return 0;
    /* 生成 JS：按 innerText 精确 / 包含匹配，跨同源 iframe */
    char expr2[4096];
    _snprintf(expr2, sizeof(expr2),
        "(function(){var T=\"%s\";"
        "function vis(e){try{return e.offsetParent!==null||e.getClientRects().length>0}catch(x){return false}}"
        "function roots(){var r=[document];try{document.querySelectorAll('iframe').forEach(function(f){"
        "try{if(f.contentDocument)r.push(f.contentDocument)}catch(e){}})}catch(e){}return r}"
        "function tx(e){return (e.innerText||e.textContent||'').trim()}"
        "for(var k=0;k<roots().length;k++){var d=roots()[k];"
        "var all=[].slice.call(d.querySelectorAll('button,a,span,div,li,[role=button],[class*=btn]'));"
        "var hit=all.filter(function(e){return vis(e)&&tx(e)===T});"
        "if(!hit.length)hit=all.filter(function(e){return vis(e)&&tx(e).indexOf(T)>=0});"
        "if(hit.length){hit.sort(function(a,b){return tx(a).length-tx(b).length});"
        "hit[0].click();return 'OK'}}return 'NOT_FOUND'})()", t);
    char *e = cdp_json_escape(expr2);
    free(t);
    if (!e) return 0;
    size_t pn = strlen(e) + 256;
    char *params = (char *)malloc(pn);
    if (!params) { free(e); return 0; }
    _snprintf(params, pn,
        "{\"expression\":\"%s\",\"returnByValue\":true,\"userGesture\":true}", e);
    char *r = cdp_call(s, id, "Runtime.evaluate", params);
    free(params); free(e);
    if (!r) return 0;
    int ok = cdp_json_str(r, "value", out, cap);
    free(r);
    return ok;
}

/* ---- 探测页面：列出可见按钮文字、file input 数量、URL ---- */
static char *cdp_probe(SOCKET s, int *id) {
    const char *js =
        "(function(){"
        "function vis(e){try{return e.offsetParent!==null||e.getClientRects().length>0}catch(x){return false}}"
        "var seen={},out=[];"
        "document.querySelectorAll('button,a,span,div,li,[role=button]').forEach(function(e){"
        "if(!vis(e))return;var t=(e.innerText||'').trim();"
        "if(!t||t.length>18||seen[t])return;seen[t]=1;out.push(t);"
        "if(out.length>=80)return});"
        "var fi=[];document.querySelectorAll('input[type=file]').forEach(function(x){"
        "fi.push((x.accept||'')+'|'+(x.multiple?'multi':'single'))});"
        "var ifr=[];document.querySelectorAll('iframe').forEach(function(x){ifr.push(x.src||'(no-src)')});"
        "return JSON.stringify({url:location.href,title:document.title,"
        "buttons:out,fileInputs:fi,iframes:ifr,"
        "bodyHead:(document.body.innerText||'').slice(0,200)})})()";
    char buf[16384];
    if (!cdp_eval(s, id, js, buf, sizeof(buf))) return NULL;
    char *r = (char *)malloc(strlen(buf) + 1);
    if (r) strcpy(r, buf);
    return r;
}

#endif /* QB_CDP_H */
