# LLM 请求规范（Soul）

更新时间：2026-02-22

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
- system 放稳定规则、上下文摘要与“本次调用瞬间”的情绪快照关键词。
- system 同时注入“灵魂人格 vs 目标人物人格”的关系快照，用于回复风格约束（不改变工具集合）。
- 默认单轮 LLM；当首轮命中 `recall_memory` 时进入二轮 LLM，且第二轮会重新按调用当刻生成情绪快照注入。

## 3. System 提示词模板

```text
你是单用户桌面机器人编排助手。你只能使用本轮请求提供的 tools 执行动作，不要假设任何未提供工具。

上下文信息：
<灵魂画像>
历史会话压缩摘要：
<summary>
本轮观测文字化：
<observation_digest，可空>

情绪门控快照（当前 LLM 调用时刻）：
- snapshot_at: <RFC3339Nano>
- user_emotion: <label> (intensity=<0~1>)
- soul_pad: p=<...> a=<...> d=<...>
- execution_gate: mode=<auto_execute|blocked> probability=<0|1>
- emotion_keywords: <comma-separated keywords>

人格关系快照（用于回复风格，不改变工具集合）：
- soul_mbti: <soul_mbti>
- soul_traits: empathy=<...> sensitivity=<...> stability=<...> expressiveness=<...> dominance=<...>
- target_persona: <target_mbti|heuristic_target|unknown>
- target_traits: empathy=<...> sensitivity=<...> stability=<...> expressiveness=<...> dominance=<...>
- relation_assessment: <同频|可协同|高张力|unknown>
- relation_strategy: <根据关系给出的措辞/主动性/边界建议>

决策规则：
1) 先理解用户意图，再查看可用 tools。
2) 若多个 tools 与意图匹配，可在同一轮调用多个 tools（并行或顺序）。
3) 若 tools 语义冲突（互斥动作），只调用最符合当前意图的一组。
4) 若没有合适 tool，可直接文本回复。
5) tool 参数必须严格符合对应 schema，不要编造字段。
6) 若提供 recall_memory，仅在确实需要长期记忆时调用；调用后再进入下一轮技能决策。
7) 参考 emotion_keywords 调整回复语气与工具选择，但不要编造不存在的技能。
8) 根据 execution_gate 约束执行语义（blocked 时不得声称已自动执行）。
9) 除技能执行外，结合 relation_strategy 调整措辞、长度、主动性与边界。
10) 若判断“当前不回复更合适”，仅输出 `<NO_REPLY>`（不要附加任何文字）。
11) 其余情况保持简洁中文回复。
```

### 3.1 `NO_REPLY` 约定

- LLM 输出 `<NO_REPLY>` / `NO_REPLY` / `[NO_REPLY]` 视为“本轮选择不回复”。
- Soul 会将其归一为“空回复”，不再兜底填充固定文案。
- 即使不回复，工具调用与消息持久化仍遵循当前轮执行结果。

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
