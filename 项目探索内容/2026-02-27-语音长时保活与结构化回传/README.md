# 2026-02-27 语音长时保活与结构化回传

## 目标

- 把 `声音采集网页 + FunASR + 辅助服务` 视为前端整体（边缘节点）。
- 前端整体进行语音识别与门槛过滤后，再把结构化结果发送给 Go 后端。
- Go 后端接入 OpenAI 兼容 LLM（`/chat/completions` 流式），并按 `session_id` 维护进程内临时会话记忆（不接 Mem0）。
- Go 后端把 LLM 增量响应流式回传给前端显示，final 时再收口完整文本。
- 设计为长时间运行：FunASR 常驻、前后端长连接保活、断线自动重连。

## 当前状态（2026-02-27）

- 已完成：`Edge(FunASR)` -> `Go LLM` -> `Browser` 的端到端流式链路。
- 已完成：Go 后端接入 OpenAI 兼容流式接口，并实现按 `session_id` 的进程内临时记忆窗口（不接 Mem0）。
- 已完成：前端段级 `final` 聚合（`FINAL_MERGE_GAP_MS` + `FINAL_MERGE_MAX_MS`），降低“连续短句”高频触发。
- 已完成：LLM 打断机制（`pre_token` 与 `post_token` 条件打断）与请求队列（`BACKEND_MAX_PENDING`）。
- 已完成：语气词过滤（如“嗯/啊/yeah”等）不触发 LLM 请求，也不触发 post-token 打断。
- 已完成：连接稳定性增强
  - Edge->Go 心跳参数可配置，默认不因 pong 超时主动断链（`BACKEND_WS_PING_TIMEOUT_S=0`）。
  - Go 侧 WS 改为“读循环 + 处理循环”解耦，避免 LLM 执行期间阻塞读导致 keepalive 误判。
- 已完成：Docker 默认读取 `../../Soul/.env`，使用 Soul 中的 `OPENAI_API_KEY / OPENAI_BASE_URL / LLM_MODEL`。

## 核心设计决策（当前版本）

- 聚合位置放在 Edge（而不是 Go LLM）：
  - Edge 紧邻 ASR 事件，能直接利用 `final` 边界与时间间隔做低延迟聚合。
  - 聚合后再上送 Go，减少后端并发压力与无效请求。
- 打断策略采用“两段式”：
  - `pre_token`：首 token 前允许新语句打断并合并重提，避免回答过时问题。
  - `post_token`：默认条件打断（句长/疑问词），平衡“实时纠偏”和“回复稳定输出”。
- 无效短语治理放在提交前：
  - 通过 `FILTER_FILLER` 对语气词做过滤，避免频繁“空语义”触发请求/打断。
- 背压与稳定性优先：
  - Edge 维护后端请求队列上限（`BACKEND_MAX_PENDING`），队列满时缓冲聚合文本而不是直接丢失语句。
  - Go 的 WS 读写解耦后，即使 LLM 响应慢，连接层也能持续处理 ping/pong 与新入站消息。
- 记忆机制采用“轻量临时记忆”：
  - 只保留进程内窗口（`CHAT_HISTORY_LIMIT`），不引入外部记忆服务，降低系统复杂度与调试成本。

## 架构

```text
Browser Mic
   |
   | WebSocket (PCM16LE binary + flush/ping JSON)
   v
Edge Frontend (FastAPI + FunASR + Filter + Backend WS Bridge)
   |
   | WebSocket (structured JSON)
   v
Go LLM Backend (WS server + OpenAI-compatible streaming + in-memory session context)
   |
   | WebSocket response
   v
Edge Frontend -> Browser display
```

## 目录结构

```text
2026-02-27-语音长时保活与结构化回传/
  README.md
  docker-compose.yml
  models/
  edge-frontend/
    app.py
    requirements.txt
    Dockerfile
    web/index.html
  go-llm-backend/
    go.mod
    main.go
    Dockerfile
```

## 协议说明

### 1) Browser -> Edge Frontend

- WebSocket endpoint: `/ws/client`
- Binary frame: `16kHz PCM16LE mono`
- JSON control:
  - `{"event":"flush"}`
  - `{"event":"ping"}`

### 2) Edge Frontend -> Go Backend

- WebSocket endpoint: `ws://go-llm:8090/ws/edge`
- Request payload:

```json
{
  "type": "llm_request",
  "request_id": "req-xxxx",
  "session_id": "s-xxxx",
  "text": "明天天气怎么样",
  "emotion": "EMO_UNKNOWN",
  "event": "Speech",
  "final": true,
  "ts_ms": 1700000000000
}
```

### 3) Go Backend -> Edge Frontend

- Streaming chunk payload:

```json
{
  "type": "llm_stream",
  "request_id": "req-xxxx",
  "session_id": "s-xxxx",
  "delta": "明天",
  "final": false,
  "ts_ms": 1700000000020
}
```

- Final payload:

```json
{
  "type": "llm_response",
  "request_id": "req-xxxx",
  "session_id": "s-xxxx",
  "text": "明天天气怎么样",
  "emotion": "EMO_UNKNOWN",
  "event": "Speech",
  "final": true,
  "reply": "明天可能多云，最高气温 18℃ 左右，出门建议带一件薄外套。",
  "ts_ms": 1700000000100
}
```

- Error payload:

```json
{
  "type": "llm_error",
  "request_id": "req-xxxx",
  "session_id": "s-xxxx",
  "error": "OPENAI_API_KEY is required",
  "final": true,
  "ts_ms": 1700000000100
}
```

### 4) Edge Frontend -> Browser（后端工作状态）

- 状态事件 payload（用于前端展示 LLM 工作阶段）：

```json
{
  "event": "backend_state",
  "session_id": "s-xxxx",
  "request_id": "s-xxxx-r12",
  "stage": "thinking",
  "detail": "merge_reason=gap_or_window merge_count=2",
  "queue_size": 0,
  "ts_ms": 1700000000123
}
```

- `stage` 取值：
  - `queued`（已入队）
  - `queue_busy`（队列繁忙）
  - `thinking`（LLM推理中）
  - `streaming`（LLM输出中）
  - `completed`（回复完成）
  - `interrupting`（准备打断）
  - `interrupted`（已打断）
  - `timeout`（请求超时）
  - `failed`（请求失败）

## 门槛过滤（降后端压力）

前端整体在段级 ASR `final=true` 后，满足以下条件才上送后端：

- `SUBMIT_MIN_TEXT_CHARS`（最短文本长度，默认 `2`）
- `SUBMIT_REQUIRE_SPEECH=1` 时要求 `event == "Speech"`
- `SUBMIT_MIN_INTERVAL_MS`（两次上送最小间隔，默认 `600ms`）

## 长时保活设计

- Edge -> Backend 长连接：
  - `websockets` 心跳（ping/pong）
  - 断线自动重连（`BACKEND_RECONNECT_S`）
  - 请求超时与失败回告警
- Backend -> Edge 连接：
  - Go 侧主动 ping
  - 读写超时与 pong 刷新 deadline
  - LLM 侧支持 SSE 增量解析，边收边推给前端
- FunASR：
  - 模型常驻内存，避免请求级重载

## LLM 环境变量（Go 后端）

- `OPENAI_BASE_URL`：默认 `https://api.openai.com/v1`
- `OPENAI_API_KEY`：必填（用于真实 LLM 调用）
- `LLM_MODEL`：默认 `gpt-4o-mini`
- `LLM_TIMEOUT_S`：默认 `90`
- `CHAT_HISTORY_LIMIT`：默认 `20`（会话临时记忆窗口大小，单位=消息条数）
- `LLM_SYSTEM_PROMPT`：可选，覆盖默认系统提示词
- `BACKEND_REQ_TIMEOUT_S`：默认 `30`（Edge 等待后端首个/后续流片段超时）
- `BACKEND_MAX_PENDING`：默认 `8`（Edge 侧待发送到 LLM 的请求队列上限，防止高频语音堵塞主链路）
- `BACKEND_WS_PING_INTERVAL_S`：默认 `20`（Edge -> Go LLM 的心跳发送间隔）
- `BACKEND_WS_PING_TIMEOUT_S`：默认 `0`（`0` 表示不因 pong 超时主动断连；>0 表示超时秒数）
- `FINAL_MERGE_GAP_MS`：默认 `500`（相邻 final 间隔小于该值则继续合并）
- `FINAL_MERGE_MAX_MS`：默认 `2200`（单次合并窗口最大时长）
- `FILTER_FILLER`：默认 `1`（过滤“嗯/啊/yeah”等语气词，不触发 LLM 请求与打断）
- `FILLER_MAX_CHARS`：默认 `8`（语气词判定时允许的最大紧凑长度）
- `INTERRUPT_PRE_TOKEN`：默认 `1`（LLM 首 token 前允许被新语句打断并合并重提）
- `INTERRUPT_POST_TOKEN_MODE`：默认 `conditional`（`off|conditional|always`）
- `INTERRUPT_MIN_CHARS`：默认 `6`（`conditional` 模式下触发打断的最小新语句长度）

## 运行

```bash
cd /Users/zhangfeng/Desktop/Linux/DesktopRobot/项目探索内容/2026-02-27-语音长时保活与结构化回传
docker compose up -d --build
```

`docker-compose.yml` 已默认通过 `env_file: ../../Soul/.env` 读取 Soul 的环境变量（包含 `OPENAI_API_KEY`、`OPENAI_BASE_URL`、`LLM_MODEL`）。

- 前端页面：`http://127.0.0.1:18288`
- Go 后端健康检查：`http://127.0.0.1:18090/healthz`
- Edge 健康检查：`http://127.0.0.1:18288/healthz`

## 已知边界与后续建议

- 当前记忆为进程内内存；容器重启后会话历史会丢失（这是设计选择，不是故障）。
- `BACKEND_REQ_TIMEOUT_S` 仍是关键保护阈值；若模型首 token 偶发偏慢，建议适当提高到 `45~60s`。
- 语气词过滤是启发式规则；如业务上出现误杀，可通过 `FILTER_FILLER=0` 临时关闭或调大 `FILLER_MAX_CHARS`。
- 如后续接入意图识别/情感服务，建议保持“ASR事件过滤在 Edge，LLM推理在 Go”的职责边界不变。

## 常用调参

- `SUBMIT_MIN_TEXT_CHARS=4`
- `SUBMIT_MIN_INTERVAL_MS=1200`
- `SUBMIT_REQUIRE_SPEECH=1`
- `BACKEND_REQ_TIMEOUT_S=45`
- `BACKEND_MAX_PENDING=12`
- `BACKEND_WS_PING_TIMEOUT_S=0`
- `FILTER_FILLER=1`
- `FINAL_MERGE_GAP_MS=500`
- `FINAL_MERGE_MAX_MS=2200`
- `INTERRUPT_PRE_TOKEN=1`
- `INTERRUPT_POST_TOKEN_MODE=conditional`
- `INTERRUPT_MIN_CHARS=6`
- `VAD_CHUNK_MS=200`
- `MAX_SEGMENT_MS=30000`

示例：

```bash
SUBMIT_MIN_TEXT_CHARS=4 SUBMIT_MIN_INTERVAL_MS=1200 docker compose up -d --build
```
