# API 文档（Soul 服务）

更新时间：2026-02-22  
状态：`实施中（Phase 1）`

## 1. 文档定位

本文档是 Soul 侧 API 的主文档（字段级定义）。  
通信 topic 与消息规范见：`../../doc/通信协议-v2.md`。

## 2. 服务入口（本地默认）

- `soul-server`：`http://localhost:9010`
- `terminal-web`：`http://localhost:9011`
- `emotion-server`：`http://localhost:9012`

## 3. `soul-server`

## 3.1 `GET /healthz`

用途：健康检查。

响应：

```json
{"ok": true}
```

## 3.2 `POST /v1/chat`

用途：主对话入口（摘要注入 + LLM + 技能调度）。

请求体：

```json
{
  "user_id": "demo-user",
  "session_id": "s1",
  "terminal_id": "terminal-001",
  "soul_hint": "friendly",
  "inputs": [
    {
      "input_id": "in-001",
      "type": "keyboard_text",
      "source": "keyboard",
      "text": "2+2=4，对吗？"
    }
  ]
}
```

字段说明：

- `session_id`：必填。
- `terminal_id`：必填。
- `inputs`：必填，至少 1 项。
- `user_id`：可选，不传使用服务默认用户。
- `soul_hint`：可选，仅首次绑定时参与匹配/创建。

输入类型（协议支持）：

- `keyboard_text`、`speech_text`、`presence`、`sensor_state`、`audio`、`image`、`video`、`event_note`

当前 Phase 1 限制：

- 至少存在 1 条非空 `keyboard_text`。
- 非 `keyboard_text` 输入当前不进入主回复推理。

会话计时规则：

- 每次成功写入用户输入（`role=user`）重置 3 分钟空闲计时。
- 连续 3 分钟无新输入触发空闲总结。

技能调度规则（当前实现）：

- 默认：单次 LLM，直接选择终端技能并执行。
- 同一轮可调用多个终端技能（若模型返回多个非冲突 tool calls）。
- 特殊：若首轮选择内置 `recall_memory`（Mem0 历史回顾），服务端先向终端发送 `status=mem0_searching`，查询后进行第二次 LLM，再执行终端技能。
- `recall_memory` 仅在 Mem0 就绪时暴露给模型；Mem0 未就绪时不会触发该分支。
- `executed_skills` 可能包含 `recall_memory`。

成功响应：

```json
{
  "session_id": "s1",
  "terminal_id": "terminal-001",
  "soul_id": "soul_xxx",
  "reply": "是的，2+2=4。",
  "executed_skills": ["recall_memory", "light_green"],
  "context_summary": "用户持续进行基础事实问答，机器人保持简洁确认式回应。"
}
```

典型失败响应：

```json
{"error":"inputs is required"}
```

```json
{"error":"currently only input.type=keyboard_text with non-empty text is supported"}
```

## 4. `terminal-web`（调试服务）

## 4.1 `GET /healthz`

```json
{"ok": true}
```

## 4.2 `GET /state`

用途：查看调试终端状态（灯色、最后动作、日志、当前会话、多轮对话片段）。

新增字段（调试态）：

- `active_session_id`：当前活动会话 ID。
- `sessions`：已出现的会话 ID 列表。
- `conversation_turns`：当前活动会话的轮次消息（user/assistant）。
- `expression`：机器人当前表情（`微笑`/`大笑`/`生气`/`哭`/`不开心`）。
- `head_pose`：机器人头部朝向（`中位`/`抬头`/`低头`/`左看`/`右看`）。
- `head_motion`：当前进行中的头部动态动作（`点头`/`摇头`；无则空）。
- `head_motion_duration_seconds`：当前头部动态动作持续时长（秒）。

## 4.3 `POST /session/new`

用途：创建并切换到新会话（便于多轮联调）。

响应：

```json
{
  "ok": true,
  "session_id": "s-1771650269287667421"
}
```

## 4.4 `POST /report-skills`

用途：手工触发重新上报技能快照。  
请求体：无。

当前终端默认上报 4 个技能：

- `light_red`：亮红灯（否定/错误判断）。
- `light_green`：亮绿灯（肯定/正确判断）。
- `set_expression`：设置表情，参数 `emotion` 枚举：`微笑`/`大笑`/`生气`/`哭`/`不开心`。
- `set_head_motion`：头部动作，参数 `action` 枚举：`抬头`/`低头`/`左看`/`右看`/`点头`/`摇头`；`duration_seconds` 为可选持续时间（0.2~10，主要用于点头/摇头）。

## 4.5 `POST /ask`

用途：调试页入口，内部转发到 `soul-server /v1/chat`。

请求体示例：

```json
{
  "session_id": "s1",
  "inputs": [
    {
      "type": "keyboard_text",
      "source": "keyboard",
      "text": "地球绕太阳公转，这句话正确吗？"
    }
  ]
}
```

说明：

- `session_id` 可选；为空时使用当前活动会话。
- `terminal-web` 会自动补齐 `inputs[].input_id` 和 `inputs[].ts`（如调用方未提供）。
- 每轮发送前会重置灯态为 `off`，若本轮返回未包含亮灯技能，则保持不亮灯。

## 4.6 `GET /`

用途：调试页面。

## 5. `emotion-server`（情感理解子服务）

> 设计目标：作为主服务可复用的“情感理解网关”，固定输出 `emotion + PAD + intensity`。  
> 情绪模式固定为 `compact`（不提供切换配置）。

## 5.1 `GET /healthz`

用途：检查 emotion-server 状态（纯 Go 本地分析引擎）。

成功响应：

```json
{
  "ok": true,
  "schema": "compact",
  "engine": "go-lexical-v1",
  "labels": ["anger", "anxiety", "boredom", "calm", "confusion", "disappointment", "disgust", "embarrassment", "excitement", "fear", "frustration", "gratitude", "guilt", "hope", "joy", "neutral", "pride", "relief", "resignation", "sadness", "surprise"]
}
```

## 5.2 `GET /v1/emotion/pad-table`

用途：查看固定 `compact` 模式下的 PAD 对照表。

## 5.3 `POST /v1/emotion/analyze`

用途：输入文本，返回情感标签与 PAD。

请求体：

```json
{
  "text": "今天被老板批评了"
}
```

响应体：

```json
{
  "emotion": "frustration",
  "p": -0.52,
  "a": 0.58,
  "d": -0.08,
  "intensity": 0.973075,
  "latency_ms": 18.234
}
```

说明：

- `latency_ms` 为 emotion-server 单次处理耗时。
- 不做统一阈值截断，保留原始强度分布。

## 5.4 `POST /v1/emotion/convert`

用途：将 `{emotion, confidence}` 转换成 `{emotion, p, a, d, intensity}`。

请求体：

```json
{
  "emotion": "sadness",
  "confidence": 0.91
}
```

响应体：

```json
{
  "emotion": "sadness",
  "p": -0.65,
  "a": -0.15,
  "d": -0.35,
  "intensity": 0.91,
  "latency_ms": 2.019
}
```

## 6. 关联文档

- Soul 设计目标：`设计目标.md`
- LLM 请求规范：`LLM请求规范.md`
- 全局通信协议：`../../doc/通信协议-v2.md`
