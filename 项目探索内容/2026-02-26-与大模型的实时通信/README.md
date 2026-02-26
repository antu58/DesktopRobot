# 2026-02-26 与大模型的实时通信

## 建议阅读顺序

1. [今日验证结论](/Users/zhangfeng/Desktop/Linux/DesktopRobot/项目探索内容/2026-02-26-与大模型的实时通信/2026-02-26-验证总结.md)
2. [技术方案选型记录](/Users/zhangfeng/Desktop/Linux/DesktopRobot/项目探索内容/2026-02-26-与大模型的实时通信/技术方案选型记录.md)
3. [完整技术方案：实时引擎与双工作流](/Users/zhangfeng/Desktop/Linux/DesktopRobot/项目探索内容/2026-02-26-与大模型的实时通信/完整技术方案-实时引擎与双工作流.md)
4. [单路流式 ASR POC](/Users/zhangfeng/Desktop/Linux/DesktopRobot/项目探索内容/2026-02-26-与大模型的实时通信/single-stream-asr-poc/README.md)
5. [CosyVoice TTS Playground](/Users/zhangfeng/Desktop/Linux/DesktopRobot/项目探索内容/2026-02-26-与大模型的实时通信/cosyvoice-tts-playground/README.md)

## 当日产出

1. 完成 FunASR 单路流式 ASR 链路（WebRTC -> Go -> FunASR -> 实时文本回传）。
2. 完成 CosyVoice 本地化服务，支持：
   - 预置音色合成
   - 音色复刻（含 `m4a`）
   - clone_id 复用
   - 真流式音频回传接口与测试页
3. 固化固定端口与 docker 启动方式，支持镜像缓存优先。

## 目录说明

1. `single-stream-asr-poc/`：ASR 链路原型。
2. `cosyvoice-tts-playground/`：TTS 复刻与流式测试。
3. `CosyVoice/`：上游仓库镜像（用于能力调研与参考）。
4. `我的音色.m4a`：本地测试素材。
