# Soul API 文档（场景化）

更新时间：2026-02-16  
适用环境：本地 Docker 部署（`soul-server + terminal-web + mem0 + mqtt + postgres`）

## 1. 服务入口

- `soul-server`：`http://localhost:9010`
- `terminal-web`：`http://localhost:9011`
- `mem0`：`http://localhost:18000`
- `mqtt broker`：`tcp://localhost:1883`
- 默认 `MQTT_TOPIC_PREFIX=soul`

## 2. 角色与调用关系

- App/前端可以直接调用 `soul-server /v1/chat`。
- 调试场景下，建议调用 `terminal-web /ask`，它会自动补全 `user_id/terminal_id/soul_hint` 并转发到 `soul-server`。
- 真实终端通过 MQTT 上报技能与在线状态；`soul-server` 通过 MQTT 下发技能调用。

## 3. HTTP API

## 3.1 `soul-server`

### `GET /healthz`

用途：健康检查。  
响应：

```json
{"ok": true}
```

### `POST /v1/chat`

用途：核心对话入口，触发“记忆注入 + LLM + 技能调用”流程。  
请求体：

```json
{
  "user_id": "demo-user",
  "session_id": "s1",
  "terminal_id": "terminal-debug-01",
  "soul_hint": "friendly",
  "message": "2+2=4，对吗？"
}
```

字段说明：

- `session_id`：必填，会话ID。
- `terminal_id`：必填，终端ID。
- `message`：必填，用户输入。
- `user_id`：可选，不传则使用服务默认用户。
- `soul_hint`：可选，仅在该终端首次绑定时参与匹配/创建灵魂。

成功响应：

```json
{
  "session_id": "s1",
  "terminal_id": "terminal-debug-01",
  "soul_id": "soul_d6343bea67b04027be636c8786ce8ca7",
  "reply": "是的，2+2=4。",
  "executed_skills": ["light_green"]
}
```

失败响应（示例）：

```json
{"error":"..."}
```

## 3.2 `terminal-web`（调试终端服务）

### `GET /healthz`

用途：健康检查。  
响应：

```json
{"ok": true}
```

### `GET /state`

用途：查看当前调试终端状态。  
响应示例：

```json
{
  "terminal_id": "terminal-debug-01",
  "soul_hint": "",
  "skill_version": 1,
  "color": "red",
  "last_action": "亮红灯",
  "updated_at": "2026-02-16T10:56:55.93528771Z",
  "logs": [
    "2026-02-16T10:55:31Z -> 绿灯已点亮",
    "2026-02-16T10:56:55Z -> 红灯已点亮"
  ]
}
```

### `POST /report-skills`

用途：手工触发重新上报技能快照到 MQTT。  
请求体：无。  
响应：

```json
{"ok": true}
```

### `POST /ask`

用途：调试页入口，内部转发到 `soul-server /v1/chat`。  
请求体：

```json
{
  "session_id": "s1",
  "message": "地球绕太阳公转，这句话正确吗？"
}
```

响应：与 `soul-server /v1/chat` 基本一致（透传）。

### `GET /`

用途：调试页面（浏览器访问）。

## 3.3 `mem0`（内部记忆服务）

推荐直接看 OpenAPI：`http://localhost:18000/docs`。  
本项目主要用到：

- `POST /memories`
- `POST /search`

说明：`soul-server` 已封装对 mem0 的调用，业务侧通常不直接调用 mem0。

## 4. MQTT 协议

## 4.1 Topic 约定（prefix=`soul`）

- 终端上报技能：`soul/terminal/{terminalId}/skills`
- 终端在线状态：`soul/terminal/{terminalId}/online`
- 终端心跳：`soul/terminal/{terminalId}/heartbeat`
- 服务端下发调用：`soul/terminal/{terminalId}/invoke/{requestId}`
- 终端回传结果：`soul/terminal/{terminalId}/result/{requestId}`

## 4.2 Payload 结构

### 技能快照（终端 -> 服务）

```json
{
  "terminal_id": "terminal-debug-01",
  "soul_hint": "friendly",
  "skill_version": 2,
  "skills": [
    {
      "name": "light_green",
      "description": "亮绿灯",
      "input_schema": {
        "type": "object",
        "properties": {},
        "required": []
      }
    }
  ]
}
```

### 技能调用（服务 -> 终端）

```json
{
  "request_id": "uuid",
  "skill": "light_green",
  "arguments": {}
}
```

### 技能结果（终端 -> 服务）

```json
{
  "request_id": "uuid",
  "ok": true,
  "output": "绿灯已点亮"
}
```

## 5. 场景用法

## 场景A：设备第一次接入（终端首次绑定灵魂）

1. 终端连接 MQTT，发布 `online=online`。  
2. 终端发布 `skills`（可附 `soul_hint`）。  
3. `soul-server` 收到后执行“匹配或创建灵魂”，写入 `(user_id, terminal_id) -> soul_id` 绑定。  
4. 后续同终端重连将复用同一 `soul_id`。

## 场景B：用户发起“对错判断”对话（触发点灯）

1. 调用 `POST /ask` 或 `POST /v1/chat`。  
2. 系统将当前终端技能注入 LLM tools。  
3. 若 LLM 判断“正确/认同” -> 调 `light_green`。  
4. 若 LLM 判断“错误/不认同” -> 调 `light_red`。  
5. 返回 `reply + executed_skills`。

## 场景C：普通文本回复（不触发技能）

1. 发起普通问答（不涉及动作）。  
2. 若 LLM 未返回 tool call，系统直接返回文本 `reply`。  
3. 响应中可能没有 `executed_skills` 字段。

## 场景D：终端技能升级

1. 终端升级后递增 `skill_version` 并重新上报 `skills`。  
2. 服务端只接受更新版本；低版本快照不会覆盖高版本能力。  
3. 技能定义不落业务数据库，由终端快照驱动。

## 场景E：Mem0 必选策略验证

1. `mem0` 正常时，对话可用，且记忆可写入/检索。  
2. `mem0` 不可达时，请求直接失败（不做降级）。

## 6. 快速测试命令

### 6.1 绿灯（正确语句）

```bash
curl -X POST http://localhost:9011/ask \
  -H 'content-type: application/json' \
  -d '{"session_id":"acc-green","message":"地球绕太阳公转，这句话正确吗？"}'
```

### 6.2 红灯（错误语句）

```bash
curl -X POST http://localhost:9011/ask \
  -H 'content-type: application/json' \
  -d '{"session_id":"acc-red","message":"太阳绕地球公转，这句话正确吗？"}'
```

### 6.3 查看终端状态

```bash
curl http://localhost:9011/state
```

### 6.4 模型可达性测试

```bash
python3 /Users/zhangfeng/Desktop/Linux/DesktopRobot/Soul/scripts/test_model_reachability.py \
  --base-url https://api.newcoin.tech/v1 \
  --model doubao-seed-1-6-251015 \
  --api-key '<YOUR_API_KEY>'
```
