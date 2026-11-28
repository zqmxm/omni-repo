# 自动抠图

一个装在安卓手机上的「一键抠图」小工具：选一张照片 → 自动把背景擦成透明 → 保存透明 PNG，也可以换个底色再保存。没抠干净的地方，用手指涂一涂就能补擦。

- 软件本名：**自动抠图**
- 所在目录：`android/自动抠图/`
- 安装包：**自动抠图.apk**（由 GitHub Actions 自动打出，按仓库规矩不存进仓库）
- 当前版本：见同目录 `VERSION`，自动备份时会自动 +1

---

## 一、手机上怎么用

1. 打开「自动抠图」，点 **选一张图片**（最好挑纯色 / 近纯色背景的照片，比如证件照、白底商品图、截图）
2. 点 **一键自动抠图**，被抠掉的地方会显示成灰白棋盘格
3. 抠过头或没抠干净：
   - 拉 **抠图力度** 滑块，再点一次「一键自动抠图」
   - 或者打开 **手动补擦**，手指在图片上涂抹，把多余的地方擦成透明；点 **撤销** 可以退一步
4. 点 **换背景**，在 棋盘 / 纯白 / 红色 / 蓝色 / 渐变 之间挑一个
5. 点 **存透明 PNG**（保留透明通道）或 **存带背景图**，然后去手机相册的 **Pictures / 自动抠图** 文件夹里找

> 灰白棋盘格只是屏幕上的标记，表示「这里是透明的」；存透明 PNG 时不会把棋盘格存进去。

---

## 二、怎么拿到 APK（最重要）

1. 把本目录的文件 push 到 `https://github.com/zqmxm/omni-repo`
2. 打开仓库 → **Actions** → 左边选 **自动抠图** → 点最近一次运行 → 拉到页面底部 **Artifacts** → 下载 **自动抠图**
3. 解压得到 `自动抠图.apk`，传到手机，点安装（系统会让打开「允许未知来源」，允许即可）

另外 `.github/workflows/auto-backup.yml` 读到 `build-manifest.json` 里的这条配置后，会**新增一个独立 Release**（唯一 tag），去 **Releases** 页签按时间找最新那条即可；历史版本全部保留，永不覆盖。

这是 **debug APK**，自己装着自己用没问题；要上架应用商店还需要你自己的签名 keystore、应用图标和隐私政策，仓库里绝不放密钥。

---

## 三、本地自己打包

需要：JDK 17、Android SDK（platform 34 + build-tools 34）、Gradle 8.7。

```bash
cd android/自动抠图
gradle assembleDebug --no-daemon --stacktrace
# 产物：app/build/outputs/apk/debug/自动抠图.apk
```

> 如果将来 Android Gradle 插件去掉了旧版命名接口，Gradle 会退回默认名 `app-debug.apk`，
> 自己改名为 `自动抠图.apk` 即可；GitHub Actions 里有「统一产物名为软件本名」这一步，
> 会自动兜底，所以下载到的安装包一定叫 `自动抠图.apk`。

也可以用 Android Studio 直接打开 `android/自动抠图/` 目录。本机没装 Android SDK 就交给 CI，不要假装本地验证过。

---

## 四、怎么测试（不用装 Android SDK）

算法核心是纯 Java，装个 JDK 17 就能跑自检，CI 里也会自动跑：

```bash
cd android/自动抠图
mkdir -p build/自检
javac -encoding UTF-8 -d build/自检 \
  app/src/main/java/com/zqmxm/autocut/CutCore.java \
  自检/CutCoreSelfTest.java
java -cp build/自检 com.zqmxm.autocut.CutCoreSelfTest
```

自检覆盖：纯色背景能不能擦干净、主体内部同色区域会不会被误挖、手指补擦、抠图力度、整图同色不崩溃。任何一项失败都会让流水线变红。

---

## 五、每个文件是干什么的（大白话）

| 文件 / 文件夹 | 是干什么的 |
| --- | --- |
| `README.md` | 就是你正在看的这份说明书 |
| `VERSION` | 记着现在是第几版，自动备份时会加一 |
| `settings.gradle` | 告诉 Gradle「这个工程叫什么、里面有哪几个模块」 |
| `build.gradle` | 声明用的 Android 插件版本（AGP 8.5.2） |
| `gradle/wrapper/gradle-wrapper.properties` | 写清楚要用 Gradle 8.7 |
| `app/build.gradle` | 这个 App 的配置：安卓版本、包名、把安装包命名成 `自动抠图.apk` |
| `app/src/main/AndroidManifest.xml` | 向手机系统登记这个 App、申请必要的相册权限 |
| `app/src/main/res/values/strings.xml` | 手机桌面上显示的名字「自动抠图」 |
| `app/src/main/java/com/zqmxm/autocut/MainActivity.java` | 页面和按钮：选图、抠图、补擦、换背景、保存 |
| `app/src/main/java/com/zqmxm/autocut/CutCore.java` | 真正干活的抠图算法（纯 Java，不碰安卓类） |
| `自检/CutCoreSelfTest.java` | 检查算法有没有写错的小程序 |

`app/build.gradle` 里的 `applicationId`（`com.zqmxm.autocut`）是安卓系统强制要求的英文「应用身份证」，只能这样写；软件本名体现在目录名、README 标题、APK 文件名和流水线名字上。

---

## 六、能做到 / 做不到（如实说）

✅ **能做到**
- 纯色 / 近纯色背景一键抠成真透明，导出透明 PNG
- 换纯色 / 渐变背景，导出带底色的图
- 手动补擦、撤销、恢复原图、调抠图力度
- 零第三方依赖、零密钥、零模型文件，离线克隆就能看，CI 就能编

⚠️ **效果有限的地方**
- 复杂背景、头发丝、毛边、半透明物体：本版本用的是「边框取背景色 + 连通区域」的轻量算法，不是 AI 人像分割，边缘会比较粗，请配合手动补擦
- 背景颜色很杂时，界面会提示你开手动补擦，不会假装抠得很完美

🔌 **想升级成发丝级人像抠图怎么改**
- 接入端侧分割模型（MediaPipe Image Segmenter / TFLite 人像分割），把模型输出的透明度数组替换 `CutCore.autoCut()` 的返回值即可，**界面和保存逻辑一行都不用改**
- 模型 `.tflite` 是二进制大文件，按仓库 `AGENTS.md` 的规矩不能提交进仓库：放到 Release、Git LFS，或 App 首次启动时下载
- 届时在 `app/build.gradle` 加依赖、在 `AndroidManifest.xml` 按需加权限，并给 CI 补模型下载步骤

❌ **不做 / 不预置**
- 不放账号密钥、私有接口、签名 keystore、模型权重、二进制大文件
- 只出 debug APK，不做应用商店签名发布
- 不做「移除他人版权水印、绕过平台保护」这类用途；请只处理你有权使用的图片

---

## 七、流水线

`.github/workflows/自动抠图.yml`：只在 `android/自动抠图/**` 或它自己改动时触发（也可在 Actions 里手动重跑），依次跑算法自检 → 编译 APK → 校验 APK 里确实有 `AndroidManifest.xml` 和 `classes.dex` → 上传 Artifact。

## 许可

MIT，见仓库根目录 `LICENSE`。
