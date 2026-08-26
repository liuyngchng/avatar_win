# Avatar Desktop 数字人（离线版）— Windows 用户手册

## 一、这是什么

Avatar Desktop 离线版是一款运行在 Windows 大屏/PC 上的 **3D 数字人问答应用**。用户说唤醒词「小冉」、点击屏幕或按空格键，对着麦克风说话，数字人会：

1. 听你说的话（语音识别，**本地模型，无需联网**）
2. 理解并生成回答（大模型对话，**需要联网**）
3. 用语音念出回答（语音合成，**本地模型，无需联网**）
4. 同步做出嘴型（口型动画）

> **离线版与在线版的区别**：
>
> | 能力 | 离线版 | 在线版 |
> |------|--------|--------|
> | 语音识别（听） | 本地模型，**不联网** | 联网调用 API |
> | 语音合成（说） | 本地模型，**不联网** | 联网调用 API |
> | 大模型对话（想） | **需要联网** | 需要联网 |
> | 部署体积 | 约 430 MB（含模型） | 约 50 MB |
> | 适用场景 | 内网 / 无外网 / 数据敏感 | 有稳定外网 |

---

## 二、运行环境要求

| 项目 | 最低要求 |
|------|---------|
| 操作系统 | Windows 10 1809 及以上（推荐 Windows 11） |
| 内存 | 8 GB 及以上（推荐 16 GB） |
| 磁盘 | 500 MB 可用空间（含模型文件） |
| 网络 | LLM 对话需联网；ASR/TTS 不联网 |
| 麦克风 | 可用的麦克风设备 |
| 扬声器 | 可用的扬声器/音响设备 |
| WebView2 运行时 | 系统自带（Win10 1809+ 默认已安装） |

> **注意**：首次启动时，程序需要加载本地 ASR/TTS 模型到内存，可能需要十几秒到半分钟，请耐心等待。加载完成后即可正常使用。

---

## 三、部署步骤（首次安装）

软件无需安装，**解压即可运行**。交付物是一个 zip 压缩包，解压后包含：

```
avatar-desktop-x64.exe       — 主程序
cfg.yml                      — 配置文件（需填写 LLM 相关）
onnxruntime.dll              — 语音运行时库
sherpa-onnx-c-api.dll        — 语音运行时库
sherpa-onnx-cxx-api.dll      — 语音运行时库
models/                      — 本地模型目录
  ├── asr/                   — 语音识别模型
  │   ├── model.int8.onnx
  │   └── tokens.txt
  └── tts/                   — 语音合成模型
      ├── model.onnx
      ├── vocos.onnx
      ├── tokens.txt
      ├── lexicon.txt
      ├── date.fst
      ├── number.fst
      ├── phone.fst
      └── dict/
使用说明.md                   — 本说明
```

### 步骤 1：解压

将 zip 解压到任意目录（建议路径不含中文和空格），例如 `D:\数字人\`。

### 步骤 2：填写配置

用记事本打开 `cfg.yml`，只需填写**大模型（LLM）相关**配置。语音识别和语音合成已经本地化，**不需要**填写 asr/tts 的 url。

### 步骤 3：双击运行

双击 `avatar-desktop-x64.exe` 即可启动。程序会先加载本地模型（约 10~30 秒），之后弹出窗口显示 3D 数字人。

---

## 四、配置说明（cfg.yml）

离线版只需关注 `llm` 和 `api_key`，其余 `asr`/`tts` 配置会被忽略。

```yaml
# 离线版：asr/tts 走本地模型，以下配置忽略
# asr: ...
# tts: ...

llm:                              # 大模型对话（生成回答）—— 必须填写
  url: "https://{WorkspaceId}.cn-beijing.maas.aliyuncs.com/compatible-mode/v1/chat/completions"
  model: "qwen-plus"              # 对话模型名
  max_tokens: 512
  name: "小冉"                    # 数字人名字

api_key: "sk-你的百炼APIKey"      # 百炼 API Key —— 必须填写

wake_word: "小冉"                 # 唤醒词（可选，默认"小冉"）

avatar:
  idle_animations_enabled: true   # idle 时是否随机播放动作动画
```

### 需要填写的内容

| 字段 | 说明 | 在哪里获取 |
|------|------|-----------|
| `llm.url` 里的 `WorkspaceId` | 业务空间 ID | 百炼控制台 → 业务空间 |
| `api_key` | 百炼 API Key（`sk-` 开头） | [获取 API Key](https://help.aliyun.com/zh/model-studio/get-api-key) |
| `llm.model` | 对话模型名 | 默认 `qwen-plus`，可改 `qwen-max`、`qwen-turbo` 等 |

### 填写示例

假设你的 WorkspaceId 是 `abc123`，API Key 是 `sk-xxxxxxxxxxxx`：

```yaml
llm:
  url: "https://abc123.cn-beijing.maas.aliyuncs.com/compatible-mode/v1/chat/completions"
  model: "qwen-plus"
  max_tokens: 512
  name: "小冉"

api_key: "sk-xxxxxxxxxxxx"

wake_word: "小冉"

avatar:
  idle_animations_enabled: true
```

---

## 五、使用说明

### 唤醒与对话

离线版支持**唤醒词**和**手动触发**两种方式：

#### 方式一：唤醒词（推荐）

1. 启动后数字人进入待机状态，**持续后台监听**。
2. 直接说 **「小冉」** 唤醒数字人。
3. 唤醒后可以紧接着说出指令，例如「小冉，今天天气怎么样？」
4. 数字人会识别你的话，思考后语音回答。

#### 方式二：手动触发

1. **点击屏幕任意位置**，或按 **空格键 / 回车键**。
2. 看到状态变成 **「聆听中...」** 后，**对着麦克风说话**。
3. 说完后 **停顿约 1.5 秒**，程序会自动停止录音。
4. 状态会依次经过 **「思考中...」** → 数字人开始说话。

### 状态说明

| 屏幕提示 | 含义 |
|---------|------|
| （无提示，仅 3D 数字人） | 待机状态（后台监听唤醒词） |
| 聆听中... | 正在录音，请说话 |
| 思考中... | 正在识别/生成回答 |
| 底部字幕 + 嘴型动画 | 正在说话 |

### 多轮对话

程序会记住最近 **10 轮**对话上下文。你可以连续追问，无需每句都说唤醒词。例如：

> 你：「小冉，介绍一下你自己」
> 数字人：「我叫小冉，是一个数字人助手…」
> 你：「你今年多大了？」（无需再说"小冉"）
> 数字人：「…」

### 移动与旋转视角

- **拖动数字人**：旋转视角（环绕观察）
- **Ctrl + 拖动**：移动整个窗口
- **WASD / 方向键**：移动窗口
- 窗口默认对齐**屏幕右下角**

### 退出

- 按 **Alt + F4** 关闭窗口；或从任务管理器结束 `avatar-desktop-x64.exe` 进程。

---

## 六、常见问题（FAQ）

### 1. 双击后没反应 / 闪退

- 确认 `cfg.yml` 和 `avatar-desktop-x64.exe` 在同一个文件夹。
- 确认 `models/` 目录完整（`models/asr/model.int8.onnx`、`models/tts/model.onnx` 等都在）。
- 确认三个 DLL 文件（`onnxruntime.dll` 等）和 exe 在同一个文件夹。
- 确认 `cfg.yml` 里 `api_key` 和 `llm.url` 都填了，且没有拼错。

### 2. 报错「Failed to load config」

- 说明程序没找到 `cfg.yml`。请把它放到 exe 同目录。

### 3. 启动后卡住 / 加载很慢

- 首次加载模型需要 10~30 秒，属正常现象。
- 若超过 1 分钟仍无反应，检查 `models/` 目录是否完整，或内存是否不足（建议 8GB+）。

### 4. 报错「ASR model file not found」或「TTS model file not found」

- 说明本地模型文件缺失。请对照「三、部署步骤」中的目录结构，检查 `models/asr/` 和 `models/tts/` 下的文件是否齐全。

### 5. 说话没反应 / 识别不到

- 检查麦克风是否可用（Windows 设置 → 隐私 → 麦克风，确认允许应用访问）。
- 确认录音时能看到「聆听中...」提示，且离麦克风近一点、说话清晰。
- 离线版语音识别对安静环境更友好，建议在噪音较小的环境使用。

### 6. 数字人嘴型不张嘴 / 没有声音

- 检查扬声器是否正常、音量是否打开。

### 7. 报错「HTTP 401 / 403」

- API Key 错误或过期。重新到百炼控制台获取并填入 `api_key`。
- 离线版虽然 ASR/TTS 本地化，但 **LLM 对话仍需联网**，请确认网络可访问阿里云百炼。

### 8. 完全断网能说话吗

- **不能**。离线版只把「听」和「说」本地化了，但「想」（生成回答）仍需联网调用大模型 API。完全断网时，数字人能听能说，但无法生成回答内容。

---

## 七、交付清单

| 文件 | 说明 | 是否必填 |
|------|------|---------|
| `avatar-desktop-x64.exe` | 主程序 | 无需改动 |
| `cfg.yml` | 配置文件 | **需要填写** llm.url、api_key |
| `onnxruntime.dll` | 语音运行时库 | 无需改动（必须保留） |
| `sherpa-onnx-c-api.dll` | 语音运行时库 | 无需改动（必须保留） |
| `sherpa-onnx-cxx-api.dll` | 语音运行时库 | 无需改动（必须保留） |
| `models/asr/` | 语音识别模型 | 无需改动（必须保留） |
| `models/tts/` | 语音合成模型 | 无需改动（必须保留） |

> ⚠️ 以上 DLL 和模型文件缺一不可，请勿删除或移动。所有文件必须保持在同一目录结构下。

---

## 八、技术支持

- 阿里云百炼文档：<https://help.aliyun.com/zh/model-studio/>
- 获取 API Key：<https://help.aliyun.com/zh/model-studio/get-api-key>
- 获取 Workspace ID：<https://help.aliyun.com/zh/model-studio/obtain-the-app-id-and-workspace-id>
- sherpa-onnx 模型：<https://github.com/k2-fsa/sherpa-onnx>
