# android/ · 最小 Android 原生工程

一个能真正打出 **APK** 的零第三方依赖工程，纯 Java、无布局 XML、无 Compose，专门为了让任何环境都能编过。

## 怎么拿到 APK（最简单）

1. push 代码 → 仓库 **Actions** → `Android APK` 流水线
2. 跑完后在该次运行底部下载 Artifact `omni-android-debug-apk`
3. 解压得到 `app-debug.apk`，传到手机安装（需允许「未知来源」）

流水线在 `.github/workflows/android.yml`，只在 `android/**` 改动时触发，也可手动 `workflow_dispatch` 重跑。

## 本地构建

需要：JDK 17、Android SDK（platform 34 + build-tools 34.0.0）、Gradle 8.7。

```bash
cd android
gradle wrapper --gradle-version 8.7 --distribution-type bin   # 首次生成 gradlew（jar 不入仓库）
./gradlew assembleDebug
# 产物：app/build/outputs/apk/debug/app-debug.apk
```

或者直接用 **Android Studio** 打开 `android/` 目录，它会自动处理 Gradle 与 SDK。

## 版本约定（改代码请保持）

| 项 | 版本 |
| --- | --- |
| JDK | 17 |
| Gradle | 8.7 |
| Android Gradle Plugin | 8.5.2 |
| compileSdk / targetSdk | 34 |
| minSdk | 24 |
| applicationId | `com.zqmxm.omni` |

## 加功能时

- 页面写在 `app/src/main/java/com/zqmxm/omni/`
- 需要权限就在 `AndroidManifest.xml` 里加（并记得动态申请）
- 加第三方库写在 `app/build.gradle` 的 `dependencies {}`
- **Release 签名**：用你自己的 keystore，通过 `local.properties` 或环境变量提供，`*.jks` / `*.keystore` 已被 `.gitignore` 拦截，绝不提交

## 边界

- 本流水线只出 **debug APK**（自测用）。上架应用商店需要签名 release AAB、应用图标、隐私政策等，另配
- 本机若没有 Android SDK 就交给 CI，不要声称本地验证过
