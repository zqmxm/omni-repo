package com.zqmxm.autocut;

import java.util.Arrays;

/**
 * 抠图算法自检：不依赖 JUnit、不依赖 Android SDK，装了 JDK 17 就能直接跑。
 * 断言失败会抛 AssertionError，让 CI 立刻变红，不假装成功。
 */
public class CutCoreSelfTest {

    public static void main(String[] args) {
        testPureBackground();
        testKeepInnerSameColor();
        testEraseCircle();
        testTolerance();
        testAllSameColor();
        System.out.println("自检通过：纯色背景 / 主体内部保护 / 手动补擦 / 容差 / 整图容错，全部 OK");
    }

    /** 白底 + 红方块：四角要透明，方块要留下。 */
    private static void testPureBackground() {
        int w = 80;
        int h = 80;
        int[] rgb = makeSolid(w, h, 0xFFFFFFFF);
        for (int y = 25; y < 55; y++) {
            for (int x = 25; x < 55; x++) {
                rgb[y * w + x] = 0xFFE53935;
            }
        }
        int[] a = CutCore.autoCut(rgb, w, h, 70);
        check(a[2 * w + 2] <= 5, "白底四角应被擦成透明");
        check(a[40 * w + 40] >= 250, "中间红色主体应保持不透明");
        check(CutCore.borderPurity(rgb, w, h) >= 80, "纯白背景的纯度应该很高");
    }

    /** 主体内部有一块和背景同色的点：因为连不到图片边缘，不能被误挖掉。 */
    private static void testKeepInnerSameColor() {
        int w = 80;
        int h = 80;
        int[] rgb = makeSolid(w, h, 0xFFFFFFFF);
        for (int y = 20; y < 60; y++) {
            for (int x = 20; x < 60; x++) {
                rgb[y * w + x] = 0xFF1E88E5;
            }
        }
        for (int y = 36; y < 44; y++) {
            for (int x = 36; x < 44; x++) {
                rgb[y * w + x] = 0xFFFFFFFF;
            }
        }
        int[] a = CutCore.autoCut(rgb, w, h, 70);
        check(a[40 * w + 40] >= 250, "主体内部的同色点不能被当成背景擦掉");
        check(a[2 * w + 2] <= 5, "外部白底仍应被擦掉");
    }

    /** 手动补擦只影响笔刷附近，远处保持原样。 */
    private static void testEraseCircle() {
        int side = 100;
        int[] a = CutCore.opaque(side * side);
        CutCore.eraseCircle(a, side, side, 50, 50, 12, 0.2f);
        check(a[50 * side + 50] == 0, "笔刷圆心应擦成透明");
        check(a[90 * side + 90] == 255, "远离笔刷的地方不应受影响");
    }

    /** 容差越大，保留下来的不透明度总和不应变多。 */
    private static void testTolerance() {
        int w = 60;
        int h = 60;
        int[] rgb = makeSolid(w, h, 0xFFFFFFFF);
        for (int y = 20; y < 40; y++) {
            for (int x = 20; x < 40; x++) {
                rgb[y * w + x] = 0xFFF0F0F0;  // 略灰，和白底有一点点距离
            }
        }
        long low = sum(CutCore.autoCut(rgb, w, h, 30));
        long high = sum(CutCore.autoCut(rgb, w, h, 120));
        check(high <= low, "抠图力度调大后，擦除范围不应反而变小");
    }

    /** 整张图都是同一个颜色：不能崩溃，也不能数组越界。 */
    private static void testAllSameColor() {
        int[] rgb = makeSolid(40, 40, 0xFFFFFFFF);
        int[] a = CutCore.autoCut(rgb, 40, 40, 70);
        check(a.length == 1600, "整图同色也要正常返回透明度数组");
    }

    private static int[] makeSolid(int w, int h, int color) {
        int[] rgb = new int[w * h];
        Arrays.fill(rgb, color);
        return rgb;
    }

    private static long sum(int[] a) {
        long s = 0;
        for (int v : a) {
            s += v;
        }
        return s;
    }

    private static void check(boolean ok, String msg) {
        if (!ok) {
            throw new AssertionError("自检失败：" + msg);
        }
    }
}
