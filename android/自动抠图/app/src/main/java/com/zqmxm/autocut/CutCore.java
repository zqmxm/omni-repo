package com.zqmxm.autocut;

import java.util.Arrays;

/**
 * 自动抠图的算法核心：纯 Java，不引用任何 Android 类，所以在电脑上装个 JDK 就能跑自检。
 *
 * 思路（三步）：
 * 1. 取图片最外圈一圈像素投票，估计「背景参考色」；
 * 2. 把和背景色接近的像素标成「疑似背景」，再从四条边出发做洪水填充，
 *    只有能一路连到图片边缘的疑似背景才真的擦掉——这样主体内部
 *    哪怕有一块颜色和背景一样，也不会被误挖出一个洞；
 * 3. 按颜色距离算软边透明度，再做一次 3x3 均值模糊，让边缘不发锯齿。
 *
 * 边界（如实说）：这是零第三方依赖的轻量方案，对纯色 / 近纯色背景
 * （证件照、白底商品图、截图）效果最好。复杂背景、发丝级人像需要
 * 端侧分割模型；只要把模型输出的 alpha 数组替换本类的返回值即可，界面不用改。
 */
public final class CutCore {

    private CutCore() {
    }

    /**
     * 一键自动抠图。
     *
     * @param rgb        长度为 宽*高 的 0xAARRGGBB 颜色数组（alpha 会被忽略）
     * @param width      图片宽
     * @param height     图片高
     * @param tolerance  抠图力度，越大擦得越多，建议 25~125
     * @return 长度 宽*高 的透明度数组，0 = 全透明，255 = 完全不透明
     */
    public static int[] autoCut(int[] rgb, int width, int height, int tolerance) {
        if (rgb == null || width <= 0 || height <= 0 || rgb.length < width * height) {
            throw new IllegalArgumentException("图片数据不合法");
        }
        int tol = Math.max(10, Math.min(160, tolerance));
        int n = width * height;

        int[] bg = estimateBackground(rgb, width, height);
        int br = bg[0];
        int bgc = bg[1];
        int bb = bg[2];

        int t0 = (int) (tol * 0.45f);  // 距离小于它：肯定是背景，直接透明
        int t1 = tol;                  // 距离大于它：肯定是主体，保留
        int t1sq = t1 * t1;

        boolean[] maybeBg = new boolean[n];
        int[] distSq = new int[n];
        for (int i = 0; i < n; i++) {
            int c = rgb[i];
            int dr = ((c >> 16) & 0xFF) - br;
            int dg = ((c >> 8) & 0xFF) - bgc;
            int db = (c & 0xFF) - bb;
            // 按人眼亮度权重（约 0.30 / 0.59 / 0.11）算加权平方距离
            int v = (30 * dr * dr + 59 * dg * dg + 11 * db * db) / 100;
            distSq[i] = v;
            maybeBg[i] = v <= t1sq;
        }

        boolean[] reachable = floodFillFromBorder(maybeBg, width, height);

        int[] alpha = new int[n];
        for (int i = 0; i < n; i++) {
            if (maybeBg[i] && reachable[i]) {
                double d = Math.sqrt(distSq[i]);
                double a = (d - t0) / (double) Math.max(1, t1 - t0) * 255.0;
                alpha[i] = (int) Math.max(0, Math.min(255, a));
            } else {
                alpha[i] = 255;
            }
        }
        return blurAlpha(alpha, width, height);
    }

    /** 生成「全部不透明」的透明度数组，用于选图后的初始状态和重置。 */
    public static int[] opaque(int size) {
        int[] a = new int[size];
        Arrays.fill(a, 255);
        return a;
    }

    /**
     * 手动补擦：以 (cx, cy) 为圆心把一片圆形区域的透明度降下来，边缘带柔化。
     * 擦除 = 把 alpha 调小，不是涂成白色，所以保存成 PNG 后是真透明。
     */
    public static void eraseCircle(int[] alpha, int width, int height,
                                   float cx, float cy, float radius, float feather) {
        if (alpha == null || radius <= 0 || alpha.length < width * height) {
            return;
        }
        float soft = Math.max(0f, Math.min(0.9f, feather)) * radius;
        float core = radius - soft;
        float r2 = radius * radius;
        int x0 = Math.max(0, (int) Math.floor(cx - radius));
        int x1 = Math.min(width - 1, (int) Math.ceil(cx + radius));
        int y0 = Math.max(0, (int) Math.floor(cy - radius));
        int y1 = Math.min(height - 1, (int) Math.ceil(cy + radius));

        for (int y = y0; y <= y1; y++) {
            float dy = y - cy;
            for (int x = x0; x <= x1; x++) {
                float dx = x - cx;
                float d2 = dx * dx + dy * dy;
                if (d2 > r2) {
                    continue;
                }
                int idx = y * width + x;
                int old = alpha[idx];
                if (old == 0) {
                    continue;
                }
                float next;
                if (soft <= 0 || d2 <= core * core) {
                    next = 0;
                } else {
                    float d = (float) Math.sqrt(d2);
                    next = old * (radius - d) / soft;
                }
                alpha[idx] = (int) Math.max(0, Math.min(old, next));
            }
        }
    }

    /**
     * 背景「纯不纯」，返回 0~100。数值低说明四边颜色很杂，
     * 自动抠图效果可能一般，界面会提示用户开手动补擦。
     */
    public static int borderPurity(int[] rgb, int width, int height) {
        if (rgb == null || width <= 0 || height <= 0 || rgb.length < width * height) {
            return 0;
        }
        return estimateBackground(rgb, width, height)[3];
    }

    /** 估计背景色：边框像素按每通道 16 级量化后投票，返回 {红, 绿, 蓝, 纯度}。 */
    private static int[] estimateBackground(int[] rgb, int width, int height) {
        int border = Math.max(2, Math.min(width, height) / 40);
        int bins = 16 * 16 * 16;
        int[] count = new int[bins];
        int[] sumR = new int[bins];
        int[] sumG = new int[bins];
        int[] sumB = new int[bins];
        int total = 0;

        for (int y = 0; y < height; y++) {
            boolean edgeY = y < border || y >= height - border;
            for (int x = 0; x < width; x++) {
                if (!edgeY && x >= border && x < width - border) {
                    continue;
                }
                int c = rgb[y * width + x];
                int r = (c >> 16) & 0xFF;
                int g = (c >> 8) & 0xFF;
                int b = c & 0xFF;
                int key = ((r >> 4) << 8) | ((g >> 4) << 4) | (b >> 4);
                count[key]++;
                sumR[key] += r;
                sumG[key] += g;
                sumB[key] += b;
                total++;
            }
        }

        int best = 0;
        for (int i = 1; i < bins; i++) {
            if (count[i] > count[best]) {
                best = i;
            }
        }
        if (total == 0 || count[best] == 0) {
            return new int[]{255, 255, 255, 0};
        }
        int purity = count[best] * 100 / total;
        return new int[]{
                sumR[best] / count[best],
                sumG[best] / count[best],
                sumB[best] / count[best],
                purity
        };
    }

    /** 从四条边出发做四邻域洪水填充，标记「和边缘连通」的疑似背景。 */
    private static boolean[] floodFillFromBorder(boolean[] maybeBg, int width, int height) {
        int n = width * height;
        boolean[] seen = new boolean[n];
        int[] queue = new int[n];
        int head = 0;
        int tail = 0;

        for (int x = 0; x < width; x++) {
            int top = x;
            int bottom = (height - 1) * width + x;
            if (maybeBg[top] && !seen[top]) {
                seen[top] = true;
                queue[tail++] = top;
            }
            if (maybeBg[bottom] && !seen[bottom]) {
                seen[bottom] = true;
                queue[tail++] = bottom;
            }
        }
        for (int y = 0; y < height; y++) {
            int left = y * width;
            int right = y * width + width - 1;
            if (maybeBg[left] && !seen[left]) {
                seen[left] = true;
                queue[tail++] = left;
            }
            if (maybeBg[right] && !seen[right]) {
                seen[right] = true;
                queue[tail++] = right;
            }
        }

        while (head < tail) {
            int p = queue[head++];
            int x = p % width;
            int y = p / width;
            if (x > 0) {
                int q = p - 1;
                if (maybeBg[q] && !seen[q]) {
                    seen[q] = true;
                    queue[tail++] = q;
                }
            }
            if (x < width - 1) {
                int q = p + 1;
                if (maybeBg[q] && !seen[q]) {
                    seen[q] = true;
                    queue[tail++] = q;
                }
            }
            if (y > 0) {
                int q = p - width;
                if (maybeBg[q] && !seen[q]) {
                    seen[q] = true;
                    queue[tail++] = q;
                }
            }
            if (y < height - 1) {
                int q = p + width;
                if (maybeBg[q] && !seen[q]) {
                    seen[q] = true;
                    queue[tail++] = q;
                }
            }
        }
        return seen;
    }

    /** 3x3 均值模糊透明度数组，让抠图边缘更自然。 */
    private static int[] blurAlpha(int[] alpha, int width, int height) {
        int[] out = new int[alpha.length];
        for (int y = 0; y < height; y++) {
            for (int x = 0; x < width; x++) {
                int sum = 0;
                int cnt = 0;
                for (int dy = -1; dy <= 1; dy++) {
                    int yy = y + dy;
                    if (yy < 0 || yy >= height) {
                        continue;
                    }
                    for (int dx = -1; dx <= 1; dx++) {
                        int xx = x + dx;
                        if (xx < 0 || xx >= width) {
                            continue;
                        }
                        sum += alpha[yy * width + xx];
                        cnt++;
                    }
                }
                out[y * width + x] = sum / Math.max(1, cnt);
            }
        }
        return out;
    }
}
