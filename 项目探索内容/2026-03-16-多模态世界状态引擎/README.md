## 多模态世界状态引擎探索（2026-03-16）

### 概述
举个例子，一个人在斑马线过马路，一辆车开过来了并未减速，后面有人呼叫提醒 ...... 这个场景下车，过马路的人，呼喊的人都是一个世界元素，观察者通过对每个元素一段时间连续变化的分析才能得出这个事件的因果（预测）以及做出提前反应。但是每一秒，每一毫秒，我们的感官获取的都是一个独立状态，这个就是世界状态。我们通过连续的事件状态抽出离散的事件。现在我们需要做一个程序完成传感器数据到世界状态的转化，就是世界状态引擎，我们用4D scene graphs作为世界状态的结构化体现。

### 数据流架构（分层视角）

#### 1. 传感器层（Sensors）
- **输入内容**：
  - Camera：视频帧 / 图像序列
  - Microphone：音频流
  - **未来扩展**：Radar / Depth / IMU / GPS 等
- **职责**：只负责提供**原始数据流**，不做语义理解。

#### 2. 感知层（Perception）
- **核心任务：识别 / 检测 / 跟踪**
- **子模块**：
  - **Object Detection**：检测人 / 车 / 物体等类别
  - **Object Tracking**：为检测到的实体分配 `entity_id`，维护轨迹
  - **Scene Recognition**：场景 / 环境类别识别（如 street / crosswalk / sidewalk）
  - **Audio Understanding**：语音内容、声源方向、是否有人喊话等
- **典型输出**（每一帧 / 每一小段时间）：
  - `object_list`：
    - `person_1`
    - `car_3`
    - `crosswalk_1`

#### 3. 状态更新层（State Update）
- **核心作用：把离散感知结果整合成连续的「世界状态快照」**
- **更新内容**（每 20–50ms 一次循环）：
  - `Entity State`：
    - `position`：位置
    - `velocity`：速度
    - `direction`：朝向
  - `Environment State`：
    - `street` / `crosswalk` / `sidewalk` 等静态结构
  - `Relations`（关系）：
    - `approaching`：接近
    - `on`：在……之上/之中
    - `facing`：面向
- **输出形式**：
  - **World State Snapshot**：某一时刻下的完整世界状态快照（为 4D Graph 提供原材料）。

#### 4. 世界状态（World State, 4D Graph）
- **这是引擎的「核心数据结构」**。
- **图结构组成**：
  - `Nodes`（节点，实体）：
    - `person_1`
    - `car_2`
    - `crosswalk`
  - `Edges`（边，关系）：
    - `car_2 → approaching → person_1`
    - `person_1 → on → crosswalk`
  - `Timestamp`（时间）：
    - 为每个世界状态打上时间戳，形成「时序图」（4D：3D + time）。
- **特点**：
  - 持续随时间更新，可支持回放、查询、预测。
  - 为上层事件抽取 / LLM 解释 / 控制算法提供统一的「世界观」。

#### 5. 事件抽取（Event Extractor）
- **目标：从连续的世界状态中提取「离散的短事件」**。
- **事件示例**：
  - `enter_region` / `enter_crosswalk`
  - `approaching`（车接近行人）
  - `shout_warning`（有人大声警告）
  - `leave_region`
- **触发方式**：
  - **不是每一帧都有事件**，而是由「状态变化」触发，例如：
    - `person_1` 的位置从人行道区域进入人行横道区域 → `enter_crosswalk`
    - `car_2` 与 `person_1` 的距离持续缩短并低于阈值 → `approaching`

#### 6. 输出接口（Outputs）
- 引擎向外部提供两种核心输出：
  - **World State Stream（世界状态流）**：
    - 高频更新（20–50ms 级别）
    - 内容示例：
      - `person_1 walking on crosswalk`
      - `car_2 approaching person_1`
    - 适用场景：
      - 实时推理 / 预测
      - 机器人 / 控制器闭环控制
  - **Event Stream（事件流）**：
    - 低频、离散事件：
      - `08:30:02 person_1 enter_crosswalk`
      - `08:30:03 car_2 approaching`
      - `08:30:04 bystander shout_warning`
    - 适用场景：
      - 作为 LLM 输入（结构化上下文）
      - 日志与可视化回放
      - 语义解释 / 报警系统

#### 一个完整时刻的示例（示意）
- **World State**（08:30:02）：
  - `nodes`：
    - `person_1`
    - `car_2`
    - `crosswalk`
  - `edges`：
    - `person_1 on crosswalk`
    - `car_2 approaching person_1`
- **Event Stream**：
  - `08:30:01 person_1 enter_crosswalk`
  - `08:30:02 car_2 approaching`
  - `08:30:03 shout_warning`

#### 最关键的一句话（边界划分）
- **多模态世界状态引擎的职责是**：
  - 从 **传感器** → 抽象出 **实体** → 形成 **关系** → 构建统一的 **世界状态** → 提取基础 **事件**。
- **它不直接负责**：
  - 高层 **预测**
  - 策略 **决策**
  - 具体 **控制**（这些可以作为后续模块，消费 World State / Event Stream）。
