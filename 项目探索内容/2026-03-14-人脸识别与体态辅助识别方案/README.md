# 2026-03-14 人脸识别与体态辅助识别方案

## 文档

1. [人脸识别主特征 + 人体 ReID/步态辅助方案](/Users/zhangfeng/Desktop/Linux/DesktopRobot/项目探索内容/2026-03-14-人脸识别与体态辅助识别方案/2026-03-14-人脸识别主特征+人体ReID步态辅助方案.md)
2. [多方案横向对比与 Docker 落地建议](/Users/zhangfeng/Desktop/Linux/DesktopRobot/项目探索内容/2026-03-14-人脸识别与体态辅助识别方案/2026-03-14-多方案横向对比与Docker落地建议.md)

## 结论

1. 主链路：人脸识别（1:1 验证、1:N 检索）。
2. 辅链路：人体 ReID（MVP）+ 步态（增强）。
3. 输出唯一 `person_id`，并采用分数融合与阈值分级判定。
