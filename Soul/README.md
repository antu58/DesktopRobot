# Soul 服务集

`Soul` 目录包含：

- `soul-server`：主服务（会话编排、LLM 调用、技能调度、摘要压缩）
- `terminal-web`：调试终端（模拟 skills 上报与执行）

## 端口（本地默认）

- `soul-server`：`9010`
- `terminal-web`：`9011`
- `mem0`：`18000`

## 启动

```bash
docker compose up --build
```

## 关键说明

- 技能能力来自终端 `skills` 快照，支持 `skill_version` 递增。
- 对话主链路不依赖 Mem0 同步读写。
- 会话活跃由 `/v1/chat` 输入驱动，3 分钟无新输入触发空闲总结。

## 文档

- 设计目标：`docs/设计目标.md`
- 技术调研：`docs/技术调研.md`
- API：`docs/API文档-Soul服务.md`
- 全局通信协议：`../doc/通信协议-v2.md`
