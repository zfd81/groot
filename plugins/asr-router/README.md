# asr-router 插件

语音语种识别插件：接收一段音频，返回 ISO 639-1 语言码、可选方言与置信度。
推理使用 [`aufklarer/FireRedLID-887M-MLX-8bit`](https://huggingface.co/aufklarer/FireRedLID-887M-MLX-8bit)
（`FireRedTeam/FireRedLID` 的 MLX INT8 转换版本），revision 固定为
`1ae1296262c51cfd38de0f134011e6fa4b657543`。

## 平台要求

- 仅支持 **Apple Silicon 的 macOS**（Darwin + arm64）。MLX 无法在 Intel Mac、Linux
  或 Windows 上运行，其他平台启动时插件会加载失败，宿主与其余插件不受影响。
- 共享解释器须为 **Python 3.10 或更高**（`mlx` 的要求；宿主本身接受 3.9，但本插件装不上）。
- 输入可以是任意常见采样率的单声道或立体声音频；解码时统一重采样为 16kHz 单声道。
- 音频时长须在 1.5–35 秒之间。

## 安装依赖

先用 `groot plugins init` 创建 `{GROOT_HOME}/plugins/` 布局、共享虚拟环境并安装基础依赖
（需要 3.10+ 解释器时用 `--py` 指定）：

```bash
groot plugins init --py python3.12
```

该命令不会创建本插件目录，从源码树或发布包复制过去：

```bash
cp -r plugins/asr-router "$GROOT_HOME/plugins/"
```

插件与其它插件共用 `{GROOT_HOME}/plugins/.venv`，不要在本仓库根目录另建虚拟环境。
在共享环境中安装本插件的业务依赖：

```bash
"$GROOT_HOME/plugins/.venv/bin/pip" install -r "$GROOT_HOME/plugins/asr-router/requirements.txt"
```

## 下载模型

权重不进入代码仓库。模型目录必须包含 `config.json`、`cmvn.ark`、`dict.txt`、
`model.safetensors`、`frontend.py`、`mlx_model.py`：

```bash
huggingface-cli download aufklarer/FireRedLID-887M-MLX-8bit \
  --revision 1ae1296262c51cfd38de0f134011e6fa4b657543 \
  --local-dir "$GROOT_HOME/plugins/asr-router/models"
```

`GROOT_PLUGIN_MODELS_DIR` 可把模型目录指到仓库之外（例如已有的 Hugging Face 缓存快照），
便于多个部署共用同一份权重：

```bash
export GROOT_PLUGIN_MODELS_DIR=/path/to/fireredlid-mlx
```

未设置时使用 `{GROOT_HOME}/plugins/asr-router/models/`。目录不存在或缺少上述文件时，插件启动失败，
错误信息中会给出完整的下载命令。

## 音频格式与 ffmpeg

`wav`、`flac`、`ogg` 由 `soundfile` 直接解码，无需额外依赖。`mp3`、`m4a`、`webm`
需要系统中的 `ffmpeg` 可执行文件；缺少 `ffmpeg` 时这些格式返回 400
`unsupported_type`，其他格式不受影响。音频全程在内存中解码与重采样，不产生临时文件。

## 接口

```
POST /plugins/asr-router
Content-Type: multipart/form-data
```

字段 `file` 为音频文件。成功响应示例：

```json
{
  "language": "zh",
  "dialect": "mandarin",
  "confidence": 0.97,
  "alternatives": [
    {"language": "en", "confidence": 0.02}
  ],
  "duration_ms": 3200
}
```

无法判定语言时 `language` 为 `und` 且不带 `dialect`。权限点在 Groot 全局配置中设为 `chat`。
