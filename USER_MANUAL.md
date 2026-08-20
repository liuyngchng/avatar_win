# Avatar PC 数字人 — Windows 用户手册

## 一、这是什么

Avatar PC 是一款运行在 Windows 大屏/PC 上的 **3D 数字人问答应用**。用户点击屏幕或按空格键，对着麦克风说话，数字人会：

1. 听你说的话（语音识别）
2. 理解并生成回答（大模型对话）
3. 用语音念出回答（语音合成）
4. 同步做出嘴型（口型动画）

全程需要联网（使用阿里云百炼在线服务），**无需在电脑上安装任何模型文件**。

---

## 二、运行环境要求

| 项目 | 最低要求 |
|------|---------|
| 操作系统 | Windows 10 1809 及以上（推荐 Windows 11） |
| 内存 | 4 GB 及以上 |
| 磁盘 | 100 MB 可用空间 |
| 网络 | 可访问公网（阿里云百炼服务） |
| 麦克风 | 可用的麦克风设备 |
| 扬声器 | 可用的扬声器/音响设备 |
| WebView2 运行时 | 系统自带（Win10 1809+ 默认已安装） |

> **注意**：软件内置了一个 3D 数字人模型，无需额外安装显卡驱动或 3D 软件。

---

## 三、部署步骤（首次安装）

软件无需安装，**解压即可运行**。交付物包含两个文件：

```
avatar.exe     — 主程序（约 25 MB）
cfg.yml        — 配置文件
```

### 步骤 1：放置文件

将 `avatar.exe` 和 `cfg.yml` 放在**同一个文件夹**里，例如：

```
D:\数字人\
  ├── avatar.exe
  └── cfg.yml
```

> ⚠️ 两个文件必须放在一起，否则程序找不到配置会报错退出。

### 步骤 2：填写配置

用记事本（或任意文本编辑器）打开 `cfg.yml`，按下面说明填写。

### 步骤 3：双击运行

双击 `avatar.exe` 即可启动，会弹出一个窗口，里面站着 3D 数字人。

---

## 四、配置说明（cfg.yml）

下面是配置文件的内容和每一项的含义：

```yaml
asr:                              # 语音识别（听用户说话）
  url: "https://你的WorkspaceId.cn-beijing.maas.aliyuncs.com/compatible-mode/v1/chat/completions"
  model: "qwen3-asr-flash"

llm:                              # 大模型对话（生成回答）
  url: "https://你的WorkspaceId.cn-beijing.maas.aliyuncs.com/compatible-mode/v1/chat/completions"
  model: "qwen-plus"

tts:                              # 语音合成（把文字念出来）
  url: "https://dashscope.aliyuncs.com/api/v1/services/aigc/multimodal-generation/generation"
  model: "qwen3-tts-flash"
  voice: "Cherry"                 # 音色

api_key: "sk-你的百炼APIKey"      # 百炼 API Key
```

### 需要填写的内容

| 字段 | 说明 | 在哪里获取 |
|------|------|-----------|
| `WorkspaceId` | 业务空间 ID，出现在 asr/llm 的 url 里 | 百炼控制台 → 业务空间 |
| `api_key` | 百炼 API Key（`sk-` 开头） | [获取 API Key](https://help.aliyun.com/zh/model-studio/get-api-key) |
| `llm.model` | 对话模型名 | 默认 `qwen-plus`，可改 `qwen-max`、`qwen-turbo` 等 |
| `tts.voice` | 音色 | 可选 `Cherry`、`Stella`、`Luna`、`Bella`、`Nick`、`Ethan`、`Liam`、`Owen`、`Lily`、`Emily`、`Leo` 等 |

### 填写示例

假设你的 WorkspaceId 是 `abc123`，API Key 是 `sk-xxxxxxxxxxxx`：

```yaml
asr:
  url: "https://abc123.cn-beijing.maas.aliyuncs.com/compatible-mode/v1/chat/completions"
  model: "qwen3-asr-flash"

llm:
  url: "https://abc123.cn-beijing.maas.aliyuncs.com/compatible-mode/v1/chat/completions"
  model: "qwen-plus"

tts:
  url: "https://dashscope.aliyuncs.com/api/v1/services/aigc/multimodal-generation/generation"
  model: "qwen3-tts-flash"
  voice: "Cherry"

api_key: "sk-xxxxxxxxxxxx"
```

---

## 五、使用说明

### 开始对话

1. 启动后，屏幕中央会显示 **「点击屏幕开始对话」** 提示。
2. **点击屏幕任意位置**，或按 **空格键 / 回车键**。
3. 看到状态变成 **「聆听中...」** 后，**对着麦克风说话**。
4. 说完后 **停顿约 1.5 秒**，程序会自动停止录音。
5. 状态会依次经过 **「思考中...」** → 数字人开始说话。
6. 数字人说话时，屏幕底部会显示回复文字，嘴型同步变化。
7. 说完自动回到待机状态，可再次点击继续对话。

### 状态说明

| 屏幕提示 | 含义 |
|---------|------|
| （无提示，仅 3D 数字人） | 待机状态 |
| 聆听中... | 正在录音，请说话 |
| 思考中... | 正在识别/生成回答 |
| 底部字幕 + 嘴型动画 | 正在说话 |

### 退出

- 直接关闭窗口即可；或在启动它的命令行/终端按 `Ctrl + C`。

---

## 六、常见问题（FAQ）

### 1. 双击后没反应 / 闪退

- 确认 `cfg.yml` 和 `avatar.exe` 在同一个文件夹。
- 用记事本打开 `cfg.yml`，确认 `api_key` 和 url 里的 `WorkspaceId` 都填了，且没有拼错。
- 确认 `api_key` 前后有引号 `"..."`，冒号后有一个空格。

### 2. 报错「Failed to load config」

- 说明程序没找到 `cfg.yml`。请把它放到 exe 同目录，或当前工作目录。

### 3. 说话没反应 / 识别不到

- 检查麦克风是否可用（Windows 设置 → 隐私 → 麦克风，确认允许应用访问）。
- 确认录音时能看到「聆听中...」提示，且离麦克风近一点、说话清晰。

### 4. 数字人嘴型不张嘴 / 没有声音

- 检查扬声器是否正常、音量是否打开。
- 确认 `tts.voice` 音色名拼写正确（区分大小写，如 `Cherry`）。

### 5. 报错「HTTP 401 / 403」

- API Key 错误或过期。重新到百炼控制台获取并填入 `api_key`。

### 6. 报错「HTTP 400」

- 可能是 `WorkspaceId` 填错，或模型名不支持。检查 url 和 model。

### 7. 网络很慢，回答延迟高

- 这是在线服务，延迟受网络影响。数字人问答的典型延迟在 2~5 秒。
- 若大屏机器网络不稳定，建议接入有线网络。

---

## 七、交付清单

| 文件 | 说明 | 是否必填 |
|------|------|---------|
| `avatar.exe` | 主程序 | 无需改动 |
| `cfg.yml` | 配置文件 | **需要填写** WorkspaceId、API Key |

> 所有依赖（3D 模型、前端资源）都已打包进 `avatar.exe`，无需额外安装。

---

## 八、技术支持

- 阿里云百炼文档：<https://help.aliyun.com/zh/model-studio/>
- 获取 API Key：<https://help.aliyun.com/zh/model-studio/get-api-key>
- 获取 Workspace ID：<https://help.aliyun.com/zh/model-studio/obtain-the-app-id-and-workspace-id>
