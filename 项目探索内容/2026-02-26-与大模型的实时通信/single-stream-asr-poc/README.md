# Single Stream ASR POC

在探索目录中构建的单路流式 ASR 测试链路：

- 浏览器：采集麦克风，降采样为 `16kHz PCM16LE`，通过 `WebRTC DataChannel` 推流
- Go 服务：处理 WebRTC 信令与会话，接收音频分片并转发给流式 ASR
- ASR：默认通过 Python WebSocket 侧车（FunASR）实时识别，文本再经 DataChannel 回传前端

## 目录结构

```text
single-stream-asr-poc/
  cmd/server/main.go
  internal/asr/
    types.go
    mock.go
    ws_bridge.go
  web/index.html
  python/
    asr_bridge_funasr.py
    requirements.txt
  Dockerfile
  docker-compose.yml
  deploy-docker.sh
```

## 运行步骤

### 1) 启动 Python ASR 侧车（FunASR 实时识别）

```bash
cd /Users/zhangfeng/Desktop/Linux/DesktopRobot/项目探索内容/2026-02-26-与大模型的实时通信/single-stream-asr-poc/python
python3 -m venv .venv
source .venv/bin/activate
pip install -r requirements.txt
```

配置 FunASR 模型（示例）：

```bash
export FUNASR_MODEL=paraformer-zh-streaming
export FUNASR_HUB=ms
uvicorn asr_bridge_funasr:app --host 127.0.0.1 --port 2700
```

### 2) 启动 Go 服务

```bash
cd /Users/zhangfeng/Desktop/Linux/DesktopRobot/项目探索内容/2026-02-26-与大模型的实时通信/single-stream-asr-poc
go mod tidy
go run ./cmd/server -addr :8088 -asr bridge -bridge-url ws://127.0.0.1:2700/ws
```

- `-asr bridge`: 仅桥接 ASR（严格真实识别）

### 3) 打开页面测试

访问：`http://127.0.0.1:8088`

点击“开始采集”后，可看到实时识别文本回传。

## 接口说明

- `POST /offer`
  - 请求：浏览器 SDP offer
  - 响应：服务端 SDP answer + `session_id` + `asr_mode`
- `GET /healthz`
  - 返回服务存活状态

## 注意事项

- 当前 WebRTC 使用 DataChannel 传输原始 PCM，便于快速联调 ASR，不涉及 Opus 解码。
- 如需升级为媒体轨道（AudioTrack）上行，可在后续版本切换到 RTP/Opus 解码链路。

## Docker 部署（固定端口）

### 1) 一键部署

```bash
cd /Users/zhangfeng/Desktop/Linux/DesktopRobot/项目探索内容/2026-02-26-与大模型的实时通信/single-stream-asr-poc
./deploy-docker.sh
```

- 默认固定 `WEB_PORT=18188` 并映射到容器 `8088`。
- 默认 `ASR_MODE=bridge` + `STRICT_MODEL=1`：强制使用真实流式 ASR，不使用 mock。
- 脚本默认注入本地代理（宿主机 `127.0.0.1:7897`，容器内 `host.docker.internal:7897`）。
- 默认使用 `FUNASR_MODEL=paraformer-zh-streaming`（可通过环境变量覆盖）。
- 默认固定 `ICE_UDP_PORT=19188`，并映射同名 UDP 端口用于 WebRTC ICE。

### 2) 访问

打开：`http://127.0.0.1:18188`

说明：WebRTC 会使用 `ICE_UDP_PORT`（宿主机回环地址）进行连通。

### 3) 停止与查看

```bash
docker compose ps
docker compose logs -f go-server asr-bridge
docker compose down
```

### 4) 自定义 FunASR 模型

模型缓存会持久化在：

```text
single-stream-asr-poc/models/
```

然后重启：

```bash
FUNASR_MODEL=paraformer-zh-streaming FUNASR_HUB=ms ASR_MODE=bridge STRICT_MODEL=1 ./deploy-docker.sh
```

如果模型加载失败，`asr-bridge` 会启动失败（严格模式），不会回退到 mock。
