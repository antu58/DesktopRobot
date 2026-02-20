# Body 对接 Soul 服务端开发文档（实现指南）

更新时间：2026-02-20

## 1. 协议基线

Body 与 Soul 的统一通信协议以全局文档为准：

- `../doc/通信协议-v2.md`
- `../Soul/docs/API文档-Soul服务.md`

本文档只描述 Body 侧落地步骤与实现建议，不重复定义协议字段。
当前 Body 侧没有独立对外 HTTP API；Body 作为调用方对接全局 API 契约。

## 2. 初始化流程（必须）

连接 MQTT 成功后，按顺序执行：

1. 发布 `online=online`（同时配置 LWT 为 `offline`）。
2. 发布 `skills` 快照（初始化行为，必做）。
3. 启动 `heartbeat` 定时上报（建议 10s 周期）。
4. 订阅 `invoke` 并准备回执 `result`。

注：`skills` 上报属于连接初始化的一部分，不能延后到首次对话时再上报。

## 3. 技能快照实现要求

请严格按全局协议 `3.3 skills` 执行，特别注意：

- topic 中 `{terminalId}` 必须与 payload `terminal_id` 一致。
- `skill_version` 必须单调不回退。
- 技能名在同一快照内必须唯一。
- `input_schema` 推荐总是提供 JSON Schema。
- 技能变更（增删改或 schema 变化）时递增 `skill_version`。

## 4. 技能执行回执要求

请按全局协议 `3.6 invoke / result` 执行：

- 回执必须带原始 `request_id`。
- 失败时 `ok=false` 且提供 `error`。
- 建议 5 秒内完成回执，避免服务端工具超时。

## 5. HTTP 聊天对接（当前阶段）

请按全局协议 `4.1 POST /v1/chat` 执行。当前阶段注意：

- 必须走 `inputs[]`。
- 至少一条非空 `keyboard_text`。
- 其他输入类型可先按协议结构上传，后续逐步生效。

会话活跃口径：

- 当前不再单独上报 `user-active`。
- `/v1/chat` 成功写入用户输入后，服务端自动重置 3 分钟计时。

## 6. 开发自检清单

1. 断网重连后是否自动恢复 `online + skills + heartbeat`。
2. 技能版本升级后是否不会被旧版本回滚。
3. 每个 `invoke` 是否都能回到对应 `result/{requestId}`。
4. `/v1/chat` 是否始终包含合法 `inputs[].keyboard_text`。
5. 连续 3 分钟无输入是否触发会话总结。
