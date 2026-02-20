# DesktopRobot 性能探索报告：LLM 响应延迟与工具调用影响（2026-02-20）

## 1. 摘要（可直接用于对外分享）

这次排查的核心结论是：

1. 在当前链路里，**主耗时几乎都在 LLM 推理阶段**，不是 MQTT、技能执行或数据库。
2. 相比“纯文本回复”，**工具选择/函数调用会显著拉长响应时间**，且工具越多、越强制调用，延迟越高。
3. 仅增加上下文 token 会变慢，但在本次实测中，影响幅度明显小于工具调用路径。
4. “触发记忆回顾（Mem0）”分支因为是双阶段 LLM（首轮决策 + 记忆查询 + 二轮决策），平均耗时约为非记忆路径的 3 倍以上。

---

## 2. 背景与目标

### 2.1 背景

在项目联调中发现：

- 裸测模型接口通常 1~2 秒级；
- 进入 Soul 完整链路后，部分请求达到 30 秒以上。

### 2.2 本次探索问题

1. 非记忆路径到底慢在哪个环节？
2. 触发记忆路径是否显著放大总耗时？
3. 影响 LLM 延迟的主因是 token 量，还是工具（skills/tools）选择机制？

---

## 3. 本次改造与观测能力建设

为得到可归因的数据，本次先补了两类能力：

1. **Mem0 就绪门控（readiness gate）**
   - 仅当 Mem0 就绪时才向模型暴露 `recall_memory` 内置技能。
   - Mem0 不可用时自动退化为单次 LLM，不阻塞主链路。

2. **分阶段耗时埋点**
   - 在 Soul `chat timing` 日志中输出：
   - `first_llm_ms`、`recall_tool_ms`、`second_llm_ms`、`terminal_tool_ms`、`total_ms`
   - 便于区分“模型推理慢”还是“工具执行慢”。

相关代码位置：

- `/Users/zhangfeng/Desktop/Linux/DesktopRobot/Soul/internal/orchestrator/service.go`
- `/Users/zhangfeng/Desktop/Linux/DesktopRobot/Soul/internal/memory/service.go`
- `/Users/zhangfeng/Desktop/Linux/DesktopRobot/Soul/internal/memory/mem0.go`

---

## 4. 实验设计

### 4.1 实验 A：真实业务链路 10 轮对比

对比两组 `/v1/chat`：

1. 不触发记忆（默认单次 LLM）
2. 触发记忆（`recall_memory` + 二次 LLM）

> 口径：统计“从用户提问到技能执行完成”的链路时延；同时保留 HTTP 总耗时和分阶段耗时。

### 4.2 实验 B：拆因实验（直接打 LLM API）

新增脚本：

- `/Users/zhangfeng/Desktop/Linux/DesktopRobot/Soul/scripts/benchmark_llm_factors.sh`

用 A/B 方式拆分两个变量：

1. **Token 规模变量**（无 tools）
2. **Tools 变量**（固定输入长度，比较 `none / 2 tools / 20 tools`，再比较“必须调用工具”与“可不调用”）

固定参数：

- `MAX_TOKENS=32`
- `TEMPERATURE=0`

目的是尽量降低输出长度随机性，让数据更可比。

---

## 5. 实验结果

## 5.1 实验 A：真实链路（10 轮）

### A1. 不触发记忆（10/10 成功）

- 10 轮平均总耗时：**14,420 ms**
- 中位数：**14,091 ms**
- 最小/最大：**6,396 / 24,621 ms**

分阶段平均（日志）：

- `first_llm_ms` = **14,400.50 ms**
- `terminal_tool_ms` = **1.10 ms**
- `total_ms` = **14,417.10 ms**

结论：非记忆路径中，`first_llm_ms` 占 `total_ms` 的 **99.88%**。

### A2. 触发记忆（10 轮中 8 轮成功）

- 全 10 轮（含 2 次 500）平均：**48,850 ms**
- 成功 8 轮平均：**46,056 ms**

成功轮次分阶段平均（日志）：

- `first_llm_ms` = **21,927.88 ms**
- `recall_tool_ms` = **11,654.62 ms**
- `second_llm_ms` = **12,436.25 ms**
- `terminal_tool_ms` = **11.25 ms**
- `total_ms` = **46,051.38 ms**

若只看 `recall_mode=true` 的成功轮次（7 轮）：

- `total_ms` = **49,195.00 ms**
- 阶段占比：
  - 首轮 LLM：**44.0%**
  - 记忆查询：**27.1%**
  - 二轮 LLM：**28.9%**

异常说明：

- 两次 500 的直接原因是外部模型请求超时（`context deadline exceeded`），属于上游模型/网关波动。

---

## 5.2 实验 B：变量拆分（直接调用 LLM）

### B1. Token 规模影响（无 tools，3 轮平均）

| 输入规模 | 平均 prompt_tokens | 平均耗时 |
|---|---:|---:|
| `w=64` | 295 | 2,333.54 ms |
| `w=512` | 2,052 | 2,162.50 ms |
| `w=1536` | 6,685 | 2,870.20 ms |
| `w=3072` | 14,365 | 2,969.04 ms |

观察：

- 从 `~2k` 提升到 `~14k` prompt tokens，平均耗时约从 `2.16s` 增至 `2.97s`，有上升，但幅度有限。

### B2. Tools 影响（固定 `w=512`，3 轮平均）

| 场景 | 平均耗时 | 相对 `tools_none` 倍数 |
|---|---:|---:|
| `tools_none` | 1,699.84 ms | 1.00x |
| `tools_2_no_call` | 5,468.12 ms | 3.22x |
| `tools_2_must_call` | 9,059.47 ms | 5.33x |
| `tools_20_no_call` | 6,790.49 ms | 3.99x |
| `tools_20_must_call` | 14,168.63 ms | 8.34x |

观察：

- 仅引入 tools（即使不调用）就显著变慢；
- 强制 tool call 比“不强制调用”更慢；
- 工具数量增加会继续拉高耗时。

---

## 6. 结论

1. **你之前的判断是对的**：影响延迟的不只是 token 数量，输入结构本身（尤其 tools/函数调用）会显著增加响应时间。
2. 在当前系统中，非记忆路径的主要瓶颈是首轮 LLM；触发记忆时，首轮+二轮 LLM + 记忆查询共同拉高时延。
3. 终端技能执行本身几乎可以忽略（毫秒级），不是当前性能重点。

---

## 7. 工程优化建议（按优先级）

1. **保持默认单次 LLM 路径**，仅在必要时进入 recall 双阶段。
2. **减少每轮暴露的 tools 数量**（按场景动态裁剪，而不是全量注入）。
3. **缩短 tool 描述与参数 schema**，降低模型函数选择开销。
4. 对 recall 分支增加更明确控制：
   - 例如 `force_recall` 请求参数（显式触发，不依赖提示词博弈）。
5. 对上游模型波动设置策略：
   - 更短超时 + 快速重试；
   - 或降级到更快模型保障体验。

---

## 8. 可复现实验命令

### 8.1 变量拆分压测

```bash
cd /Users/zhangfeng/Desktop/Linux/DesktopRobot/Soul

# token 影响
ROUNDS=5 SUITE=tokens TOKEN_WORD_COUNTS='64 512 1536 3072' MAX_TOKENS=32 TEMPERATURE=0 ./scripts/benchmark_llm_factors.sh

# tools 影响
ROUNDS=5 SUITE=tools TOOLS_FIXED_WORDS=512 MAX_TOKENS=32 TEMPERATURE=0 ./scripts/benchmark_llm_factors.sh
```

### 8.2 当前一次结果文件

- `/tmp/benchmark_llm_factors_1771610136.csv`（tokens）
- `/tmp/benchmark_llm_factors_1771610015.csv`（tools）

---

## 9. 读者可直接引用的“短结论”

在这个机器人对话系统里，真正拖慢响应的不只是上下文长度，更关键的是“让模型做工具选择和函数调用”这件事本身。  
同等输入规模下，开启工具选择可以带来 3~8 倍的时延上升；而纯 token 增长带来的增幅相对温和。  
因此，实时对话系统要想快，优先做的是“工具注入与调用策略优化”，其次才是单纯压缩提示词长度。
