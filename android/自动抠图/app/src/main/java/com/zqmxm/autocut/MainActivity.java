package com.zqmxm.autocut;

import android.Manifest;
import android.app.Activity;
import android.content.ContentValues;
import android.content.Intent;
import android.content.pm.PackageManager;
import android.graphics.Bitmap;
import android.graphics.Canvas;
import android.graphics.LinearGradient;
import android.graphics.Matrix;
import android.graphics.Paint;
import android.graphics.Shader;
import android.graphics.BitmapFactory;
import android.net.Uri;
import android.os.Build;
import android.os.Bundle;
import android.os.Environment;
import android.provider.MediaStore;
import android.view.Gravity;
import android.view.MotionEvent;
import android.view.View;
import android.widget.Button;
import android.widget.ImageView;
import android.widget.LinearLayout;
import android.widget.ProgressBar;
import android.widget.ScrollView;
import android.widget.SeekBar;
import android.widget.TextView;
import android.widget.Toast;

import java.io.File;
import java.io.FileOutputStream;
import java.io.InputStream;
import java.io.OutputStream;
import java.text.SimpleDateFormat;
import java.util.ArrayDeque;
import java.util.Date;
import java.util.Locale;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;

/**
 * 自动抠图的主页面。
 *
 * 用法：选一张图 → 一键自动抠图 →（不满意就开手动补擦用手指涂）→ 换背景 → 保存。
 * 界面全部用代码搭，不写布局 XML，也不引第三方库，保证仓库的 CI 一定能编过。
 * 所有透明度都存在 alpha 数组里，保存时才合成成真正的透明 PNG。
 */
public class MainActivity extends Activity {

    private static final int REQ_PICK_IMAGE = 1001;
    private static final int REQ_WRITE_STORAGE = 2001;
    private static final int MAX_SIDE = 1000;  // 手机里够用了，也避免大图把内存撑爆

    private ImageView preview;
    private TextView status;
    private ProgressBar progress;
    private SeekBar toleranceBar;
    private Button eraseButton;
    private Button backgroundButton;

    private Bitmap sourceBitmap;
    private Bitmap previewBitmap;
    private Bitmap foregroundBitmap;
    private int[] previewPixels;
    private int[] rgb;
    private int[] alpha;
    private int width;
    private int height;

    private int backgroundMode = 0;   // 0 棋盘格 / 1 白 / 2 红 / 3 蓝 / 4 渐变
    private boolean eraseMode = false;
    private boolean pendingSaveWithBackground = false;
    private final ArrayDeque<int[]> undoStack = new ArrayDeque<>();
    private final ExecutorService worker = Executors.newSingleThreadExecutor();

    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        setContentView(buildUi());
        status.setText("第一步：点「选一张图片」，最好挑纯色背景的照片");
    }

    /** 用代码把整个页面搭出来，注释和文案全部中文。 */
    private View buildUi() {
        ScrollView scroll = new ScrollView(this);
        scroll.setFillViewport(true);
        LinearLayout column = new LinearLayout(this);
        column.setOrientation(LinearLayout.VERTICAL);
        column.setPadding(dp(12), dp(12), dp(12), dp(16));
        scroll.addView(column, new ScrollView.LayoutParams(
                ScrollView.LayoutParams.MATCH_PARENT, ScrollView.LayoutParams.WRAP_CONTENT));

        TextView title = new TextView(this);
        title.setText("自动抠图");
        title.setTextSize(24);
        title.setGravity(Gravity.CENTER);
        title.setTextColor(0xFF212121);
        column.addView(title);

        TextView intro = new TextView(this);
        intro.setText("选照片 → 一键抠掉背景 → 保存透明 PNG。抠不干净的地方可以用手指补擦。");
        intro.setTextSize(13);
        intro.setGravity(Gravity.CENTER);
        intro.setPadding(0, dp(4), 0, dp(8));
        column.addView(intro);

        status = new TextView(this);
        status.setTextSize(13);
        status.setTextColor(0xFF37474F);
        status.setPadding(0, dp(4), 0, dp(4));
        column.addView(status);

        progress = new ProgressBar(this, null, android.R.attr.progressBarStyleHorizontal);
        progress.setIndeterminate(true);
        progress.setVisibility(View.GONE);
        column.addView(progress);

        LinearLayout row1 = row();
        Button pickButton = button("选一张图片", row1);
        Button autoButton = button("一键自动抠图", row1);
        column.addView(row1);

        TextView toleranceLabel = new TextView(this);
        toleranceLabel.setText("抠图力度（往右拉擦得更多，擦过头就拉回来重抠）");
        toleranceLabel.setTextSize(12);
        toleranceLabel.setPadding(0, dp(8), 0, 0);
        column.addView(toleranceLabel);
        toleranceBar = new SeekBar(this);
        toleranceBar.setMax(100);
        toleranceBar.setProgress(45);  // 对应容差 70
        column.addView(toleranceBar);

        LinearLayout row2 = row();
        eraseButton = button("手动补擦：关", row2);
        Button undoButton = button("撤销", row2);
        Button resetButton = button("恢复原图", row2);
        column.addView(row2);

        LinearLayout row3 = row();
        backgroundButton = button("换背景：棋盘", row3);
        Button saveTransparentButton = button("存透明 PNG", row3);
        Button saveBackgroundButton = button("存带背景图", row3);
        column.addView(row3);

        preview = new ImageView(this);
        preview.setScaleType(ImageView.ScaleType.FIT_CENTER);
        preview.setBackgroundColor(0xFFDDDDDD);
        preview.setLayoutParams(new LinearLayout.LayoutParams(
                LinearLayout.LayoutParams.MATCH_PARENT, dp(340)));
        column.addView(preview);

        TextView tips = new TextView(this);
        tips.setTextSize(12);
        tips.setPadding(0, dp(10), 0, 0);
        tips.setText("说明：\n"
                + "· 灰白棋盘格 = 已经被抠成透明的地方。\n"
                + "· 纯色 / 近纯色背景（证件照、白底商品图、截图）效果最好。\n"
                + "· 复杂背景和头发丝扣不干净，先开「手动补擦」用手指涂一涂。\n"
                + "· 保存后在相册的「Pictures / 自动抠图」文件夹里找。\n"
                + "· 本版本是零依赖的轻量算法，不含 AI 模型；想升级成发丝级人像分割，见 README。");
        column.addView(tips);

        pickButton.setOnClickListener(v -> pickImage());
        autoButton.setOnClickListener(v -> runAutoCut());
        undoButton.setOnClickListener(v -> undo());
        resetButton.setOnClickListener(v -> reset());
        eraseButton.setOnClickListener(v -> toggleErase());
        backgroundButton.setOnClickListener(v -> nextBackground());
        saveTransparentButton.setOnClickListener(v -> save(false));
        saveBackgroundButton.setOnClickListener(v -> save(true));
        preview.setOnTouchListener(this::onPreviewTouch);

        return scroll;
    }

    private LinearLayout row() {
        LinearLayout r = new LinearLayout(this);
        r.setOrientation(LinearLayout.HORIZONTAL);
        r.setLayoutParams(new LinearLayout.LayoutParams(
                LinearLayout.LayoutParams.MATCH_PARENT, LinearLayout.LayoutParams.WRAP_CONTENT));
        return r;
    }

    private Button button(String text, LinearLayout parent) {
        Button b = new Button(this);
        b.setText(text);
        b.setTextSize(13);
        LinearLayout.LayoutParams lp = new LinearLayout.LayoutParams(0,
                LinearLayout.LayoutParams.WRAP_CONTENT, 1f);
        lp.setMargins(dp(2), dp(2), dp(2), dp(2));
        b.setLayoutParams(lp);
        parent.addView(b);
        return b;
    }

    private int dp(int value) {
        return Math.round(value * getResources().getDisplayMetrics().density);
    }

    /** 打开系统相册选图；用系统选择器拿到的 Uri 自带临时读取授权，不用申请读权限。 */
    private void pickImage() {
        Intent intent = new Intent(Intent.ACTION_OPEN_DOCUMENT);
        intent.addCategory(Intent.CATEGORY_OPENABLE);
        intent.setType("image/*");
        intent.addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION);
        startActivityForResult(intent, REQ_PICK_IMAGE);
    }

    @Override
    protected void onActivityResult(int requestCode, int resultCode, Intent data) {
        super.onActivityResult(requestCode, resultCode, data);
        if (requestCode != REQ_PICK_IMAGE || resultCode != RESULT_OK || data == null
                || data.getData() == null) {
            return;
        }
        try {
            Bitmap bmp = loadBitmap(data.getData());
            if (sourceBitmap != null && sourceBitmap != bmp) {
                sourceBitmap.recycle();
            }
            sourceBitmap = bmp;
            width = bmp.getWidth();
            height = bmp.getHeight();
            rgb = new int[width * height];
            bmp.getPixels(rgb, 0, width, 0, 0, width, height);
            for (int i = 0; i < rgb.length; i++) {
                rgb[i] |= 0xFF000000;  // 源图可能有 alpha，这里统一当成不透明的普通照片
            }
            alpha = CutCore.opaque(rgb.length);
            undoStack.clear();
            previewBitmap = null;
            foregroundBitmap = null;
            previewPixels = null;
            refreshPreview();
            status.setText("已选图：" + width + " × " + height + "，点「一键自动抠图」开始");
        } catch (Exception e) {
            toast("读图失败：" + e.getMessage());
        }
    }

    /** 分两次解码控制内存，再按 EXIF 摆正方向、缩到最长边 1000。 */
    private Bitmap loadBitmap(Uri uri) throws Exception {
        BitmapFactory.Options bounds = new BitmapFactory.Options();
        bounds.inJustDecodeBounds = true;
        try (InputStream in = getContentResolver().openInputStream(uri)) {
            BitmapFactory.decodeStream(in, null, bounds);
        }
        int sample = 1;
        while (Math.max(bounds.outWidth, bounds.outHeight) / sample > MAX_SIDE * 2) {
            sample *= 2;
        }
        BitmapFactory.Options opts = new BitmapFactory.Options();
        opts.inSampleSize = sample;
        opts.inPreferredConfig = Bitmap.Config.ARGB_8888;
        Bitmap bmp;
        try (InputStream in = getContentResolver().openInputStream(uri)) {
            bmp = BitmapFactory.decodeStream(in, null, opts);
        }
        if (bmp == null) {
            throw new Exception("这张图片读不出来，换一张试试");
        }
        int rotation = readRotation(uri);
        if (rotation != 0) {
            Matrix matrix = new Matrix();
            matrix.postRotate(rotation);
            Bitmap rotated = Bitmap.createBitmap(bmp, 0, 0, bmp.getWidth(), bmp.getHeight(), matrix, true);
            bmp.recycle();
            bmp = rotated;
        }
        float scale = Math.min(1f, (float) MAX_SIDE / Math.max(bmp.getWidth(), bmp.getHeight()));
        if (scale < 1f) {
            Bitmap scaled = Bitmap.createScaledBitmap(bmp,
                    Math.round(bmp.getWidth() * scale), Math.round(bmp.getHeight() * scale), true);
            bmp.recycle();
            bmp = scaled;
        }
        return bmp;
    }

    /** 读照片的 EXIF 方向；读不到就算了，不影响抠图。 */
    private int readRotation(Uri uri) {
        try (InputStream in = getContentResolver().openInputStream(uri)) {
            if (in == null) {
                return 0;
            }
            android.media.ExifInterface exif = new android.media.ExifInterface(in);
            int o = exif.getAttributeInt(android.media.ExifInterface.TAG_ORIENTATION,
                    android.media.ExifInterface.ORIENTATION_NORMAL);
            if (o == android.media.ExifInterface.ORIENTATION_ROTATE_90) {
                return 90;
            }
            if (o == android.media.ExifInterface.ORIENTATION_ROTATE_180) {
                return 180;
            }
            if (o == android.media.ExifInterface.ORIENTATION_ROTATE_270) {
                return 270;
            }
        } catch (Exception ignored) {
            // 有些相册格式不带 EXIF，忽略即可
        }
        return 0;
    }

    /** 一键自动抠图放到后台线程跑，算完回主线程刷新预览。 */
    private void runAutoCut() {
        if (sourceBitmap == null) {
            toast("请先点「选一张图片」");
            return;
        }
        final int tolerance = 25 + toleranceBar.getProgress();
        final int[] input = rgb.clone();
        final int w = width;
        final int h = height;
        pushUndo();
        setBusy(true, "正在自动抠图…");
        worker.execute(() -> {
            try {
                long start = System.currentTimeMillis();
                int[] result = CutCore.autoCut(input, w, h, tolerance);
                long cost = System.currentTimeMillis() - start;
                int purity = CutCore.borderPurity(input, w, h);
                runOnUiThread(() -> {
                    alpha = result;
                    refreshPreview();
                    String hint = purity < 35
                            ? "背景颜色比较杂，自动效果可能一般，建议开「手动补擦」修一下"
                            : "抠好了，透明的地方显示成灰白棋盘格";
                    setBusy(false, "完成（用了 " + cost + " 毫秒），" + hint);
                });
            } catch (Exception e) {
                runOnUiThread(() -> setBusy(false, "抠图失败：" + e.getMessage()));
            }
        });
    }

    private void setBusy(boolean busy, String text) {
        progress.setVisibility(busy ? View.VISIBLE : View.GONE);
        status.setText(text);
    }

    /** 手动补擦：把手指位置换算成图片像素坐标，交给算法核心擦圆。 */
    private boolean onPreviewTouch(View v, MotionEvent event) {
        if (!eraseMode || sourceBitmap == null) {
            return false;
        }
        int action = event.getActionMasked();
        if (action != MotionEvent.ACTION_DOWN && action != MotionEvent.ACTION_MOVE
                && action != MotionEvent.ACTION_UP) {
            return false;
        }
        if (action == MotionEvent.ACTION_DOWN && v.getParent() != null) {
            v.getParent().requestDisallowInterceptTouchEvent(true);
        }
        float viewW = v.getWidth();
        float viewH = v.getHeight();
        float scale = Math.min(viewW / (float) width, viewH / (float) height);
        float left = (viewW - width * scale) / 2f;
        float top = (viewH - height * scale) / 2f;
        float bx = (event.getX() - left) / scale;
        float by = (event.getY() - top) / scale;
        if (bx < 0 || by < 0 || bx >= width || by >= height) {
            return true;
        }
        if (action == MotionEvent.ACTION_DOWN) {
            pushUndo();
        }
        float radius = Math.max(10f, Math.min(width, height) * 0.035f);
        CutCore.eraseCircle(alpha, width, height, bx, by, radius, 0.2f);
        refreshPreview();
        return true;
    }

    private void toggleErase() {
        if (sourceBitmap == null) {
            toast("请先选一张图片");
            return;
        }
        eraseMode = !eraseMode;
        eraseButton.setText(eraseMode ? "手动补擦：开" : "手动补擦：关");
        status.setText(eraseMode
                ? "补擦已打开：手指在图片上涂抹，把没抠干净的地方擦成透明"
                : "补擦已关闭");
    }

    private void nextBackground() {
        backgroundMode = (backgroundMode + 1) % 5;
        String[] names = {"棋盘", "纯白", "红色", "蓝色", "渐变"};
        backgroundButton.setText("换背景：" + names[backgroundMode]);
        refreshPreview();
    }

    private void pushUndo() {
        if (alpha == null) {
            return;
        }
        if (undoStack.size() >= 8) {
            undoStack.removeFirst();
        }
        undoStack.addLast(alpha.clone());
    }

    private void undo() {
        if (undoStack.isEmpty()) {
            toast("没有可以撤销的操作");
            return;
        }
        alpha = undoStack.removeLast();
        refreshPreview();
        status.setText("已撤销上一步");
    }

    private void reset() {
        if (sourceBitmap == null) {
            toast("请先选一张图片");
            return;
        }
        pushUndo();
        alpha = CutCore.opaque(rgb.length);
        refreshPreview();
        status.setText("已恢复成原图，可以重新自动抠图");
    }

    /** 把 alpha 数组合到当前背景上刷新预览；复用 Bitmap，手指涂抹时不卡。 */
    private void refreshPreview() {
        if (sourceBitmap == null) {
            return;
        }
        if (previewBitmap == null || previewBitmap.getWidth() != width
                || previewBitmap.getHeight() != height) {
            previewBitmap = Bitmap.createBitmap(width, height, Bitmap.Config.ARGB_8888);
            foregroundBitmap = Bitmap.createBitmap(width, height, Bitmap.Config.ARGB_8888);
            previewPixels = new int[width * height];
            preview.setImageBitmap(previewBitmap);
        }
        for (int i = 0; i < previewPixels.length; i++) {
            previewPixels[i] = (alpha[i] << 24) | (rgb[i] & 0x00FFFFFF);
        }
        foregroundBitmap.setPixels(previewPixels, 0, width, 0, 0, width, height);
        Canvas canvas = new Canvas(previewBitmap);
        drawBackground(canvas, width, height);
        canvas.drawBitmap(foregroundBitmap, 0, 0, null);
        preview.invalidate();
    }

    private void drawBackground(Canvas canvas, int w, int h) {
        switch (backgroundMode) {
            case 1:
                canvas.drawColor(0xFFFFFFFF);
                return;
            case 2:
                canvas.drawColor(0xFFD32F2F);
                return;
            case 3:
                canvas.drawColor(0xFF1976D2);
                return;
            case 4:
                Paint gradient = new Paint();
                gradient.setShader(new LinearGradient(0, 0, w, h,
                        0xFF7B1FA2, 0xFF03A9F4, Shader.TileMode.CLAMP));
                canvas.drawRect(0, 0, w, h, gradient);
                return;
            case 0:
            default:
                int cell = Math.max(8, Math.min(w, h) / 40);
                Paint paint = new Paint();
                for (int y = 0; y < h; y += cell) {
                    for (int x = 0; x < w; x += cell) {
                        paint.setColor(((x / cell + y / cell) % 2 == 0) ? 0xFFE8E8E8 : 0xFFFFFFFF);
                        canvas.drawRect(x, y, x + cell, y + cell, paint);
                    }
                }
        }
    }

    /** 生成要保存的图：不带背景时是真透明 PNG，带背景时把底色画在下面。 */
    private Bitmap buildResult(boolean withBackground) {
        int[] pixels = new int[width * height];
        for (int i = 0; i < pixels.length; i++) {
            pixels[i] = (alpha[i] << 24) | (rgb[i] & 0x00FFFFFF);
        }
        Bitmap fg = Bitmap.createBitmap(width, height, Bitmap.Config.ARGB_8888);
        fg.setPixels(pixels, 0, width, 0, 0, width, height);
        if (!withBackground) {
            return fg;
        }
        Bitmap out = Bitmap.createBitmap(width, height, Bitmap.Config.ARGB_8888);
        Canvas canvas = new Canvas(out);
        if (backgroundMode == 0) {
            canvas.drawColor(0xFFFFFFFF);  // 棋盘格只是预览标记，存图时自动换成白底
        } else {
            drawBackground(canvas, width, height);
        }
        canvas.drawBitmap(fg, 0, 0, null);
        return out;
    }

    /** 保存到手机相册的「Pictures / 自动抠图」文件夹，格式固定 PNG（保住透明通道）。 */
    private void save(boolean withBackground) {
        if (sourceBitmap == null) {
            toast("请先选一张图片并抠图");
            return;
        }
        if (Build.VERSION.SDK_INT < 29
                && checkSelfPermission(Manifest.permission.WRITE_EXTERNAL_STORAGE)
                != PackageManager.PERMISSION_GRANTED) {
            pendingSaveWithBackground = withBackground;
            requestPermissions(new String[]{Manifest.permission.WRITE_EXTERNAL_STORAGE},
                    REQ_WRITE_STORAGE);
            return;
        }
        try {
            Bitmap result = buildResult(withBackground);
            String name = "自动抠图_" + stamp();
            if (Build.VERSION.SDK_INT >= 29) {
                ContentValues values = new ContentValues();
                values.put(MediaStore.Images.Media.DISPLAY_NAME, name + ".png");
                values.put(MediaStore.Images.Media.MIME_TYPE, "image/png");
                values.put(MediaStore.Images.Media.RELATIVE_PATH, "Pictures/自动抠图");
                values.put(MediaStore.Images.Media.IS_PENDING, 1);
                Uri uri = getContentResolver().insert(
                        MediaStore.Images.Media.EXTERNAL_CONTENT_URI, values);
                if (uri == null) {
                    throw new Exception("相册不允许写入");
                }
                try (OutputStream os = getContentResolver().openOutputStream(uri)) {
                    if (os == null || !result.compress(Bitmap.CompressFormat.PNG, 100, os)) {
                        throw new Exception("PNG 保存失败");
                    }
                }
                values.clear();
                values.put(MediaStore.Images.Media.IS_PENDING, 0);
                getContentResolver().update(uri, values, null, null);
            } else {
                File dir = new File(Environment.getExternalStoragePublicDirectory(
                        Environment.DIRECTORY_PICTURES), "自动抠图");
                if (!dir.exists() && !dir.mkdirs()) {
                    throw new Exception("建相册文件夹失败");
                }
                File file = new File(dir, name + ".png");
                try (FileOutputStream os = new FileOutputStream(file)) {
                    if (!result.compress(Bitmap.CompressFormat.PNG, 100, os)) {
                        throw new Exception("PNG 保存失败");
                    }
                }
                sendBroadcast(new Intent(Intent.ACTION_MEDIA_SCANNER_SCAN_FILE, Uri.fromFile(file)));
            }
            toast("已保存到相册：Pictures / 自动抠图 / " + name + ".png");
            status.setText("保存成功：" + name + ".png");
        } catch (Exception e) {
            toast("保存失败：" + e.getMessage());
        }
    }

    @Override
    public void onRequestPermissionsResult(int requestCode, String[] permissions, int[] grantResults) {
        super.onRequestPermissionsResult(requestCode, permissions, grantResults);
        if (requestCode == REQ_WRITE_STORAGE) {
            if (grantResults.length > 0 && grantResults[0] == PackageManager.PERMISSION_GRANTED) {
                save(pendingSaveWithBackground);
            } else {
                toast("没给存储权限，存不了图");
            }
        }
    }

    private String stamp() {
        return new SimpleDateFormat("yyyyMMdd_HHmmss", Locale.CHINA).format(new Date());
    }

    private void toast(String message) {
        Toast.makeText(this, message, Toast.LENGTH_SHORT).show();
    }

    @Override
    protected void onDestroy() {
        super.onDestroy();
        worker.shutdownNow();
    }
}
