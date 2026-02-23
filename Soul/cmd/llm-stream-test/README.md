# llm-stream-test

一个独立的测试服务，支持：

- 文本单轮流式聊天（无上下文记忆）
- 语音输入：按住空格说话，前端使用 Web Speech API 转文本后请求 LLM 流式返回

## 运行

```bash
cd /Users/zhangfeng/Desktop/Linux/DesktopRobot/Soul
go run ./cmd/llm-stream-test
```

默认会尝试读取以下配置（显式环境变量优先）：

- `.env`
- `Soul/.env`（当你在仓库根目录启动时）

需要的变量：

- `OPENAI_API_KEY`（必填）
- `OPENAI_BASE_URL`（默认 `https://api.openai.com/v1`）
- `LLM_MODEL` 或 `MODEL`（默认 `gpt-4o-mini`）
- `TEST_CHAT_ADDR`（默认 `:9014`）
- `ASR_BASE_URL` / `ASR_API_KEY` / `ASR_MODEL` / `ASR_LANGUAGE` / `VAD_UDP_ADDR`：仅后端语音链路调试时使用，Web Speech 模式不依赖这些配置

启动后打开：

- [http://localhost:9014](http://localhost:9014)

## 使用说明

### 文本模式

- 每次请求只发送当前输入，不带历史消息。
- 后端调用 `POST {OPENAI_BASE_URL}/chat/completions`，参数 `stream=true`。
- 页面会显示首字延迟（TTFT）和总耗时。

### 语音模式

- 在页面按住 `空格` 开始说话，松开空格结束。
- 浏览器本地通过 Web Speech API 做语音转文本，再把文本发给 `/api/chat/stream`，由后端调用 `chat/completions` 流式输出。
- 若浏览器不支持 Web Speech API（建议 Chrome），页面会提示不支持。
