# Avatar PC — 大屏 3D 数字人

基于 Go + three.js + three-vrm 的企业级 3D 数字人，面向大屏/kiosk 场景。
复用现有移动端的 ASR/TTS/KWS 离线模型和 LLM 对话链路，将渲染层从 2D 矢量/火柴人升级为 VRM 3D 数字人。

## 一句话架构

**Go 做大脑（状态机、语音、LLM 对话），浏览器内核做脸（WebView2/Lorca 加载 three.js + three-vrm 渲染 3D 数字人），中间一条 JS Bridge 通信。**

```
┌─────────────── Go 可执行文件 (.exe 或 Linux 二进制) ───────────────┐
│                                                                   │
│  ┌──────────────────────────┐   ┌──────────────────────────────┐ │
│  │  Go 后端（大脑）           │   │  WebView / Lorca（脸）        │ │
│  │                          │   │                              │ │
│  │  • 状态机 RobotMode       │◄──│  • three.js + three-vrm      │ │
│  │  • ASR (sherpa-onnx)     │JS │  • VRM 模型加载+渲染          │ │
│  │  • TTS (sherpa-onnx)     │桥 │  • 口型 viseme 动画           │ │
│  │  • 唤醒词 KWS             │接 │  • 表情 blendshape            │ │
│  │  • LLM HTTP 客户端        │   │  • idle 呼吸/眨眼/微动        │ │
│  │  • 拼音→viseme 映射       │   │  • 全屏 kiosk 模式            │ │
│  │  • 音频 I/O (malgo)      │   │                              │ │
│  └──────────────────────────┘   └──────────────────────────────┘ │
│                                                                   │
│  静态资源（embed 进二进制）：                                       │
│    index.html + three.js + three-vrm.js + VRM 模型文件             │
└───────────────────────────────────────────────────────────────────┘
```

## 技术选型

| 层 | 技术 | 说明 |
|----|------|------|
| 后端语言 | Go 1.23+ | 单一静态二进制，零依赖部署 |
| 音频 I/O | malgo (miniaudio) | 单头文件 C 库，支持 ASIO/WASAPI/ALSA/PulseAudio |
| ASR | sherpa-onnx SenseVoiceSmall int8 | 离线，~158MB，与 iOS/Android 同模型 |
| TTS | sherpa-onnx Matcha-TTS + vocos | 离线，~123MB，与 iOS/Android 同模型 |
| 唤醒词 | sherpa-onnx KWS Zipformer 3.3M | 离线，~13MB |
| 对话 | LLM HTTP 流式 API | 可配置 OpenAI/Claude/本地模型 endpoint |
| 3D 渲染 | three.js + three-vrm | WebGL 硬件加速，VRM 模型标准 |
| Windows 窗口 | WebView2 (系统自带) | Edge/Chromium 内核，Win10 1809+ 默认存在 |
| Linux 窗口 | Lorca (调系统 Chrome `--app` 模式) | 需系统装 Chrome/Chromium |
| 3D 模型 | VRM 格式 (免费开源/VRoid Studio 自制) | 支持 blendshape 口型/表情 |

## 跨平台

| | Windows | Ubuntu 24.04 Desktop |
|--|---------|---------------------|
| 窗口宿主 | WebView2（系统自带） | Lorca（调用系统 Chrome） |
| 额外依赖 | 无 | 需装 Chrome |
| Go 代码 | `renderer_windows.go` | `renderer_linux.go` |
| 编译产物 | 单一 `.exe` | 单一 Linux 二进制 |
| 渲染层 | 同一套 HTML/JS（两平台共享，内核同源 Chromium） | ← 同 |

**开发工作流：** Ubuntu 上开发/预览效果 → 代码同步到 Windows → Windows 编译 `.exe` → 拷贝到客户大屏机器。

## 与现有 iOS/Android 版本的对比

| | iOS/Android | PC (本方案) |
|--|-------------|-------------|
| 大脑 | Swift/Kotlin 调 sherpa-onnx C API | Go + CGo 调 sherpa-onnx C API |
| 模型 | SenseVoiceSmall + Matcha-TTS + KWS | 同模型，复用 |
| LLM | HTTP 流式客户端 | 同，Logic 平移 |
| 脸 | Core Graphics/Canvas 2D 矢量 / 火柴人 | three.js + three-vrm 3D 数字人 |
| 口型 | 假（0.12s 定时器随机翻 speakAmount） | 真（中文拼音→viseme 映射，逐字驱动） |
| 人脸跟踪 | Vision/ML Kit 表情模仿 | 企业大屏不需要，不实现 |
| 部署 | App Store / APK | 单一 exe / Linux 二进制 |

## 核心升级：中文拼音→Viseme 口型同步

这是"玩具→数字人"的关键质变。现有版本 `speakAmount` 是假口型（一个 0.12s 定时器在 `0.2↔1.0` 之间来回翻），本方案做真口型。

### 原理

- TTS 合成文本 → 同步产出拼音/viseme 时间线
- 每个字的韵母映射到 5 个标准 VRM 口型（A/I/U/E/O），双唇音映射到闭口
- 时长按"总音频时长 ÷ 字数"加权分配，标点处插停顿

### 中文韵母→VRM Viseme 映射表

| 韵母 | 例字 | VRM Viseme | 口型描述 |
|------|------|------------|---------|
| a, ai, an, ang, ia, ua, iao, uan, iang | 啊/开/三/上/家/花/小/关/香 | **A** | 大开口 |
| i, ei, in, ing, ui, iu, ian | 一/飞/音/星/归/秋/天 | **I** | 咧嘴 |
| u, ou, ong, ü, un, iong | 乌/走/东/女/云/穷 | **U** | 圆唇 |
| e, ie, üe, er, en, eng, uen | 饿/写/月/二/本/风/问 | **E** | 半开口 |
| o, uo, ao | 我/说/好 | **O** | 圆唇张大 |
| 声母 b/p/m (双唇音) | 爸/怕/妈 | **闭口** | 闭嘴 |
| 其他声母 + 韵母组合 | 以韵母为准 | 韵母对应 viseme | — |
| 无声母（零声母） | 以韵母为准 | 韵母对应 viseme | — |
| 标点/停顿 | — | **闭口** | 自然停顿 |

### 数据流

```
LLM 回复文本
  → TextNormalizer.normalize()（复用现有）
  → 分句 splitSentences()
  → 逐句送 TTS 合成 → Float32 PCM（PCM 时长已知）
  → 同时：逐句 → pypinyin 转拼音 → 逐字韵母→viseme 映射
  → viseme 时间线 = [字1: viseme_0ms, 字2: viseme_100ms, ...]
  → 通过 JS Bridge 发给 WebView → three-vrm 驱动 blendshape
```

## 表情系统

### 情绪→VRM Blendshape 映射

沿用现有 `Emotion` 枚举（neutral/happy/curious/surprised/shy/sleepy/sad），映射到 VRM 标准表情 blendshape：

| Emotion | VRM Blendshape | 强度 |
|---------|---------------|------|
| neutral | neutral | 1.0 |
| happy | fun / joy | 1.0 |
| curious | — | 微调 eyebrow + head tilt |
| surprised | surprised | 1.0 |
| shy | — | 微调 blush + 低头 |
| sleepy | — | 眯眼 + 微低头 |
| sad | sorrow | 1.0 |

### 情绪来源

LLM 回复时附带情绪标签（如 `[emotion:happy]你好呀！`），Go 解析后驱动表情。无标签时默认 neutral。

## 状态机（复用现有 RobotMode）

```
        启动
  IDLE ◄──────────────────────────────────┐
    │                                      │
    │ 唤醒词 / 点击屏幕                     │
    ↓                                      │
  LISTENING ──→ (VAD 静音/手动停止)         │
    │                                      │
    ↓                                      │
  THINKING (LLM 请求中)                     │
    │                                      │
    ↓ (LLM 响应就绪)                        │
  SPEAKING ──→ (TTS 播放完毕) ──────────────┘
```

## 项目目录结构

```
pc/
├── README.md
├── go.mod
├── go.sum
├── main.go                    # 入口，启动窗口 + 大脑
├── cmd/
│   └── avatar/
│       └── main.go            # 实际入口（可选）
├── internal/
│   ├── brain/
│   │   ├── statemachine.go    # RobotMode 状态机
│   │   ├── emotion.go         # Emotion 枚举 + 情绪→blendshape 映射
│   │   └── viseme.go          # 中文拼音→viseme 映射表 + 时间线生成
│   ├── asr/
│   │   └── engine.go          # CGo 封装 sherpa-onnx ASR
│   ├── tts/
│   │   ├── engine.go          # CGo 封装 sherpa-onnx TTS
│   │   └── normalizer.go      # 文本归一化（移植现有 TextNormalizer）
│   ├── kws/
│   │   └── engine.go          # CGo 封装 sherpa-onnx KWS 唤醒词
│   ├── llm/
│   │   └── client.go          # LLM HTTP 流式客户端
│   ├── audio/
│   │   ├── capture.go         # 麦克风采集（malgo）
│   │   └── playback.go        # 音频播放（malgo）
│   └── renderer/
│       ├── renderer.go        # Renderer 接口定义
│       ├── renderer_windows.go  # WebView2 实现（//go:build windows）
│       ├── renderer_linux.go    # Lorca 实现（//go:build linux）
│       └── bridge.go          # JS Bridge：Go → JS 消息协议
├── web/
│   ├── index.html             # 渲染页面
│   ├── js/
│   │   ├── three.min.js       # three.js
│   │   ├── three-vrm.min.js   # VRM loader
│   │   └── avatar.js          # 数字人控制逻辑（接收 JS Bridge 消息）
│   ├── models/
│   │   └── avatar.vrm         # VRM 数字人模型
│   └── css/
│       └── style.css
├── models/                    # 离线模型（不提交 git）
│   ├── asr/
│   │   ├── model.int8.onnx
│   │   └── tokens.txt
│   ├── tts/
│   │   ├── model.onnx
│   │   ├── vocos.onnx
│   │   ├── tokens.txt
│   │   └── lexicon.txt
│   └── kws/
│       ├── model.onnx
│       └── tokens.txt
└── scripts/
    ├── download-models.sh     # 下载离线模型到 models/
    └── build-windows.sh       # Windows 交叉编译脚本
```

## JS Bridge 协议（Go ↔ WebView）

Go 通过 `window.external` 或 `eval` 向 WebView 注入消息，WebView 通过回调路径回传事件。

### Go → JS（控制数字人）

```json
// 口型驱动
{"type": "viseme", "viseme": "A", "weight": 1.0, "duration_ms": 120}

// 表情
{"type": "emotion", "emotion": "happy", "weight": 1.0}

// 状态切换
{"type": "mode", "mode": "speaking"}

// 眨眼
{"type": "blink"}
```

### JS → Go（用户交互事件）

```json
// 点击屏幕
{"type": "tap"}

// 唤醒词触发的视觉反馈
{"type": "wake_detected"}
```

## 开发阶段

### P0 — 渲染验证（1-2 周）

- [ ] Go 项目骨架：`go.mod`、`main.go`、目录结构
- [ ] WebView2 窗口（Windows）+ Lorca 窗口（Linux）
- [ ] 加载 `web/index.html`，three.js + three-vrm 加载 VRM 模型
- [ ] idle 动画：呼吸、眨眼、眼神微动
- [ ] 验收：**窗口里站着一个 3D 数字人，会眨眼、有呼吸**

### P1 — 大脑平移（1-2 周）

- [ ] CGo 接入 sherpa-onnx ASR（SenseVoiceSmall）
- [ ] CGo 接入 sherpa-onnx TTS（Matcha-TTS + vocos）+ 文本归一化
- [ ] 音频 I/O：malgo 麦克风采集 + 扬声器播放
- [ ] 拼音→viseme 映射表 + 时间线生成
- [ ] JS Bridge：viseme 时间线 → three-vrm 口型驱动
- [ ] 验收：**说一句话，嘴型跟得上**

### P2 — 表情+对话（1-2 周）

- [ ] LLM HTTP 流式客户端
- [ ] 状态机实现（idle/listening/thinking/speaking）
- [ ] 情绪标签解析 + 表情 blendshape 驱动
- [ ] 完整对话链路：唤醒/点击 → 录音 → ASR → LLM → TTS → 口型+表情
- [ ] 验收：**完整对话，有表情、有口型**

### P3 — 大屏加固（1-2 周）

- [ ] 全屏 kiosk 模式（无边框、无任务栏、防退出）
- [ ] 开机自启（Windows 注册表/启动文件夹）
- [ ] 崩溃自恢复（watchdog）
- [ ] 待机动画（长时间无人交互时切 idle 循环）
- [ ] 字幕显示（LLM 回复文本实时滚动）
- [ ] 设置页面（LLM endpoint 配置、模型管理、音量）
- [ ] 验收：**可交付的大屏数字人**

## 关键依赖

### Go 依赖

```
github.com/zserge/lorca          # Linux 窗口宿主（调系统 Chrome）
github.com/jchv/go-webview2      # Windows 窗口宿主（WebView2）
github.com/gen2brain/malgo       # 音频 I/O（基于 miniaudio）
github.com/mozillazg/go-pinyin   # 中文拼音转换（viseme 映射用）
```

### 系统依赖

| 平台 | 依赖 | 安装方式 |
|------|------|---------|
| Ubuntu 24.04 | Chrome/Chromium | `sudo apt install chromium-browser` |
| Ubuntu 24.04 | gcc (CGo 编译) | 系统自带 |
| Ubuntu 24.04 | ALSA/PulseAudio 开发库 | `sudo apt install libasound2-dev libpulse-dev` |
| Windows 10/11 | WebView2 Runtime | 系统自带 (Win10 1809+) |
| Windows 10/11 | MinGW gcc (CGo 编译) | MSYS2: `pacman -S mingw-w64-x86_64-gcc` |

### 离线模型（与 iOS/Android 共用）

| 模型 | 文件 | 大小 |
|------|------|------|
| ASR: SenseVoiceSmall int8 | `model.int8.onnx` + `tokens.txt` | ~158MB |
| TTS: Matcha-TTS zh-baker | `model.onnx` + `tokens.txt` + `lexicon.txt` | ~72MB |
| Vocoder: vocos-22khz-univ | `vocos.onnx` | ~51MB |
| KWS: Zipformer 3.3M | `model.onnx` + `tokens.txt` | ~13MB |
| **合计** | | **~294MB** |

## VRM 模型来源

### 免费方案（起步推荐）

1. **VRoid Studio**（免费，Windows/macOS）
   - 官网：https://vroid.com/en/studio
   - 自动生成带标准 blendshape（A/I/U/E/O 口型 + 表情）的 VRM 模型
   - 可调整服装、发型、体型，做出"职业清爽"形象
   - 导出 `.vrm` 文件，放 `web/models/` 目录

2. **VRoid Hub**（社区模型，部分 CC0）
   - https://hub.vroid.com/
   - 筛选可商用/CC0 授权的模型直接使用

3. **three-vrm 示例模型**
   - https://github.com/pixiv/three-vrm/tree/dev/packages/three-vrm/examples/models
   - 含 CC0 授权的测试模型

### 商用方案

- 找画师/建模师定制 VRM 模型
- 购买商业 VRM 模型（Booth、VRoid Hub 付费区）

## 注意事项

### sherpa-onnx 的链接方式

sherpa-onnx 是 C 库，Go 通过 CGo 调用。两个平台：

- **Windows**：官方提供 `sherpa-onnx.dll`，编译时链接 `.lib`，运行时 dll 与 exe 同目录即可。后续可改为静态链接 `.lib` 实现"真正单文件"。
- **Linux**：官方提供 `libsherpa-onnx.so`，或从源码编译静态 `.a`。

### 音频延迟

TTS 合成是整句一次性产出 PCM，但口型需要逐字驱动。策略：
- 总 PCM 时长 / 字数 = 每个字的平均时长
- 标点处插入额外停顿
- 播放时按 viseme 时间线切换口型

### 唤醒词与 ASR 共享麦克风

唤醒词和 ASR 都读麦克风，需要做音频路由：
- 默认状态：KWS 持续监听唤醒词
- 唤醒后：切换到 ASR 录音模式
- ASR 识别完成后：切回 KWS 监听

### 大屏硬件建议

- CPU：Intel i5 8代+ 或同等 AMD（sherpa-onnx 用 xnnpack 推理，CPU 即可）
- 内存：8GB+（模型加载后约 500MB 常驻 + 系统）
- GPU：集显即可（WebGL 渲染不依赖独显，three-vrm 模型面数低）
- 麦克风：USB 麦克风或阵列麦（大屏离人远，建议定向麦）
- 扬声器：大屏自带或外接
- 系统：Windows 10 1809+ 或 Ubuntu 24.04 Desktop

## 参考

- [sherpa-onnx](https://github.com/k2-fsa/sherpa-onnx) — 离线 ASR/TTS/KWS 引擎
- [three-vrm](https://github.com/pixiv/three-vrm) — three.js VRM 加载器
- [VRoid Studio](https://vroid.com/en/studio) — 免费 3D 角色制作工具
- [VRM 规范](https://vrm.dev/) — VRM 模型格式标准
- [malgo](https://github.com/gen2brain/malgo) — miniaudio Go 绑定
- [Lorca](https://github.com/zserge/lorca) — Go + Chrome `--app` 模式
- [go-webview2](https://github.com/jchv/go-webview2) — Go WebView2 绑定