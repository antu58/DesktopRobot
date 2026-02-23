# Soul 服务集

`Soul` 目录包含：

- `soul-server`：主服务（会话编排、LLM 调用、技能调度、摘要压缩）
- `emotion-server`：情感理解子服务（Python + mDeBERTa-XNLI + ONNX Runtime int8，PAD 三轴直推；输出主情绪 + PAD）
- `intent-filter`：意图筛选子服务（Python，输入意图表 + 命令上下文，输出多意图数组与固定参数结构）
- `persona-model`：已并入 `soul-server`（MBTI -> 人格向量 T，动态 PAD，执行概率门控）

## 端口（本地默认）

- `soul-server`：`9010`
- `emotion-server`：`9012`
- `intent-filter`：`9013`
- `mem0`：`18000`

## 启动

```bash
docker compose up --build
```

如果只本地跑情感子服务（不走 compose）：

```bash
cd /Users/zhangfeng/Desktop/Linux/DesktopRobot/Soul
python3 -m venv .venv-emotion
source .venv-emotion/bin/activate
pip install -r emotion-server-py/requirements.txt
uvicorn emotion-server-py.app:app --host 0.0.0.0 --port 9012
```

验证：

```bash
curl -sS -X POST http://127.0.0.1:9012/v1/emotion/analyze \
  -H 'content-type: application/json' \
  -d '{"text":"今天被老板批评了"}' | jq
```

PAD 对照表：

```bash
curl -sS http://127.0.0.1:9012/v1/emotion/pad-table | jq
```

如果只本地跑意图筛选子服务（不走 compose）：

```bash
cd /Users/zhangfeng/Desktop/Linux/DesktopRobot/Soul/intent-filter-py
python3 -m venv .venv
source .venv/bin/activate
pip install -r requirements.txt
uvicorn app:app --host 0.0.0.0 --port 9013
```

验证：

```bash
curl -sS -X POST http://127.0.0.1:9013/v1/intents/filter \
  -H 'content-type: application/json' \
  -d '{"command":"帮我关灯并且提醒我10分钟后取快递","intent_catalog":[{"id":"light_off","match":{"keywords_any":["关灯"]}},{"id":"reminder_create","match":{"keywords_any":["提醒"]}}]}' | jq
```

说明：

- 首次调用 `analyze` 会下载模型权重，耗时会明显高于后续调用。
- 服务启动时会自动做 ONNX 导出/量化（首次）并执行一次预热推理，避免首轮业务请求冷启动。
- 模型会缓存到宿主机目录 `EMOTION_MODEL_CACHE_DIR`（默认 `./.cache/huggingface`），容器重建后不会重复下载。
- 缓存目录已在 `Soul/.gitignore` 中忽略，不会被提交到 Git。

## 灵魂人格模型（v2）

- 新增灵魂接口：
  - `GET /v1/souls`
  - `POST /v1/souls`
  - `POST /v1/souls/select`
- `POST /v1/chat` 现支持 `keyboard_text` 与 `speech_text`。
- 主链路增加：用户情绪分析 -> 灵魂 PAD 更新 -> MQTT 下发 `emotion_update` -> intent-filter -> MQTT 下发 `intent_action`。
- 服务端会按 `EMOTION_TICK_INTERVAL_SECONDS`（默认 3 秒，限制 2~5 秒）执行一次“自然演化 + 持久化 + MQTT emotion_update 下发”，避免端侧情绪显示长时间停留。
历史数据清理（一次性）：

```bash
cd /Users/zhangfeng/Desktop/Linux/DesktopRobot/Soul
./scripts/reset_soul_history.sh
```

## 关键说明

- 技能能力来自终端 `skills` 快照，支持 `skill_version` 递增。
- 对话主链路不依赖 Mem0 同步读写。
- 会话活跃由 `/v1/chat` 输入驱动，3 分钟无新输入触发空闲总结。

## 文档

- 设计目标：`docs/设计目标.md`
- 技术调研：`docs/技术调研.md`
- 情感方案沉淀：`docs/情感理解-PAD方案沉淀.md`
- API：`docs/API文档-Soul服务.md`
- 人格系统方案（v2）：`docs/灵魂人格系统-v2-设计方案.md`
- 全局通信协议：`../doc/通信协议-v2.md`
