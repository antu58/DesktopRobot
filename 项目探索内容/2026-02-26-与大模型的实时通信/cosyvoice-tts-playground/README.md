# CosyVoice TTS Playground

独立 CosyVoice 测试服务与网页，用于快速验证：

1. 预置 100-200 字短文合成。
2. 预训练音色下拉选择。
3. 音色复刻（上传参考音频创建 `clone_id`，支持 `m4a`）。
4. 复刻音色复用（按 `clone_id` 持续合成）。
5. 输出音色、采样率、音频时长、合成耗时。

## 启动

```bash
cd /Users/zhangfeng/Desktop/Linux/DesktopRobot/项目探索内容/2026-02-26-与大模型的实时通信/cosyvoice-tts-playground
./deploy.sh
```

默认固定端口：`18388`

部署脚本默认使用镜像缓存，不强制重建。仅在需要时手动重建：

```bash
FORCE_REBUILD=1 ./deploy.sh
```

访问：

`http://127.0.0.1:18388`

流式分片测试页：

`http://127.0.0.1:18388/stream-test`

说明：`/stream-test` 现为真流式测试页，使用单次请求读取 `/api/synthesize/clone/stream` 的 PCM chunked 音频流并边收边播。

提示：

1. 当前默认模型为 `iic/CosyVoice-300M-SFT`。
2. 如果你重点测试复刻能力，可尝试改为 `COSY_MODEL_DIR=iic/CosyVoice-300M ./deploy.sh`。
3. 参考音频建议 3-10 秒、单人声、安静环境，且 `prompt_text` 与音频内容尽量一致。

## 结构

```text
cosyvoice-tts-playground/
  app/server.py
  web/index.html
  requirements-cpu.txt
  Dockerfile
  docker-compose.yml
  deploy.sh
```

## 接口

1. `GET /api/healthz`：服务健康状态
2. `GET /api/voices`：预置音色与 clone 列表
3. `POST /api/synthesize`：文本转语音（返回 `audio/wav`）
4. `GET /api/clones`：已保存 clone 元信息
5. `POST /api/clones`：创建/覆盖 clone（`application/json`，音频用 base64）
6. `POST /api/synthesize/clone`：用 clone_id 合成（返回 `audio/wav`）
7. `POST /api/synthesize/clone/stream`：用 clone_id 真流式合成（`chunked` + `pcm_s16le`）
