# Soul API 联调场景文档

更新时间：2026-02-20  
定位：联调示例与排查说明（字段级定义以 API 主文档为准）

## 1. 主文档入口

- API 主文档：`API文档-Soul服务.md`
- 全局通信协议：`../../doc/通信协议-v2.md`

## 2. 服务入口（本地默认）

- `soul-server`：`http://localhost:9010`
- `terminal-web`：`http://localhost:9011`

## 3. 关键实现口径（Phase 1）

- `/v1/chat` 请求统一使用 `inputs[]`。
- 当前只把 `keyboard_text` 作为主输入参与回复推理。
- 每次成功写入用户输入都会重置 3 分钟空闲计时。
- MQTT 技能来源于终端快照（`skills`），服务端按版本生效。

## 4. 联调场景

## 场景A：终端首次接入

1. 终端连接 MQTT，发布 `online=online`。  
2. 终端发布 `skills`（可附 `soul_hint`）。  
3. 服务端创建或复用灵魂绑定。  
4. 后续重连复用同一 `soul_id`。

## 场景B：正确语句触发绿灯

```bash
curl -X POST http://localhost:9011/ask \
  -H 'content-type: application/json' \
  -d '{"session_id":"acc-green","inputs":[{"type":"keyboard_text","source":"keyboard","text":"地球绕太阳公转，这句话正确吗？"}]}'
```

期望：`executed_skills` 包含 `light_green`。

## 场景C：错误语句触发红灯

```bash
curl -X POST http://localhost:9011/ask \
  -H 'content-type: application/json' \
  -d '{"session_id":"acc-red","inputs":[{"type":"keyboard_text","source":"keyboard","text":"太阳绕地球公转，这句话正确吗？"}]}'
```

期望：`executed_skills` 包含 `light_red`。

## 场景D：无技能文本回复

当终端技能快照为空时，请求仍可返回 `reply`，但不会执行技能。

## 场景E：3 分钟无输入触发总结

- 持续 3 分钟无新 `/v1/chat` 用户输入。  
- 期望触发空闲总结与异步记忆队列写入（若启用）。

## 5. 快速排查清单

1. `/v1/chat` 是否始终携带 `inputs[]` 且包含非空 `keyboard_text`。
2. `skills` topic 里的终端 ID 与 payload `terminal_id` 是否一致。
3. `skill_version` 是否单调递增（防止旧快照回滚）。
4. `invoke/{requestId}` 与 `result/{requestId}` 是否一一对应。
5. 若无技能执行，先确认终端技能快照是否在线且未过期。
