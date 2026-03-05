"use strict";

const fs = require("fs");
const path = require("path");
const http = require("http");
const { URL } = require("url");

const HOST = process.env.WEB_CHAT_HOST || "127.0.0.1";
const PORT = Number(process.env.WEB_CHAT_PORT || "18080");
const MODEL_BASE_URL = process.env.MODEL_BASE_URL || "http://127.0.0.1:8000/v1";
const DEFAULT_MODEL = process.env.MODEL_NAME || "Qwen3.5-0.8B-Q4_K_M.gguf";
const STATIC_DIR = path.join(__dirname, "public");

function sendJson(res, statusCode, payload) {
  const body = JSON.stringify(payload);
  res.writeHead(statusCode, {
    "Content-Type": "application/json; charset=utf-8",
    "Content-Length": Buffer.byteLength(body),
  });
  res.end(body);
}

function sendFile(res, filePath) {
  fs.readFile(filePath, (err, data) => {
    if (err) {
      res.writeHead(404, { "Content-Type": "text/plain; charset=utf-8" });
      res.end("Not found");
      return;
    }
    const ext = path.extname(filePath).toLowerCase();
    const contentType = ext === ".html"
      ? "text/html; charset=utf-8"
      : ext === ".js"
        ? "application/javascript; charset=utf-8"
        : ext === ".css"
          ? "text/css; charset=utf-8"
          : "application/octet-stream";
    res.writeHead(200, { "Content-Type": contentType });
    res.end(data);
  });
}

function collectJsonBody(req) {
  return new Promise((resolve, reject) => {
    let raw = "";
    req.on("data", (chunk) => {
      raw += chunk;
      if (raw.length > 1024 * 1024) {
        reject(new Error("body too large"));
        req.destroy();
      }
    });
    req.on("end", () => {
      if (!raw) {
        resolve({});
        return;
      }
      try {
        resolve(JSON.parse(raw));
      } catch (err) {
        reject(new Error("invalid json body"));
      }
    });
    req.on("error", reject);
  });
}

async function fetchModelList() {
  const resp = await fetch(`${MODEL_BASE_URL}/models`);
  if (!resp.ok) {
    throw new Error(`model list failed: HTTP ${resp.status}`);
  }
  return resp.json();
}

async function chatCompletion(payload) {
  const resp = await fetch(`${MODEL_BASE_URL}/chat/completions`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(payload),
  });
  const text = await resp.text();
  if (!resp.ok) {
    throw new Error(`chat failed: HTTP ${resp.status} body=${text.slice(0, 300)}`);
  }
  return JSON.parse(text);
}

const server = http.createServer(async (req, res) => {
  try {
    const parsed = new URL(req.url, `http://${req.headers.host}`);
    const pathname = parsed.pathname;

    if (req.method === "GET" && pathname === "/health") {
      sendJson(res, 200, { ok: true, model_base_url: MODEL_BASE_URL, default_model: DEFAULT_MODEL });
      return;
    }

    if (req.method === "GET" && pathname === "/api/models") {
      try {
        const data = await fetchModelList();
        sendJson(res, 200, { ok: true, data });
      } catch (err) {
        sendJson(res, 502, { ok: false, error: String(err.message || err) });
      }
      return;
    }

    if (req.method === "POST" && pathname === "/api/chat") {
      const body = await collectJsonBody(req);
      const model = typeof body.model === "string" && body.model.trim() ? body.model.trim() : DEFAULT_MODEL;
      const systemPrompt = typeof body.system_prompt === "string" ? body.system_prompt.trim() : "";
      const userMessage = typeof body.message === "string" ? body.message.trim() : "";
      const history = Array.isArray(body.history) ? body.history : [];

      if (!userMessage) {
        sendJson(res, 400, { ok: false, error: "message is required" });
        return;
      }

      const messages = [];
      if (systemPrompt) {
        messages.push({ role: "system", content: systemPrompt });
      }
      for (const item of history) {
        if (!item || typeof item !== "object") continue;
        if (item.role !== "user" && item.role !== "assistant") continue;
        if (typeof item.content !== "string" || !item.content.trim()) continue;
        messages.push({ role: item.role, content: item.content });
      }
      messages.push({ role: "user", content: userMessage });

      const payload = {
        model,
        messages,
        temperature: Number.isFinite(Number(body.temperature)) ? Number(body.temperature) : 0.7,
        max_tokens: Number.isFinite(Number(body.max_tokens)) ? Number(body.max_tokens) : 256,
      };

      const data = await chatCompletion(payload);
      const choice = data.choices && data.choices[0] && data.choices[0].message ? data.choices[0].message : {};
      sendJson(res, 200, {
        ok: true,
        reply: typeof choice.content === "string" ? choice.content : "",
        usage: data.usage || null,
        raw: data,
      });
      return;
    }

    const filePath = pathname === "/"
      ? path.join(STATIC_DIR, "index.html")
      : path.join(STATIC_DIR, pathname.replace(/^\/+/, ""));
    if (!filePath.startsWith(STATIC_DIR)) {
      sendJson(res, 400, { ok: false, error: "invalid path" });
      return;
    }
    sendFile(res, filePath);
  } catch (err) {
    sendJson(res, 500, { ok: false, error: String(err.message || err) });
  }
});

server.listen(PORT, HOST, () => {
  console.log(`[qwen35_web_chat] listening on http://${HOST}:${PORT}`);
  console.log(`[qwen35_web_chat] model base url: ${MODEL_BASE_URL}`);
  console.log(`[qwen35_web_chat] default model: ${DEFAULT_MODEL}`);
});
