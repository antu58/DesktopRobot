# LLM 请求规范（Soul）

更新时间：2026-02-21

## 1. 目标

规范 Soul 发往 LLM 的请求结构，确保：

- 系统提示词不写死具体技能名；
- 历史会话以压缩摘要注入；
- 工具使用规则以通用策略 + `tools.description` 共同约束；
- 支持单轮多工具调用（非互斥时）。

## 2. 请求结构

Soul 使用 OpenAI 兼容 `/chat/completions`：

```json
{
  "model": "<LLM_MODEL>",
  "messages": [
    {"role": "system", "content": "<system_prompt>"},
    {"role": "user", "content": "<latest_user_text>"},
    {"role": "assistant", "content": "..."},
    {"role": "tool", "name": "...", "tool_call_id": "...", "content": "..."}
  ],
  "tools": [
    {
      "type": "function",
      "function": {
        "name": "skill_name",
        "description": "用途/触发条件/冲突规则/是否可并行",
        "parameters": {"type":"object","properties":{},"required":[]}
      }
    }
  ],
  "tool_choice": "auto"
}
```

说明：

- `latest_user_text` 通过 `messages.role=user` 传递，不在 system 重复注入。
- system 只放稳定规则与上下文摘要（灵魂画像、压缩摘要、本轮观测）。
- 若 Mem0 就绪，首轮 `tools` 可额外包含 `recall_memory`。

## 3. System 提示词模板

```text
你是单用户桌面机器人编排助手。你只能使用本轮请求提供的 tools 执行动作，不要假设任何未提供工具。

上下文信息：
<灵魂画像>
历史会话压缩摘要：
<summary>
本轮观测文字化：
<observation_digest，可空>

决策规则：
1) 先理解用户意图，再查看可用 tools。
2) 若多个 tools 与意图匹配，可在同一轮调用多个 tools（并行或顺序）。
3) 若 tools 语义冲突（互斥动作），只调用最符合当前意图的一组。
4) 若没有合适 tool，可直接文本回复。
5) tool 参数必须严格符合对应 schema，不要编造字段。
6) 若提供 recall_memory，仅在确实需要长期记忆时调用；调用后先回顾记忆，再选择终端技能。
7) 回复保持简洁中文。
```

## 4. Tool 描述规范（Body 上报）

每个技能的 `description` 建议包含：

1. 用途：什么时候该调用。
2. 效果：调用后设备会做什么。
3. 约束：互斥关系、是否可并行、风险边界。
4. 参数说明：关键字段含义（schema 是机器约束，description 是语义约束）。

示例（2 技能）：

```json
[
  {
    "name": "light_green",
    "description": "用途：表达肯定或认同。效果：亮绿灯。可与非冲突动作同轮调用。",
    "input_schema": {"type":"object","properties":{},"required":[]}
  },
  {
    "name": "light_red",
    "description": "用途：表达否定或风险提醒。效果：亮红灯。可与非冲突动作同轮调用。",
    "input_schema": {"type":"object","properties":{},"required":[]}
  }
]
```

## 5. 压缩摘要生成与保存

1. 每轮聊天结束后尝试压缩（阈值控制）。
2. 会话空闲超时后后台强制压缩。
3. 新摘要写入 `sessions.summary`，并更新 `last_compacted_message_id`。
4. 下一轮 system 使用最新 `sessions.summary` 注入“历史会话压缩摘要”。

## 6. 兼容与演进

- 该规范是 Soul 内部 LLM 编排规范，不替代全局通信协议。
- 通信字段定义仍以：`../../doc/通信协议-v2.md` 为准。
- Soul 对外 HTTP 字段仍以：`API文档-Soul服务.md` 为准。
