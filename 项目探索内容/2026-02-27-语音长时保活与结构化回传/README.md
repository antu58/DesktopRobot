# 2026-02-27 语音长时保活与结构化回传

## 目标

- 把 `声音采集网页 + FunASR + 辅助服务` 视为前端整体（边缘节点）。
- 前端整体进行语音识别与门槛过滤后，再把结构化结果发送给 Go 后端。
- Go 后端预留 LLM 入口（当前使用 `LLM_STUB` 拼接回包），再回传前端显示。
- 设计为长时间运行：FunASR 常驻、前后端长连接保活、断线自动重连。

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
Go LLM Backend (WS server, currently LLM_STUB)
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

- Response payload:

```json
{
  "type": "llm_response",
  "request_id": "req-xxxx",
  "session_id": "s-xxxx",
  "text": "明天天气怎么样",
  "emotion": "EMO_UNKNOWN",
  "event": "Speech",
  "final": true,
  "reply": "明天天气怎么样 [LLM_STUB emo=EMO_UNKNOWN event=Speech final=true]",
  "ts_ms": 1700000000100
}
```

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
- FunASR：
  - 模型常驻内存，避免请求级重载

## 运行

```bash
cd /Users/zhangfeng/Desktop/Linux/DesktopRobot/项目探索内容/2026-02-27-语音长时保活与结构化回传
docker compose up -d --build
```

- 前端页面：`http://127.0.0.1:18288`
- Go 后端健康检查：`http://127.0.0.1:18090/healthz`
- Edge 健康检查：`http://127.0.0.1:18288/healthz`

## 常用调参

- `SUBMIT_MIN_TEXT_CHARS=4`
- `SUBMIT_MIN_INTERVAL_MS=1200`
- `SUBMIT_REQUIRE_SPEECH=1`
- `VAD_CHUNK_MS=200`
- `MAX_SEGMENT_MS=30000`

示例：

```bash
SUBMIT_MIN_TEXT_CHARS=4 SUBMIT_MIN_INTERVAL_MS=1200 docker compose up -d --build
```
