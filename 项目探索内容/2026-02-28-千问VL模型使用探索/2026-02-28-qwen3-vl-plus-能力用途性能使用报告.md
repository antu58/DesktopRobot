# 2026-02-28 qwen3-vl-plus 能力/用途/性能/使用报告

## 1. 报告结论（先看）

1. `qwen3-vl-plus` 适合做“图文理解 + 结构化输出 + 智能体工具调用前的视觉解释层”，不适合低延迟强实时场景。
2. 官方规格显示该模型为**大上下文视觉模型**：`Context 131,072`、`Max Output 16,384`、单图输入上限 `16,384 tokens`。
3. 在你当前可用的 API（`https://api.newcoin.top/v1`）上，图文请求延迟显著高于纯文本：
   - 文本 3 次均值约 `2.3s`
   - 图文 3 次均值约 `11.2s`
   - 流式首 token（图文）实测约 `14.1s`
4. 计费和限流上，图像输入 token 成本占比高，建议在产品里做图片压缩、裁剪和任务分流。

## 2. 调研范围与方法

- 调研日期：`2026-02-28`（Asia/Shanghai）
- 信息来源：以官方文档/官方模型页为主（DashScope、Qwen 官方模型卡）
- 性能数据：在你的现网可用端点上做了小样本实测（3 次文本、3 次图文 + 1 次流式）

## 3. 模型能力（官方）

基于 Qwen 官方模型卡，Qwen3-VL 系列的核心能力包括：

1. 多图和长视频理解（官方描述覆盖多图、长视频理解能力）。
2. 细粒度视觉定位与 grounding（支持屏幕/UI 元素级定位）。
3. 文档、表格、图表、流程图等复杂视觉对象解析。
4. 代码仓库级视觉任务（如 GUI 代理、Design2Code、代码导航相关场景）。
5. 支持动态分辨率与图像原生分辨率输入（强调细节读取能力）。

## 4. 规格、价格、限流（官方）

### 4.1 规格（qwen3-vl-plus）

- 当前版本：`qwen3-vl-plus-2025-12-19`
- Max Output：`16,384`
- Context Window：`131,072`
- Max tokens per image：`16,384`
- Mode：`Non-thinking mode`

### 4.2 价格（按官方页面，Global）

- 输入（`0 < token <= 32K`）：`¥0.004 / 1K tokens`
- 输出（`0 < token <= 32K`）：`¥0.016 / 1K tokens`
- 输入（`32K < token <= 128K`）：`¥0.008 / 1K tokens`
- 输出（`32K < token <= 128K`）：`¥0.032 / 1K tokens`

### 4.3 限流（qwen3-vl-plus）

- 中国大陆站点：标准 `10 QPS`、`1,200,000 TPM`，QPS 上限 `100`
- 国际站点：标准 `5 QPS`、`480,000 TPM`，QPS 上限 `100`

## 5. 实测性能（你的可用端点）

测试端点：`https://api.newcoin.top/v1/chat/completions`
模型：`qwen3-vl-plus`

### 5.1 连通性与返回格式

- 非流式图文请求：`HTTP 200`
- 返回体格式为 OpenAI 兼容 `choices[0].message.content`，并包含 `usage`。

一次样本（图文）：

- 耗时：`13,094 ms`
- usage：`prompt_tokens=2517, completion_tokens=49, total_tokens=2566`

### 5.2 小样本延迟与 token（3 次）

1. 文本请求（`stream=false`）
- 延迟：`2057ms, 2451ms, 2502ms`
- 平均：`2336ms`
- 平均 token：`prompt 14 / completion 22 / total 36`

2. 图文请求（`stream=false`，单图 URL）
- 延迟：`10037ms, 11475ms, 12076ms`
- 平均：`11196ms`
- 平均 token：`prompt 2517 / completion 29 / total 2546`

3. 图文流式（`stream=true`，单次）
- 首 token：`14110ms`
- 总耗时：`14392ms`
- 流片段数：`30`

### 5.3 性能观察

1. 图像输入会显著抬高 prompt token（本次约 2500+），是成本和时延主要来源。
2. 流式在图文场景下首 token 并不一定更快，首 token 常受视觉编码阶段影响。
3. 如果业务需要 <3s 交互，建议把 qwen3-vl-plus 放到“异步分析”链路，而非主对话阻塞路径。

## 6. 适用场景与落地建议

### 6.1 适用场景

1. 屏幕理解/GUI 代理前置：识别按钮、区域、状态并输出结构化动作建议。
2. 多模态问答：用户上传图片 + 文字问题，返回解释、摘要、风险点。
3. 文档视觉解析：流程图、报表、PPT、白板照片提取关键信息。
4. 质检与巡检：基于图片进行缺陷描述与规则判断。

### 6.2 不适合场景

1. 高并发、超低延迟（<1s）在线主链路。
2. 无需视觉能力的普通文本任务（成本不优）。

### 6.3 接入建议

1. 请求层
- 使用 OpenAI 兼容格式：`messages[].content` 中混合 `text` 与 `image_url`。
- `base_url` 必须是 `.../v1`，否则易命中 HTML 页面导致 JSON 解析失败。

2. 成本层
- 先做图片压缩与裁剪（仅上传任务相关区域）。
- 为纯文本问题路由到文本模型，避免视觉模型误用。

3. 产品层
- 前台可先返回“已收到，正在分析图像”，异步回填结果。
- 对图文任务设置单独超时和重试策略（建议 > 30s）。

## 7. 最小调用示例

当前目录下已提供脚本：

`/Users/zhangfeng/Desktop/Linux/DesktopRobot/项目探索内容/2026-02-28-千问VL模型使用探索/scripts/qwen3_vl_plus_demo.py`

示例（URL 图）：

```bash
cd /Users/zhangfeng/Desktop/Linux/DesktopRobot/项目探索内容/2026-02-28-千问VL模型使用探索
OPENAI_BASE_URL="https://api.newcoin.top/v1" \
OPENAI_API_KEY="<your_key>" \
LLM_MODEL="qwen3-vl-plus" \
python3 scripts/qwen3_vl_plus_demo.py \
  --image-url "https://dashscope.oss-cn-beijing.aliyuncs.com/images/dog_and_girl.jpeg" \
  --prompt "请描述图片并给出关键风险点"
```

## 8. 风险与待补充

1. 本报告性能样本量较小（n=3），用于工程决策时建议补充压测曲线（并发 1/5/10）。
2. 官方模型卡中的部分 benchmark 以图表图片给出，当前未逐项转录数值。
3. 第三方网关层（非官方直连）可能引入额外时延与限流策略，需与官方口径分开评估。

## 9. 参考来源

1. DashScope 模型列表（qwen3-vl-plus 规格）
- https://www.alibabacloud.com/help/en/model-studio/models

2. DashScope 计费页面（qwen3-vl-plus）
- https://www.alibabacloud.com/help/zh/model-studio/model-pricing

3. DashScope 限流页面（qwen3-vl-plus）
- https://www.alibabacloud.com/help/zh/model-studio/rate-limit

4. DashScope OpenAI 兼容视觉调用文档
- https://help.aliyun.com/zh/model-studio/vision

5. Qwen 官方模型卡（Qwen3-VL 系列能力）
- https://huggingface.co/Qwen/Qwen3-VL-235B-A22B-Instruct/blob/main/README.md
- https://huggingface.co/Qwen/Qwen3-VL-235B-A22B-Instruct/raw/main/README.md
