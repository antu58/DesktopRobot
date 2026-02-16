# Desktop Robot

桌面端 AI 机器人项目 —— 在本地桌面环境运行的智能助手与自动化机器人。

## 项目简介

本项目用于开发一款**桌面端 AI 机器人**，可在 macOS / Windows / Linux 等桌面系统中运行，提供对话、任务执行、系统交互等能力。

## 目标功能（规划）

- **智能对话**：基于大语言模型的自然语言交互
- **桌面自动化**：控制窗口、快捷键、文件与剪贴板等
- **多模态**：支持文字、语音、图像输入与输出
- **本地优先**：优先本地推理与隐私保护，可选云端增强

## 技术栈

项目技术选型待定，可根据需求选择：

- **桌面框架**：SwiftUI (macOS)、Electron、Tauri 等
- **AI 能力**：本地模型 (Ollama / llama.cpp) 或 API (OpenAI / 国产大模型)
- **语言**：Swift 6 (Apple 生态) / TypeScript / Rust 等

## 快速开始

```bash
# 克隆仓库（若从远程拉取）
git clone <repository-url>
cd DesktopRobot

# 一键本地 Docker 部署 Soul 服务
./deploy_local.sh
```

可选命令：
- `./deploy_local.sh status`
- `./deploy_local.sh logs`
- `./deploy_local.sh down`
- `./deploy_local.sh restart`

## 开发说明

- 若采用 **Swift / SwiftUI**，将遵循 Swift 6 编码规范与 SwiftUI 最佳实践
- 若采用 **macOS** 开发，将兼容当前主流 macOS 版本

## 许可证

待定。

---

*README 会随项目进展持续更新。*
