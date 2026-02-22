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
- `intent-filter`：`http://localhost:9013`

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
  "soul_id": "soul_xxx",
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

- 至少存在 1 条非空 `keyboard_text` 或 `speech_text`。
- 其他输入类型当前不进入主回复推理。

会话计时规则：

- 每次成功写入用户输入（`role=user`）重置 3 分钟空闲计时。
- 连续 3 分钟无新输入触发空闲总结。

情绪定时推送规则：

- 服务端会按 `EMOTION_TICK_INTERVAL_SECONDS`（默认 3 秒，范围 2~5 秒）执行一次灵魂情绪“自然演化”。
- 每次演化都会先落库更新 `emotion_state`，随后通过 MQTT 下发一次 `emotion_update`。
- 定时推送的 `emotion_update.session_id` 固定为 `system_decay_tick`，用于端侧区分“非对话输入触发”的状态演化。

技能调度规则（当前实现）：

- 默认：单次 LLM，直接选择终端技能并执行。
- 同一轮可调用多个终端技能（若模型返回多个非冲突 tool calls）。
- 特殊：若首轮选择内置 `recall_memory`（Mem0 历史回顾），服务端先向终端发送 `status=mem0_searching`，查询后进行第二次 LLM，再执行终端技能。
- 无论首轮还是二轮，在发起该轮 LLM 请求前都会重新计算“当刻情绪快照”（用户情绪 + 灵魂 PAD + 执行门控）并注入 system prompt。
- 同时注入“灵魂人格 vs 目标人物人格”关系快照，指导回复风格（措辞、主动性、边界），不改变工具集合。
- `recall_memory` 仅在 Mem0 就绪时暴露给模型；Mem0 未就绪时不会触发该分支。
- `executed_skills` 可能包含 `recall_memory`。

成功响应：

```json
{
  "session_id": "s1",
  "terminal_id": "terminal-001",
  "soul_id": "soul_xxx",
  "reply": "是的，2+2=4。",
  "executed_skills": ["recall_memory", "control_light"],
  "context_summary": "用户持续进行基础事实问答，机器人保持简洁确认式回应。",
  "intent_decision": "fallback_reasoning",
  "exec_mode": "auto_execute",
  "exec_probability": 0.91
}
```

补充说明：

- 当模型输出 `<NO_REPLY>` / `NO_REPLY` / `[NO_REPLY]` 时，服务端会将其归一为“空回复”，即 `reply=""`。
- “空回复”仅表示本轮选择不输出文本；技能执行路径与 MQTT 行为仍按本轮决策执行。

典型失败响应：

```json
{"error":"inputs is required"}
```

```json
{"error":"currently only input.type=keyboard_text|speech_text with non-empty text is supported"}
```

```json
{"error":"soul selection is required before chat"}
```

## 3.3 `GET /v1/souls`

用途：按用户列出灵魂。

响应：

```json
{
  "user_id": "demo-user",
  "items": [
    {
      "soul_id": "soul_xxx",
      "name": "工作助理",
      "mbti_type": "INFJ",
      "personality_vector": {
        "empathy": 0.72,
        "sensitivity": 0.54,
        "stability": 0.58,
        "expressiveness": 0.38,
        "dominance": 0.33
      },
      "emotion_state": {
        "p": 0.02,
        "a": -0.04,
        "d": 0.01
      }
    }
  ]
}
```

## 3.4 `POST /v1/souls`

用途：创建灵魂（MBTI -> 人格向量自动生成）。

请求体：

```json
{
  "user_id": "demo-user",
  "name": "工作助理",
  "mbti_type": "INFJ"
}
```

## 3.5 `POST /v1/souls/select`

用途：终端选择灵魂（绑定 terminal 与 soul）。

请求体：

```json
{
  "user_id": "demo-user",
  "terminal_id": "terminal-001",
  "soul_id": "soul_xxx"
}
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
- `emotion_p / emotion_a / emotion_d`：当前灵魂 PAD（调试展示）。
- `exec_mode`：执行模式（`auto_execute`/`blocked`）。
- `exec_probability`：当前执行概率（0~1）。

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

用途：手工触发重新上报技能快照与意图快照。  
请求体：无。

当前终端默认上报 5 个技能：

- `control_light`：控制灯光。参数：`mode=on/off/set_color`，可选 `color=white/red/green`。
- `create_alarm`：订闹钟。参数：`trigger_at` 或 `trigger_in_seconds`（二选一），可选 `label`。
- `set_head_motion`：头部动作。参数：`action=点头/摇头`，可选 `duration_seconds`（0.2~10）。
- `set_reminder`：设置提醒事项。参数：`content`（必填），可选 `due_at`。
- `send_email`：发邮件（调试页仅模拟执行）。参数：`to/subject/body`（均必填）。

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

## 4.7 `GET /quick-intents`

用途：返回测试网页可快速触发的意图预设列表（用于测试 `intent_action` 协议）。

## 4.8 `POST /quick-intent`

用途：快速触发一个意图（默认通过 MQTT 发布 `intent_action`，失败时本地回退处理并展示）。

请求体示例：

```json
{
  "intent_id": "qk_light_on",
  "session_id": "s1",
  "transport": "mqtt"
}
```

说明：

- `intent_id`：来自 `/quick-intents`。
- `session_id`：可选，空则使用当前会话。
- `transport`：`mqtt`（默认）或 `local`。
- 当前预置快速意图：
  - `qk_light_on` / `qk_light_off` / `qk_light_red` / `qk_light_green`
  - `qk_alarm_10m`
  - `qk_nod` / `qk_shake`

调试页 15 情绪响应规则（由 `emotion_update.user_emotion` 驱动）：

- 强负向（`anger/disgust/frustration`）：`生气 + 摇头`
- 压力型（`anxiety/fear`）：`不开心`，高强度时 `摇头`
- 低落型（`sadness/disappointment/boredom`）：`不开心`，高强度时 `哭 + 摇头`
- 正向（`joy/gratitude/relief`）：`微笑`，中高强度时 `点头`
- 高唤醒正向（`excitement/surprise`）：`大笑`，中高强度时 `点头`
- 稳态（`calm/neutral`）：`微笑`

补充约定：

- 当 `emotion_update.session_id=system_decay_tick` 时，表示服务端定时自然演化推送，不代表新增用户输入。

## 5. `emotion-server`（情感理解子服务）

> 设计目标：作为主服务可复用的“情感理解网关”，固定输出 `emotion + PAD + intensity`。  
> 使用 `mDeBERTa-v3-base-xnli-multilingual-nli-2mil7` + ONNX Runtime（CPU int8）做 PAD 三轴直推，再按 15 类 PAD 原型输出主情绪。

## 5.1 `GET /healthz`

用途：检查 emotion-server 状态（Python + mDeBERTa-XNLI 模型服务）。

成功响应：

```json
{
  "ok": true,
  "engine": "python-mdeberta-xnli-pad",
  "model": "MoritzLaurer/mDeBERTa-v3-base-xnli-multilingual-nli-2mil7",
  "analyze_mode": "pad_direct_nli",
  "nli_hypothesis_template": "这句话表达的是{}。",
  "runtime_backend": "onnxruntime",
  "runtime_int8": true,
  "runtime_model_dir": "/models/onnx/MoritzLaurer--mDeBERTa-v3-base-xnli-multilingual-nli-2mil7/int8",
  "warmup_ok": true,
  "warmup_ms": 76670.127,
  "warmup_error": "",
  "pad_labels": ["anger", "anxiety", "boredom", "calm", "disappointment", "disgust", "excitement", "fear", "frustration", "gratitude", "joy", "neutral", "relief", "sadness", "surprise"]
}
```

## 5.2 `GET /v1/emotion/pad-table`

用途：查看主情绪到 PAD 的对照表。

## 5.3 `POST /v1/emotion/analyze`

用途：输入文本，返回 PAD 三轴与主情绪（由 15 类 PAD 原型最近邻得到）。

请求体：

```json
{
  "text": "今天被老板批评了"
}
```

响应体：

```json
{
  "emotion": "sadness",
  "p": -0.65,
  "a": -0.15,
  "d": -0.35,
  "intensity": 0.9123,
  "latency_ms": 22.614
}
```

说明：

- `latency_ms` 为 emotion-server 单次处理耗时。
- 首次请求包含模型加载/下载耗时，后续会明显降低。
- 服务启动阶段会先完成一次预热推理（若失败，服务启动失败，不做自动回退）。

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

## 6. `intent-filter`（意图筛选子服务）

> 设计目标：由主服务注入“支持的意图表 + 命令文本”，返回统一的多意图结果，不参与技能调用。

## 6.1 `GET /healthz`

用途：检查 intent-filter 服务状态。

响应：

```json
{
  "ok": true,
  "engine": "mvp-rule-filter",
  "version": "0.4.0"
}
```

## 6.2 `POST /v1/intents/filter`

用途：意图筛选（支持一句多意图）。

请求体（关键字段）：

```json
{
  "request_id": "optional",
  "command": "帮我把灯变成绿色，然后10分钟后提醒我",
  "intent_catalog": [
    {
      "id": "intent_light_control",
      "name": "控制灯",
      "match": {"keywords_any": ["开灯", "打开灯", "把灯打开", "灯打开", "关灯", "关闭灯", "灯关了", "灯关", "灯", "红色", "绿色", "白色", "灯白色", "变红", "变绿", "变白"]},
      "slots": [
        {"name": "skill", "default": "control_light"},
        {"name": "mode", "regex": "(开灯|打开灯|把灯打开|灯打开|打开|开启|关灯|关闭灯|把灯关掉|灯关了|关了|关掉|关闭|变红|变红色|变绿|变绿色|变白|变白色|红灯|绿灯|白灯)", "regex_group": 1},
        {"name": "color", "regex": "(红色|红|绿色|绿|白色|白|白灯|灯白色)", "regex_group": 1}
      ]
    },
    {
      "id": "intent_alarm_create",
      "name": "订闹钟",
      "match": {"keywords_any": ["闹钟", "提醒", "alarm"]},
      "slots": [
        {"name": "skill", "default": "create_alarm"},
        {"name": "trigger_in_seconds", "regex": "([0-9]+(?:\\.[0-9]+)?)\\s*分钟", "regex_group": 1},
        {"name": "label", "default": "提醒事项"}
      ]
    }
  ],
  "options": {
    "allow_multi_intent": true,
    "max_intents_per_segment": 1,
    "min_confidence": 0.35,
    "return_debug_entities": true,
    "emit_system_intent_when_empty": true
  }
}
```

响应体（关键字段）：

```json
{
  "request_id": "ifr_xxx",
  "decision": {
    "action": "execute_intents",
    "trigger_intent_id": "intent_light_control",
    "reason": "matched_catalog_intents"
  },
  "intents": [
    {
      "intent_id": "intent_light_control",
      "intent_name": "控制灯",
      "confidence": 0.91,
      "status": "ready",
      "segment_index": 0,
      "span": {"text": "把灯变成绿色", "start": 0, "end": 6},
      "parameters": {"mode": "set_color", "color": "green"},
      "normalized": {"skill": "control_light", "mode": "set_color", "color": "green"},
      "missing_parameters": [],
      "evidence": [{"type": "keyword_any", "value": "灯", "score": 1.0}]
    },
    {
      "intent_id": "intent_alarm_create",
      "intent_name": "订闹钟",
      "confidence": 0.83,
      "status": "ready",
      "segment_index": 1,
      "span": {"text": "10分钟后提醒我", "start": 7, "end": 14},
      "parameters": {"trigger_in_seconds": 600, "label": "提醒事项"},
      "normalized": {"skill": "create_alarm", "trigger_in_seconds": 600, "label": "提醒事项"},
      "missing_parameters": [],
      "evidence": [{"type": "keyword_any", "value": "提醒", "score": 1.0}]
    }
  ],
  "meta": {
    "latency_ms": 6.21,
    "segment_count": 2,
    "catalog_size": 2,
    "time_signals": 1
  }
}
```

说明：

- 服务仅负责意图筛选与参数结构化，不负责技能路由和执行。
- 时间解析当前为算法策略（相对时间 + 常见绝对时间）。
- 服务内会自动推算 `timezone/now`，并自动抽取基础实体（action/device/room）。
- 服务支持自动识别语言：`zh-CN` / `zh-TW` / `en-US` / `ko-KR` / `ja-JP`。
- 当 `options.return_debug_entities=true` 时，会在 `meta.extracted_entities` 返回服务内部抽取到的实体。
- 当未命中业务意图时，服务会返回系统意图（默认开启）：
  - `sys.fallback_reasoning`：主服务应触发高级模型思考。
  - `sys.no_action`：主服务应忽略执行动作（可仅做记录/情绪回应）。

## 7. 关联文档

- Soul 设计目标：`设计目标.md`
- LLM 请求规范：`LLM请求规范.md`
- 全局通信协议：`../../doc/通信协议-v2.md`
