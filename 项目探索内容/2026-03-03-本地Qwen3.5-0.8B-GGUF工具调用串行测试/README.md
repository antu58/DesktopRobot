# 2026-03-03 本地 Qwen3.5-0.8B GGUF 工具调用串行测试

本目录独立沉淀“本地 Qwen3.5-0.8B GGUF 工具调用串行测试”相关结论与脚本，不归属液态神经网络替换专题。

## 文档

1. 测试成果：
   [/Users/zhangfeng/Desktop/Linux/DesktopRobot/项目探索内容/2026-03-03-本地Qwen3.5-0.8B-GGUF工具调用串行测试/2026-03-03-本地Qwen3.5-0.8B-GGUF工具调用串行测试成果.md](/Users/zhangfeng/Desktop/Linux/DesktopRobot/项目探索内容/2026-03-03-本地Qwen3.5-0.8B-GGUF工具调用串行测试/2026-03-03-本地Qwen3.5-0.8B-GGUF工具调用串行测试成果.md)

## 脚本

1. 基准脚本：
   [/Users/zhangfeng/Desktop/Linux/DesktopRobot/项目探索内容/2026-03-03-本地Qwen3.5-0.8B-GGUF工具调用串行测试/scripts/qwen35_official_api_chat.py](/Users/zhangfeng/Desktop/Linux/DesktopRobot/项目探索内容/2026-03-03-本地Qwen3.5-0.8B-GGUF工具调用串行测试/scripts/qwen35_official_api_chat.py)
2. 启动 0.8B 本地服务（Docker + llama.cpp）：
   [/Users/zhangfeng/Desktop/Linux/DesktopRobot/项目探索内容/2026-03-03-本地Qwen3.5-0.8B-GGUF工具调用串行测试/scripts/start_qwen35_08b_service.sh](/Users/zhangfeng/Desktop/Linux/DesktopRobot/项目探索内容/2026-03-03-本地Qwen3.5-0.8B-GGUF工具调用串行测试/scripts/start_qwen35_08b_service.sh)
3. 停止 0.8B 本地服务：
   [/Users/zhangfeng/Desktop/Linux/DesktopRobot/项目探索内容/2026-03-03-本地Qwen3.5-0.8B-GGUF工具调用串行测试/scripts/stop_qwen35_08b_service.sh](/Users/zhangfeng/Desktop/Linux/DesktopRobot/项目探索内容/2026-03-03-本地Qwen3.5-0.8B-GGUF工具调用串行测试/scripts/stop_qwen35_08b_service.sh)
4. 启动网页测试后端：
   [/Users/zhangfeng/Desktop/Linux/DesktopRobot/项目探索内容/2026-03-03-本地Qwen3.5-0.8B-GGUF工具调用串行测试/scripts/start_qwen35_web_chat.sh](/Users/zhangfeng/Desktop/Linux/DesktopRobot/项目探索内容/2026-03-03-本地Qwen3.5-0.8B-GGUF工具调用串行测试/scripts/start_qwen35_web_chat.sh)
5. 停止网页测试后端：
   [/Users/zhangfeng/Desktop/Linux/DesktopRobot/项目探索内容/2026-03-03-本地Qwen3.5-0.8B-GGUF工具调用串行测试/scripts/stop_qwen35_web_chat.sh](/Users/zhangfeng/Desktop/Linux/DesktopRobot/项目探索内容/2026-03-03-本地Qwen3.5-0.8B-GGUF工具调用串行测试/scripts/stop_qwen35_web_chat.sh)

## 快速开始（对话网页测试）

```bash
cd /Users/zhangfeng/Desktop/Linux/DesktopRobot

# 1) 启动 0.8B 模型服务（默认 127.0.0.1:8000）
bash "项目探索内容/2026-03-03-本地Qwen3.5-0.8B-GGUF工具调用串行测试/scripts/start_qwen35_08b_service.sh"

# 2) 启动网页测试后端（默认 127.0.0.1:18080）
bash "项目探索内容/2026-03-03-本地Qwen3.5-0.8B-GGUF工具调用串行测试/scripts/start_qwen35_web_chat.sh"
```

打开：`http://127.0.0.1:18080`

网页后端文件：
[/Users/zhangfeng/Desktop/Linux/DesktopRobot/项目探索内容/2026-03-03-本地Qwen3.5-0.8B-GGUF工具调用串行测试/web_chat/server.js](/Users/zhangfeng/Desktop/Linux/DesktopRobot/项目探索内容/2026-03-03-本地Qwen3.5-0.8B-GGUF工具调用串行测试/web_chat/server.js)

网页前端文件：
[/Users/zhangfeng/Desktop/Linux/DesktopRobot/项目探索内容/2026-03-03-本地Qwen3.5-0.8B-GGUF工具调用串行测试/web_chat/public/index.html](/Users/zhangfeng/Desktop/Linux/DesktopRobot/项目探索内容/2026-03-03-本地Qwen3.5-0.8B-GGUF工具调用串行测试/web_chat/public/index.html)
