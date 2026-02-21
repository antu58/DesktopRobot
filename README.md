# Desktop Robot

桌面机器人项目，采用 **Soul（大脑） + Body（躯体）** 架构。

## 核心概念

- **Soul**：负责对话理解、上下文管理、技能调度与记忆策略。
- **Body**：负责硬件感知、动作执行、能力上报与输入采集。
- **Skill**：由 Body 上报的可调用能力，Soul 按 LLM 结果调度。
- **Session**：单次会话上下文容器，3 分钟无新输入触发总结。

## 当前实现边界（Phase 1）

- 对话输入统一 `inputs[]`。
- 主链路仅处理 `keyboard_text`。
- 通信主协议稳定，技能快照 + 调用回执可闭环。
- Mem0 不进入同步聊天链路，仅异步流程使用。

## 文档导航

### 全局（Soul / Body 共用）

- `doc/通信协议-v2.md`：唯一通信协议基线
- `doc/性能探索报告-LLM响应延迟与工具调用影响.md`：LLM 延迟与工具调用性能探索报告

### Soul（仅维护 2 份核心文档 + 1 份 API）

- `Soul/docs/设计目标.md`
- `Soul/docs/技术调研.md`
- `Soul/docs/API文档-Soul服务.md`
- `Soul/docs/LLM请求规范.md`

### Body（仅维护 2 份核心文档）

- `Body/设计目标.md`
- `Body/技术调研.md`

### 历史归档

- `Soul/docs/archive/`
- `Body/archive/`

## 快速开始

```bash
./deploy_local.sh
```

常用命令：

- `./deploy_local.sh status`
- `./deploy_local.sh logs`
- `./deploy_local.sh restart`
- `./deploy_local.sh down`
