# 2026-02-28 千问 VL 模型使用探索（qwen3-vl-plus）

## 目标

- 在现有 OpenAI 兼容调用链路上验证 `qwen3-vl-plus` 的图文理解能力。
- 提供可复用的最小调用脚本（支持图片 URL 和本地图片）。

## 今日产出

- 能力/用途/性能/使用报告：
  - [/Users/zhangfeng/Desktop/Linux/DesktopRobot/项目探索内容/2026-02-28-千问VL模型使用探索/2026-02-28-qwen3-vl-plus-能力用途性能使用报告.md](/Users/zhangfeng/Desktop/Linux/DesktopRobot/项目探索内容/2026-02-28-千问VL模型使用探索/2026-02-28-qwen3-vl-plus-能力用途性能使用报告.md)

## 模型与接口配置

- 主机 URL：`https://api.newcoin.top/v1`
- 模型名称：`qwen3-vl-plus`
- 调用方式：OpenAI 兼容 `POST /chat/completions`

建议环境变量：

```bash
export OPENAI_BASE_URL="https://api.newcoin.top/v1"
export OPENAI_API_KEY="你的密钥"
export LLM_MODEL="qwen3-vl-plus"
```

## 最小脚本

脚本路径：

`/Users/zhangfeng/Desktop/Linux/DesktopRobot/项目探索内容/2026-02-28-千问VL模型使用探索/scripts/qwen3_vl_plus_demo.py`

### 1) 使用网络图片 URL

```bash
cd /Users/zhangfeng/Desktop/Linux/DesktopRobot/项目探索内容/2026-02-28-千问VL模型使用探索
python3 scripts/qwen3_vl_plus_demo.py \
  --image-url "https://modelscope.oss-cn-beijing.aliyuncs.com/resource/qwen.png" \
  --prompt "请描述这张图片并给出3个关键点。"
```

### 2) 使用本地图片

```bash
cd /Users/zhangfeng/Desktop/Linux/DesktopRobot/项目探索内容/2026-02-28-千问VL模型使用探索
python3 scripts/qwen3_vl_plus_demo.py \
  --image-path "/绝对路径/你的图片.jpg" \
  --prompt "这张图里有什么？请用中文简洁回答。"
```

## 说明

- 脚本默认读取：`OPENAI_BASE_URL`、`OPENAI_API_KEY`、`LLM_MODEL`。
- 可通过参数覆盖：`--base-url`、`--api-key`、`--model`。
- 若接口返回非 JSON（例如 HTML），通常是 base URL 路径错误；请确认是 `.../v1`。

## 下一步建议

- 接入流式输出（`stream=true`）观察首 token 延迟。
- 扩展为多图输入与结构化输出（JSON schema）。
- 与语音项目联动：ASR 文本 + 屏幕截图共同输入 `qwen3-vl-plus`。
