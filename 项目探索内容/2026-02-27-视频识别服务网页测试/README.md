# 视频识别服务网页测试（摄像头 + 关键事件日志）

## 目标

单独启动一个网页视频识别服务，验证以下链路：

1. 浏览器实时显示摄像头画面。
2. 每秒发送 1 帧到后端。
3. 快路径：每帧执行 `YOLO + OCR + 人物跟踪 + 人脸特征记忆 + 事件状态机`。
4. 慢路径：仅“明显变化/关键事件”触发本地 VLM（约 1B）。
5. 网页摘要区仅输出时间序关键日志（不做复杂卡片）。

## 目录

```text
2026-02-27-视频识别服务网页测试/
  app/main.py
  web/index.html
  requirements.txt
```

## 默认模型

1. YOLO：`yolov8n.pt`
2. OCR：`easyocr`（`ch_sim,en`）
3. 人脸特征：`ResNet18` 特征向量（本地匹配记忆）
4. 慢路径 VLM：`Salesforce/blip-image-captioning-large`（约 1B）

> 说明：`blip-image-captioning-large`更偏图像描述模型，语义能力弱于更大 VLM，但更容易本地跑通。

## 运行

```bash
cd /Users/zhangfeng/Desktop/Linux/DesktopRobot/项目探索内容/2026-02-27-视频识别服务网页测试
python3.13 -m venv .venv
source .venv/bin/activate
pip install -r requirements.txt
uvicorn app.main:app --host 0.0.0.0 --port 18301
```

打开网页：

`http://127.0.0.1:18301`

## 关键参数（环境变量）

1. `YOLO_MODEL`：默认 `yolov8n.pt`
2. `YOLO_CONF`：默认 `0.25`
3. `OCR_LANGS`：默认 `ch_sim,en`
4. `VLM_MODEL`：默认 `Salesforce/blip-image-captioning-large`
5. `VLM_KEYFRAME_THRESHOLD`：默认 `0.12`
6. `VLM_COOLDOWN_MS`：默认 `2500`

示例（切更轻慢路径模型）：

```bash
VLM_MODEL=Salesforce/blip-image-captioning-base uvicorn app.main:app --host 0.0.0.0 --port 18301
```

## 健康检查

`GET /healthz`

返回项包含：设备类型（CPU/GPU）、模型名、VLM 是否加载成功等。

## 当前实现说明

1. 快路径固定执行：
   - 人体检测（YOLO）+ OCR
   - 简单多目标跟踪（IoU Tracker）
   - 人脸检测（Haar）+ 人脸 embedding 匹配（ResNet18）
2. 关键事件日志（时间序）：
   - 人物进入/离开画面
   - 人数变化
   - 疑似使用手机（开始/结束）
   - 疑似进食动作（开始/结束）
   - 两人面对面交流（开始/结束）
3. 慢路径 VLM：
   - 仅在场景变化较大或关键事件出现时触发
   - 用于补充场景文本，不替代规则事件判断
