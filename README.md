# Avatar PC — Windows 3D 数字人

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
| 后端语言 | Go 1.23+ | 单一静态二进制，零依赖部署 |
| 窗口宿主 | WebView2 | Edge/Chromium 内核，Win10 1809+ 系统自带 |
| 3D 渲染 | three.js + three-vrm | WebGL 硬件加速，VRM 模型标准 |
| 对话 | LLM HTTP 流式 API | 阿里云百炼 Bailian（可配置其他 OpenAI 兼容 API） |
| ASR | 阿里云实时语音识别 | 流式 WebSocket 长连接 |
| TTS | 阿里云语音合成 | 阿里云百炼语音合成 |

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
├── go.mod / go.sum
├── main.go                    # 入口，启动窗口 + 大脑
├── build.sh                   # 构建脚本
├── cfg.yml                    # 配置文件
├── internal/
│   ├── brain/
│   │   ├── statemachine.go    # RobotMode 状态机
│   │   ├── state.go           # 状态定义
│   │   └── viseme.go          # 中文拼音→viseme 映射表
│   ├── asr/
│   │   └── client.go          # 阿里云 ASR WebSocket 客户端
│   ├── tts/
│   │   └── client.go          # 阿里云 TTS 客户端
│   ├── llm/
│   │   └── client.go          # LLM HTTP 流式客户端
│   ├── audio/
│   │   ├── recorder.go        # 麦克风采集
│   │   └── playback.go        # 音频播放
│   ├── renderer/
│   │   ├── renderer.go        # Renderer 接口
│   │   └── renderer_windows.go  # WebView2 实现
│   ├── config/
│   │   └── config.go          # 配置加载
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

```bash
# 增量构建
bash build.sh

# 构建 + 打包 zip
bash build.sh release
```

产物：`dist/avatar-pc.exe`（22MB，单文件，自签名）。

## 配置

复制 `cfg.yml.example` 为 `cfg.yml`，填入阿里云百炼 API Key：

```yaml
api_key: "your-api-key"
asr:
  url: "wss://dashscope.aliyuncs.com/api-ws/v1/realtime"
  model: "paraformer-realtime-v2"
llm:
  url: "https://dashscope.aliyuncs.com/compatible-mode/v1"
  model: "qwen-plus"
tts:
  url: "https://dashscope.aliyuncs.com/api-ws/v1/tts"
  model: "qwen-tts"
  voice: "longxiaochun"
```

## 使用

1. 双击 `avatar-pc.exe`
2. 点击屏幕或按空格开始对话
3. 说话 → ASR 识别 → LLM 回复 → TTS 语音 + 口型动画

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