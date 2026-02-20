# API 文档（Soul 服务）

更新时间：2026-02-20  
状态：`实施中（Phase 1）`

## 1. 文档定位

本文档是 Soul 侧 API 的主文档（字段级定义）。  
通信 topic 与消息规范见：`../../doc/通信协议-v2.md`。

## 2. 服务入口（本地默认）

- `soul-server`：`http://localhost:9010`
- `terminal-web`：`http://localhost:9011`

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

成功响应：

```json
{
  "session_id": "s1",
  "terminal_id": "terminal-001",
  "soul_id": "soul_xxx",
  "reply": "是的，2+2=4。",
  "executed_skills": ["light_green"],
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

用途：查看调试终端状态（灯色、最后动作、日志）。

## 4.3 `POST /report-skills`

用途：手工触发重新上报技能快照。  
请求体：无。

## 4.4 `POST /ask`

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

## 4.5 `GET /`

用途：调试页面。

## 5. 关联文档

- Soul 设计目标：`设计目标.md`
- 全局通信协议：`../../doc/通信协议-v2.md`
