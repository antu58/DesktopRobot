"use strict";

const statusEl = document.getElementById("status");
const messagesEl = document.getElementById("messages");
const inputBox = document.getElementById("inputBox");
const sendBtn = document.getElementById("sendBtn");
const pingBtn = document.getElementById("pingBtn");
const clearBtn = document.getElementById("clearBtn");
const modelInput = document.getElementById("modelInput");
const systemInput = document.getElementById("systemInput");
const tempInput = document.getElementById("tempInput");
const maxTokensInput = document.getElementById("maxTokensInput");
const dotEl = document.getElementById("dot");
const headTextEl = document.getElementById("headText");

const history = [];
let busy = false;

function setStatus(text, ok = null) {
  statusEl.textContent = text;
  if (ok === true) {
    dotEl.classList.add("ok");
    headTextEl.textContent = "模型服务可用";
  } else if (ok === false) {
    dotEl.classList.remove("ok");
    headTextEl.textContent = "模型服务异常";
  }
}

function appendMsg(role, content) {
  const div = document.createElement("div");
  div.className = `msg ${role}`;
  div.textContent = content;
  messagesEl.appendChild(div);
  messagesEl.scrollTop = messagesEl.scrollHeight;
}

function lockUI(locked) {
  busy = locked;
  sendBtn.disabled = locked;
  inputBox.disabled = locked;
  sendBtn.style.opacity = locked ? "0.7" : "1";
}

async function pingModel() {
  try {
    setStatus("正在检查模型服务...");
    const resp = await fetch("/api/models");
    const data = await resp.json();
    if (!resp.ok || !data.ok) {
      throw new Error(data.error || `HTTP ${resp.status}`);
    }
    const models = Array.isArray(data.data?.data) ? data.data.data : [];
    const names = models.map((m) => m.id || m.model || m.name).filter(Boolean);
    const details = names.length ? names.join(", ") : "未返回模型列表";
    setStatus(`模型服务已连接\n可用模型: ${details}`, true);
  } catch (err) {
    setStatus(`模型服务不可用: ${err.message}`, false);
  }
}

async function sendMessage() {
  const text = inputBox.value.trim();
  if (!text || busy) return;

  appendMsg("user", text);
  history.push({ role: "user", content: text });
  inputBox.value = "";
  lockUI(true);
  setStatus("模型生成中...");

  try {
    const payload = {
      model: modelInput.value.trim(),
      system_prompt: systemInput.value,
      temperature: Number(tempInput.value),
      max_tokens: Number(maxTokensInput.value),
      message: text,
      history: history.slice(0, -1),
    };
    const resp = await fetch("/api/chat", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(payload),
    });
    const data = await resp.json();
    if (!resp.ok || !data.ok) {
      throw new Error(data.error || `HTTP ${resp.status}`);
    }
    const answer = (data.reply || "").trim() || "(空回复)";
    appendMsg("assistant", answer);
    history.push({ role: "assistant", content: answer });
    const usage = data.usage
      ? `prompt_tokens=${data.usage.prompt_tokens || 0}, completion_tokens=${data.usage.completion_tokens || 0}`
      : "未返回 token 统计";
    setStatus(`完成\n${usage}`, true);
  } catch (err) {
    appendMsg("error", `请求失败: ${err.message}`);
    setStatus(`请求失败: ${err.message}`, false);
  } finally {
    lockUI(false);
    inputBox.focus();
  }
}

function clearChat() {
  history.length = 0;
  messagesEl.innerHTML = "";
  setStatus("已清空对话。");
}

sendBtn.addEventListener("click", sendMessage);
pingBtn.addEventListener("click", pingModel);
clearBtn.addEventListener("click", clearChat);
inputBox.addEventListener("keydown", (event) => {
  if (event.key === "Enter" && !event.shiftKey) {
    event.preventDefault();
    sendMessage();
  }
});

appendMsg("assistant", "你好，我已经准备好。你可以先试一句“用一句话自我介绍”。");
pingModel();
