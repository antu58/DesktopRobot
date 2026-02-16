# Soul 服务集

该目录包含两个独立服务：
- `soul-server`：Go 后端（LLM访问层 + 记忆注入层 + MQTT通信层 + 技能注入层）
- `terminal-web`：Web 调试终端（上报 `light_red`/`light_green` 两个技能，并执行点灯）

## 端口
- `soul-server`：`9010`
- `terminal-web`：`9011`
- `mem0`：`18000`

## 配置
1. 编辑 `.env`，至少填入真实 `OPENAI_API_KEY`（或切换 `LLM_PROVIDER=claude` 并填 `ANTHROPIC_API_KEY`）。
2. Docker Compose 会从 `.env` 读取必要配置。
3. 可选配置 `TERMINAL_SOUL_HINT`：终端首次连接时用于匹配已有灵魂（可填 `soul_id` 或 `name`）。
4. `TERMINAL_SKILL_VERSION` 每次躯体技能升级后递增；服务端只接受新版本技能快照。
5. `SKILL_SNAPSHOT_TTL_SECONDS` 控制技能快照过期时间，依赖终端 heartbeat 自动续期。
6. Mem0 语义记忆为默认必需组件（主存储仍是 PostgreSQL，不做降级）。

## 启动
```bash
docker compose up --build
```

## 关键环境变量
- `TERMINAL_SKILL_VERSION`: 终端技能版本号，升级技能后递增。
- `TERMINAL_HEARTBEAT_INTERVAL_SECONDS`: 终端 heartbeat 间隔。
- `SKILL_SNAPSHOT_TTL_SECONDS`: 服务端技能快照 TTL，超时后不再注入技能。
- `MEM0_BASE_URL`: Mem0 服务地址（默认 `http://localhost:8000`）。
- `MEM0_API_KEY`: Mem0 API Key（如部署配置要求）。
- `MEM0_TOP_K`: 每次语义检索返回条数。

## 调试
- 访问终端页面：`http://localhost:9011`
- 访问 Mem0 文档：`http://localhost:18000/docs`
- 页面输入会调用 `soul-server /v1/chat`
- 若 LLM 判断用户说法正确，会触发 `light_green`；否则触发 `light_red`
- 首次终端连接时会自动“匹配或创建”灵魂，并把 `terminal_id + soul_id` 持久绑定；后续重连复用同一组合
- 终端每 `TERMINAL_HEARTBEAT_INTERVAL_SECONDS` 发布 heartbeat；若超出 TTL 则该终端技能自动失效

## API 示例
```bash
curl -X POST http://localhost:9010/v1/chat \
  -H 'Content-Type: application/json' \
  -d '{
    "session_id": "s1",
    "terminal_id": "terminal-debug-01",
    "soul_hint": "friendly",
    "message": "2+2=4"
  }'
```
