# Avatar Desktop — Windows 3D 数字人

基于 Go + three.js + three-vrm 的 Windows 3D 数字人，面向大屏/kiosk 场景。

## 架构

**Go 做大脑（状态机、语音、LLM 对话），WebView2 做脸（three.js + three-vrm 渲染），JS Bridge 通信。**

```
┌─────────────── Go 可执行文件 (.exe) ───────────────────────────┐
│                                                                 │
│  ┌──────────────────────────┐   ┌────────────────────────────┐ │
│  │  Go 后端（大脑）           │   │  WebView2（脸）             │ │
│  │                          │   │                            │ │
│  │  • 状态机 RobotMode       │◄──│  • three.js + three-vrm    │ │
│  │  • ASR / TTS / LLM       │JS │  • VRM 模型加载+渲染        │ │
│  │  • 拼音→viseme 映射       │桥 │  • 口型 viseme 动画         │ │
│  │  • 音频 I/O              │接 │  • 表情 blendshape          │ │
│  │                          │   │  • idle 呼吸/眨眼/微动      │ │
│  └──────────────────────────┘   └────────────────────────────┘ │
│                                                                 │
│  静态资源（embed 进二进制）：                                     │
│    index.html + three.js + three-vrm.js + VRM 模型文件           │
└─────────────────────────────────────────────────────────────────┘
```

## 技术选型

| 层 | 技术 | 说明 |
|----|------|------|
| 后端语言 | Go 1.24+ | 单一静态二进制，零依赖部署 |
| 窗口宿主 | WebView2 | Edge/Chromium 内核，Win10 1809+ 系统自带 |
| 3D 渲染 | three.js + three-vrm | WebGL 硬件加速，VRM 模型标准 |
| 对话 | LLM HTTP API | 阿里云百炼 Bailian（可配置其他 OpenAI 兼容 API） |
| ASR | 在线：阿里云语音识别 / 离线：sherpa-onnx SenseVoice | 在线走 WebSocket，离线走本地模型 |
| TTS | 在线：阿里云语音合成 / 离线：sherpa-onnx Matcha-TTS | 在线走 WebSocket，离线走本地模型 |

支持两种构建模式：

- **在线版**（默认）：ASR/TTS/LLM 全部走阿里云百炼 API，无需本地模型文件，但需要联网 + API Key。
- **离线版**（`-tags offline`）：ASR/TTS 走本地 sherpa-onnx 模型，只有 LLM 需要联网。适合内网/无外网环境。

## 状态机

```
        启动
  IDLE ◄──────────────────────────┐
    │                              │
    │ 点击屏幕 / 空格               │
    ↓                              │
  LISTENING ──→ (VAD 静音/手动停止)  │
    │                              │
    ↓                              │
  THINKING (LLM 请求中)             │
    │                              │
    ↓ (LLM 响应就绪)                │
  SPEAKING ──→ (TTS 播放完毕) ──────┘
```

## 目录结构

```
avatar_win/
├── README.md
├── USER_MANUAL.md             # 用户手册
├── go.mod / go.sum
├── main.go                    # 入口，启动窗口 + 大脑
├── build.ps1                  # 构建脚本（Windows PowerShell）
├── cfg.yml.template           # 配置模板
├── models/                    # 离线模型文件（仅离线版需要）
│   ├── README.md
│   ├── asr/                   # ASR 模型（SenseVoice）
│   │   ├── model.int8.onnx
│   │   └── tokens.txt
│   └── tts/                   # TTS 模型（Matcha-TTS + vocos）
│       ├── model.onnx         # Matcha 声学模型
│       ├── vocos.onnx         # vocos vocoder
│       ├── tokens.txt
│       ├── lexicon.txt
│       ├── date.fst           # 中文数字/日期正则化
│       ├── number.fst
│       ├── phone.fst
│       └── dict/              # jieba 分词词典
├── internal/
│   ├── brain/
│   │   ├── statemachine.go    # 状态机（含唤醒词检测）
│   │   ├── state.go           # 状态定义
│   │   ├── viseme.go          # 中文拼音→viseme 映射表
│   │   └── wakeword.go        # 唤醒词后台监听
│   ├── asr/
│   │   ├── init.go            # 构建入口
│   │   ├── init_online.go     # 在线 ASR（DashScope WebSocket）
│   │   ├── init_offline.go    # 离线 ASR（sherpa-onnx SenseVoice）
│   │   ├── client.go          # 在线 ASR 客户端
│   │   └── engine.go          # 离线 ASR 引擎
│   ├── tts/
│   │   ├── init.go            # 构建入口
│   │   ├── init_online.go     # 在线 TTS（DashScope）
│   │   ├── init_offline.go    # 离线 TTS（sherpa-onnx Matcha）
│   │   ├── client.go          # 在线 TTS 客户端
│   │   └── engine.go          # 离线 TTS 引擎
│   ├── llm/
│   │   └── client.go          # LLM HTTP 流式客户端
│   ├── audio/
│   │   ├── recorder.go        # 麦克风采集 接口
│   │   ├── recorder_windows.go # WASAPI 麦克风实现
│   │   └── playback.go        # 音频播放（oto/WASAPI）
│   ├── renderer/
│   │   ├── renderer.go        # Renderer 接口
│   │   └── renderer_windows.go  # WebView2 实现
│   ├── config/
│   │   └── config.go          # cfg.yml 配置加载
│   └── logfile/
│       └── logfile.go         # 文件日志
├── web/
│   ├── index.html             # 渲染页面（含 JS）
│   ├── js/
│   │   ├── three.module.js    # three.js
│   │   ├── three-vrm.module.js  # VRM loader
│   │   └── addons/            # GLTFLoader 等
│   └── models/
│       └── avatar.vrm         # VRM 数字人模型
├── third_party/go-webview2/   # WebView2 Go 绑定（本地修改版）
└── cert/                      # 签名证书
```

## JS Bridge 协议（Go ↔ WebView）

### Go → JS

```json
// 口型驱动
{"type": "viseme", "viseme": "aa", "weight": 1.0}

// 状态切换
{"type": "state", "mode": "speaking", "isSpeaking": true, "responseText": "你好！"}
```

### JS → Go

```json
// 点击屏幕
{"type": "tap"}
```

## 构建

### 在线版（默认）

```powershell
# 开发调试（exe 生成在当前目录）
go build -o avatar-desktop-x64.exe .

# 构建 + 打包 zip
powershell -ExecutionPolicy Bypass -File build.ps1 release
```

> **为什么双击 exe 会闪现黑色终端？**
> Go 编译的可执行文件默认链接为「控制台子系统」，双击运行时 Windows 会先弹出一个黑色 cmd 窗口。本项目 `build.ps1` 通过 `-ldflags "-H windowsgui"` 把 exe 编成「GUI 子系统」，所以 build.ps1 的产物直接双击就是数字人窗口、没有黑框。
> 如果直接用 `go build` 开发调试，想让产物同样不弹黑色终端，可执行以下一条命令（写入 Go 用户配置，永久生效）：
>
> ```powershell
> go env -w GOFLAGS=-ldflags=-H=windowsgui
> ```
>
> 之后这台机器上 `go build` 产生的所有 exe 都默认为窗口程序。撤销：
>
> ```powershell
> go env -u GOFLAGS
> ```
>
> 注意：该设置是**机器级**的，会影响本机所有 Go 项目的 `go build` 产物。

在线版产物：`dist/avatar-desktop-x64.zip`
- `avatar-desktop-x64.exe`（约 45 MB）
- `cfg.yml`（配置模板）
- `使用说明.md`

### 离线版

离线版需要 **MinGW-w64 x86_64 gcc**（CGO 编译 sherpa-onnx），以及模型文件。

#### 1. 准备模型文件

模型文件来源于 sherpa-onnx 官方模型包。假设模型包存放在 `siri_models/` 目录下，需准备如下文件：

| 文件 | 用途 | 来源 |
|------|------|------|
| `sherpa-onnx-sense-voice-zh-en-ja-ko-yue-int8-2025-09-09.tar` | 离线 ASR（语音识别） | sherpa-onnx 官方 SenseVoice 模型 |
| `matcha-icefall-zh-baker.tar` | 离线 TTS 声学模型 | sherpa-onnx 官方 Matcha-TTS 中文模型 |
| `vocos-22khz-univ.onnx` | 离线 TTS vocoder | sherpa-onnx 官方 vocos 模型 |

> 这些模型文件可在 [sherpa-onnx 模型页面](https://github.com/k2-fsa/sherpa-onnx/releases) 下载，也可以在 sherpa-onnx 项目源码的 `scripts/` 目录中找到下载脚本。

将模型文件整理到项目 `models/` 目录下：

```
models/
├── README.md                  # 保留
├── asr/
│   ├── model.int8.onnx        # 从 sense-voice tar 解压得到
│   └── tokens.txt             # 从 sense-voice tar 解压得到
└── tts/
    ├── model.onnx             # 从 matcha tar 解压的 model-steps-3.onnx，重命名为 model.onnx
    ├── vocos.onnx             # vocos-22khz-univ.onnx，重命名为 vocos.onnx
    ├── tokens.txt             # 从 matcha tar 解压得到
    ├── lexicon.txt            # 从 matcha tar 解压得到
    ├── date.fst               # 从 matcha tar 解压得到（中文数字/日期正则化）
    ├── number.fst             # 从 matcha tar 解压得到
    ├── phone.fst              # 从 matcha tar 解压得到
    └── dict/                  # 从 matcha tar 解压的 dict/ 目录（jieba 分词词典）
```

具体操作步骤：

1. 解压 `sherpa-onnx-sense-voice-zh-en-ja-ko-yue-int8-2025-09-09.tar`，取出 `model.int8.onnx` 和 `tokens.txt`，放入 `models/asr/`
2. 解压 `matcha-icefall-zh-baker.tar`，取出 `model-steps-3.onnx` 重命名为 `model.onnx`，连同 `tokens.txt`、`lexicon.txt`、`date.fst`、`number.fst`、`phone.fst`、`dict/` 目录，放入 `models/tts/`
3. 将 `vocos-22khz-univ.onnx` 重命名为 `vocos.onnx`，放入 `models/tts/`

#### 2. 编译

```powershell
# 离线版构建 + 打包 zip
powershell -ExecutionPolicy Bypass -File build.ps1 release offline
```

> 编译脚本会自动查找 MinGW-w64 gcc 编译器（常见路径 `D:\software\MinGW-W64-*\mingw64\bin\x86_64-w64-mingw32-gcc.exe` 或 `C:\mingw64\bin\...`）。
> 如果找不到，设置环境变量 `$env:CC = "你的gcc路径"` 再运行。

离线版产物：`dist/avatar-desktop-x64.zip`
- `avatar-desktop-x64.exe`（约 53 MB）
- `cfg.yml`（配置模板）
- `使用说明.md`
- `onnxruntime.dll`、`sherpa-onnx-c-api.dll`、`sherpa-onnx-cxx-api.dll`（sherpa-onnx 运行时，约 22 MB）
- `models/` 目录（ASR + TTS 模型文件，约 350 MB）

> 离线版 zip 约 400 MB，解压后约 430 MB。用户解压后直接双击 exe 即可使用，
> 无需额外安装任何依赖。LLM 仍需联网（需要配置 API Key），ASR 和 TTS 完全离线。

#### 3. 运行

离线版运行时，目录结构如下：

```
你的文件夹/
├── avatar-desktop-x64.exe
├── cfg.yml                    # 需要填写 llm.url 和 api_key
├── onnxruntime.dll
├── sherpa-onnx-c-api.dll
├── sherpa-onnx-cxx-api.dll
├── models/
│   ├── asr/
│   │   ├── model.int8.onnx
│   │   └── tokens.txt
│   └── tts/
│       ├── model.onnx
│       ├── vocos.onnx
│       ├── tokens.txt
│       ├── lexicon.txt
│       ├── date.fst
│       ├── number.fst
│       ├── phone.fst
│       └── dict/
└── 使用说明.md
```

双击 `avatar-desktop-x64.exe` 启动。ASR 和 TTS 完全离线运行，不消耗网络流量；仅 LLM 对话需要联网调用 API。

## 配置

复制 `cfg.yml.template` 为 `cfg.yml`，填入配置。完整配置模板见 `cfg.yml.template`，关键字段：

```yaml
asr:
  url: "wss://{WorkspaceId}.cn-beijing.maas.aliyuncs.com/api-ws/v1/inference"
  model: "qwen-audio-3.0-asr-flash-streaming"
  format: "pcm"
  sample_rate: 16000

llm:
  url: "https://{WorkspaceId}.cn-beijing.maas.aliyuncs.com/compatible-mode/v1/chat/completions"
  model: "qwen-plus"
  max_tokens: 512
  name: "小然"           # 数字人名字（写入系统提示词）

tts:
  url: "wss://dashscope.aliyuncs.com/api-ws/v1/realtime"
  model: "qwen3-tts-flash-realtime"
  voice: "Cherry"
  format: "pcm"
  sample_rate: 24000

api_key: "your-api-key"

wake_word: "小然"         # 唤醒词（可自定义）

avatar:
  idle_animations_enabled: true   # idle 时是否随机播放动作动画
```

> **离线版注意**：`asr` 和 `tts` 配置项会被忽略（本地模型替代），但仍需配置 `llm` 和 `api_key`。

## 使用

1. 双击 `avatar-desktop-x64.exe`
2. **唤醒词**：直接说「小然」即可唤醒对话（默认开启），唤醒后可以接着说指令
3. **手动触发**：点击屏幕 / 按空格 / 回车，进入聆听状态后说话
4. 说话 → ASR 识别 → LLM 回复 → TTS 语音 + 口型动画

> 支持多轮对话：程序会记住最近 10 轮对话上下文，追问无需重复唤醒词。

## 大屏硬件建议

- CPU：Intel i5 8代+ 或同等 AMD
- 内存：8GB+
- GPU：集显即可（WebGL 渲染）
- 麦克风：USB 麦克风或阵列麦
- 系统：Windows 10 1809+

## 参考

- [three-vrm](https://github.com/pixiv/three-vrm) — three.js VRM 加载器
- [VRM 规范](https://vrm.dev/) — VRM 模型格式标准
- [VRoid Studio](https://vroid.com/en/studio) — 免费 3D 角色制作工具
- [阿里云百炼](https://bailian.console.aliyun.com/) — LLM/ASR/TTS API