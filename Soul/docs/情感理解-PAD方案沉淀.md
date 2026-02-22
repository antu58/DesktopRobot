# 情感理解 PAD 方案沉淀

更新时间：2026-02-22  
适用范围：`Soul/emotion-server-py`（Python 子服务）

## 1. 背景与问题

在原方案中，文本情绪分析采用“模型输出 7 类主情绪，再映射到 PAD”。该路径在中文场景出现了两类典型问题：

- 语义覆盖不足：中文口语、隐式情绪、混合情绪难以被 7 类离散标签准确承载。
- 误判稳定性差：如“哈哈哈哈哈”被模型高置信度判到负向类别，影响下游行为控制。

因此，目标从“分类优先”改为“PAD 优先”。

## 2. 最终目标与设计原则

### 2.1 最终目标

- 对任意输入文本，稳定输出：
  - `emotion`（解释层主情绪）
  - `p/a/d`（核心连续变量）
  - `intensity`（强度）

### 2.2 设计原则

- 以 PAD 三轴为核心，不以离散分类为核心。
- 保留 15 类作为解释层原型，不作为推理主空间。
- 不使用外挂词典修正，不做规则兜底分类。
- 工程上优先低延迟可部署：CPU 可运行、可缓存、可观测。

## 3. 情绪空间定义（15 类 PAD 原型）

当前原型表（用于解释层最近邻）：

| emotion | p | a | d |
|---|---:|---:|---:|
| neutral | 0.00 | 0.05 | 0.00 |
| joy | 0.70 | 0.55 | 0.20 |
| surprise | 0.10 | 0.75 | -0.05 |
| sadness | -0.65 | -0.15 | -0.35 |
| fear | -0.70 | 0.70 | -0.60 |
| anger | -0.60 | 0.75 | 0.25 |
| disgust | -0.55 | 0.35 | 0.10 |
| calm | 0.20 | -0.35 | 0.15 |
| relief | 0.50 | -0.20 | 0.30 |
| gratitude | 0.60 | 0.20 | 0.35 |
| excitement | 0.78 | 0.82 | 0.30 |
| anxiety | -0.62 | 0.72 | -0.48 |
| frustration | -0.52 | 0.58 | -0.08 |
| disappointment | -0.58 | -0.08 | -0.28 |
| boredom | -0.20 | -0.45 | -0.15 |

## 4. 方案演进

### 4.1 V0：7 类分类 -> PAD 映射（已废弃）

- 优点：实现简单。
- 缺点：对中文隐式表达与混合情绪拟合不足。

### 4.2 V1：PAD Direct（当前主线）

使用 `MoritzLaurer/mDeBERTa-v3-base-xnli-multilingual-nli-2mil7` 做 zero-shot NLI，直接估计三轴。

- 轴锚点策略：
  - `P`：积极愉悦 vs 痛苦消极
  - `A`：激动紧张 vs 平静放松
  - `D`：掌控自信 vs 无力被压制
- 每轴以“正锚点均值 - 负锚点均值”得到连续值。

### 4.3 V2：ONNX Runtime + CPU int8 + 启动预热（当前生产工程态）

- ONNX 导出与 int8 动态量化（首次启动执行并缓存）。
- 服务启动时自动预热一次推理。
- 严格 ONNX 模式：预热失败即服务启动失败，不做自动回退。

## 5. 推理算法（核心公式）

记某一轴 `x∈{p,a,d}`：

- `pos_mean_x = mean(score(anchor_pos_i))`
- `neg_mean_x = mean(score(anchor_neg_j))`
- `x = clamp(pos_mean_x - neg_mean_x, -1, 1)`

强度：

- `norm = sqrt((p^2 + a^2 + d^2) / 3)`
- `certainty = mean(|p_delta|, |a_delta|, |d_delta|)`
- `intensity = clamp(0.65 * norm + 0.35 * certainty, 0, 1)`

解释层主情绪：

- 在 15 个 PAD 原型中做欧氏距离最近邻：  
  `emotion = argmin_k ((p-p_k)^2 + (a-a_k)^2 + (d-d_k)^2)`

## 6. 服务接口与可观测性

核心接口：

- `GET /healthz`
- `GET /v1/emotion/pad-table`
- `POST /v1/emotion/analyze`
- `POST /v1/emotion/convert`（兼容接口）

`/healthz` 关键运行字段：

- `runtime_backend`: `onnxruntime`
- `runtime_int8`: `true/false`
- `runtime_model_dir`: 当前模型目录
- `warmup_ok / warmup_ms / warmup_error`: 启动预热状态

## 7. 缓存与部署策略

缓存目标：避免重复下载与重复导出/量化。

- 模型缓存目录：`EMOTION_MODEL_CACHE_DIR`（默认 `./.cache/huggingface`）
- 容器内挂载：`/models`
- ONNX 缓存路径：`/models/onnx/<model-id>/...`
- Git 忽略：`Soul/.gitignore` 已忽略 `.cache/`

## 8. 当前实测（本机）

测试设备（2026-02-22）：

- Apple M1（8C CPU / 8C GPU）
- 16GB 内存
- macOS 26.3

实测结果：

- 首次启动预热（含导出+量化）：`warmup_ms = 76670.127`
- 稳态 `analyze`（12 次）：
  - `min`: 736.552 ms
  - `p50`: 813.275 ms
  - `mean`: 955.412 ms
  - `max`: 1520.982 ms

说明：

- 冷启动成本被前置到服务启动阶段，业务首请求不再承担完整冷启动。
- 稳态已从多秒级降到约 0.8~1.5 秒区间（CPU）。

## 9. 与“小模型 PAD 回归”目标的关系

当前方案的价值是提供了可运行基线与数据闭环，为下一步“小模型直回归 PAD”提供路径：

- 当前服务可持续产出 `(text, p, a, d, intensity)` 样本。
- 可叠加人工校正构建监督集，训练轻量回归模型（3 维输出）。
- 目标是将推理进一步压缩到亚秒级并提升中文语境稳定性。

## 10. 已知限制与下一步

已知限制：

- zero-shot 锚点仍受提示语设计影响，存在语义漂移。
- `emotion` 由最近邻得到，表达能力依赖原型表质量。

下一步建议：

1. 建立固定回归测试集（直接/间接/脏话/混合情绪）。
2. 对锚点句做 A/B 实验，收敛到稳定集合。
3. 训练“小模型 PAD 回归头”（冻结 backbone 或 LoRA）。
4. 保持接口不变，替换 `analyze` 内核实现，平滑迁移。

## 11. 复现命令（当前仓库）

```bash
cd /Users/zhangfeng/Desktop/Linux/DesktopRobot/Soul
docker compose up --build -d emotion-server
curl -sS http://127.0.0.1:9012/healthz | jq
curl -sS -X POST http://127.0.0.1:9012/v1/emotion/analyze \
  -H 'Content-Type: application/json' \
  -d '{"text":"今天被老板批评了，但我也想尽快调整好状态"}' | jq
```

---

本沉淀文档可直接作为博客技术主线：  
问题定义 -> 方案演进 -> 算法细节 -> 工程优化 -> 实测结果 -> 后续路线。
