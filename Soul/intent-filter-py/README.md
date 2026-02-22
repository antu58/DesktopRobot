# Intent Filter Service (MVP)

独立意图筛选服务（不负责技能调用），输入：
- 支持意图表（`intent_catalog`）
- 本轮命令文本（`command`）

输出：
- 命中的单/多意图数组（`intents[]`）
- 固定参数结构（`parameters + normalized + status + missing_parameters`）
- 路由决策（`decision`），供主服务决定执行/高级推理/忽略

## Run

```bash
cd /Users/zhangfeng/Desktop/Linux/DesktopRobot/Soul/intent-filter-py
python3 -m venv .venv
source .venv/bin/activate
pip install -r requirements.txt
uvicorn app:app --host 0.0.0.0 --port 9013
```

## API

- `GET /healthz`
- `POST /v1/intents/filter`

### 支持语言

- `zh-CN`（简体中文）
- `zh-TW`（繁体中文）
- `en-US`（英语）
- `ko-KR`（韩语）
- `ja-JP`（日语）

说明：服务会自动检测语言并选择对应词典与时间规则。

### Request 示例

```json
{
  "command": "帮我关闭窗帘并且定一个30分钟的闹钟我要休息一会",
  "intent_catalog": [
    {
      "id": "curtain_control",
      "name": "窗帘控制",
      "priority": 90,
      "match": {
        "keywords_any": ["窗帘", "关闭", "拉上"],
        "regex_any": ["(关|拉上).{0,3}窗帘"]
      },
      "slots": [
        {"name": "target", "required": true, "from_entity_types": ["device"]},
        {"name": "action", "required": true, "from_entity_types": ["action"]}
      ]
    },
    {
      "id": "alarm_create",
      "name": "闹钟创建",
      "priority": 80,
      "match": {
        "keywords_any": ["闹钟", "定一个", "设个"],
        "regex_any": ["(定|设).*(闹钟)"]
      },
      "slots": [
        {"name": "duration_seconds", "required": true, "from_time_key": "duration_seconds", "time_kind": "duration"},
        {"name": "trigger_at", "required": true, "from_time_key": "trigger_at", "time_kind": "duration"},
        {"name": "label", "regex": "闹钟(.*)$", "default": "提醒"}
      ]
    }
  ],
  "options": {
    "allow_multi_intent": true,
    "max_intents_per_segment": 1,
    "return_debug_candidates": true,
    "return_debug_entities": true
  }
}
```

### Response 关键字段

- `decision`
  - `action`: `execute_intents` / `fallback_reasoning` / `no_action`
  - `trigger_intent_id`: 触发该路由的意图 ID
  - `reason`: 路由原因
- `intents[]`
  - `intent_id` / `intent_name`
  - `confidence`
  - `status`: `ready` / `need_clarification` / `rejected` / `system`
  - `parameters`: 原始参数
  - `normalized`: 规范化参数
  - `missing_parameters`: 缺失参数列表
  - `span`: 命中片段
- `meta.extracted_entities`（仅当 `options.return_debug_entities=true`）

### 默认系统意图

- `sys.fallback_reasoning`：意图不明确，建议主服务触发高级模型思考
- `sys.no_action`：检测到情绪/感叹类输入（如“吓我一跳”），建议主服务不执行动作

## 时间解析策略（算法）

- 相对时间：
  - 中文：`10分钟后`
  - 英文：`in 10 minutes`
  - 韩文：`10분 뒤`
  - 日文：`10分後`
- 绝对时间：
  - 中文：`明早5点`
  - 英文：`tomorrow 5am`
  - 韩文：`내일 오전 5시`
  - 日文：`明日 午前5時`

注意：这是 MVP 算法解析，复杂口语时间后续可在本服务内继续增强。

## 内部自动推算

服务内部会自动完成：

- 文本轻度归一（空白清理、礼貌前缀裁剪）
- `timezone`（默认 `Asia/Shanghai`）
- `now`（服务当前时间）
- `locale`（自动识别）
- 基础实体抽取（`action` / `device` / `room`）
- 时间信号抽取（相对时间/绝对时间）

可通过环境变量修改默认值：

- `INTENT_FILTER_DEFAULT_LOCALE`（默认 `zh-CN`）
- `INTENT_FILTER_DEFAULT_TIMEZONE`（默认 `Asia/Shanghai`）
