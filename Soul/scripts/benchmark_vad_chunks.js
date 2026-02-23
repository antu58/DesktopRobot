#!/usr/bin/env node

const http = require("http");
const https = require("https");

const corpus = [
  "早上好，今天上海天气怎么样",
  "我十点有个会，帮我快速整理一下待办重点",
  "中午想吃清淡一点，有什么外卖建议",
  "我现在有点困，下午怎么安排效率会更高",
  "下班后想去附近散步，有推荐路线吗",
  "周末想带孩子出去玩，半天行程怎么安排",
  "家里牛奶快喝完了，提醒我下班买一盒",
  "我在地铁上，给我一个五分钟能读完的新闻摘要",
  "我晚上想做番茄炒蛋和青菜，购物清单给我",
  "今天消费有点多，帮我做个简单节省计划",
  "我准备开始学习英语口语，第一周怎么开始",
  "我现在情绪有点低，给我三个能马上做的小动作",
];

function getConfig() {
  const baseURL = (process.env.OPENAI_BASE_URL || "https://api.openai.com/v1").replace(/\/$/, "");
  const apiKey = process.env.OPENAI_API_KEY || "";
  const model = process.env.LLM_MODEL || process.env.MODEL || "gpt-4o-mini";
  const rounds = Number(process.env.BENCH_ROUNDS || "1");
  const maxTokens = Number(process.env.BENCH_MAX_TOKENS || "120");
  const timeoutMs = Number(process.env.BENCH_TIMEOUT_MS || "120000");
  return { baseURL, apiKey, model, rounds, maxTokens, timeoutMs };
}

function percentile(arr, p) {
  if (arr.length === 0) return null;
  const sorted = [...arr].sort((a, b) => a - b);
  const idx = Math.ceil((p / 100) * sorted.length) - 1;
  return sorted[Math.max(0, Math.min(idx, sorted.length - 1))];
}

function avg(arr) {
  if (arr.length === 0) return null;
  return arr.reduce((a, b) => a + b, 0) / arr.length;
}

function pickTransport(urlObj) {
  return urlObj.protocol === "https:" ? https : http;
}

function streamOnce(cfg, text) {
  return new Promise((resolve) => {
    const u = new URL(cfg.baseURL + "/chat/completions");
    const transport = pickTransport(u);
    const body = JSON.stringify({
      model: cfg.model,
      stream: true,
      temperature: 0.2,
      max_tokens: cfg.maxTokens,
      messages: [
        {
          role: "system",
          content: "你是实时语音助手。请用简短口语回答，优先给3条以内要点。",
        },
        { role: "user", content: text },
      ],
    });

    const t0 = Date.now();
    let statusCode = null;
    let headerMs = null;
    let ttftMs = null;
    let totalMs = null;
    let outputChars = 0;
    let output = "";
    let doneFlag = false;
    let rest = "";

    const finish = (extra = {}) => {
      if (doneFlag) return;
      doneFlag = true;
      resolve({
        input: text,
        statusCode,
        headerMs,
        ttftMs,
        totalMs: totalMs ?? Date.now() - t0,
        outputChars,
        charsPerSecAfterFirstToken:
          ttftMs == null
            ? null
            : Number(
                (outputChars / (Math.max(1, (Date.now() - t0) - ttftMs) / 1000)).toFixed(2)
              ),
        outputPreview: output.slice(0, 120),
        ...extra,
      });
    };

    const req = transport.request(
      {
        protocol: u.protocol,
        hostname: u.hostname,
        port: u.port || (u.protocol === "https:" ? 443 : 80),
        path: u.pathname + u.search,
        method: "POST",
        headers: {
          Authorization: `Bearer ${cfg.apiKey}`,
          "Content-Type": "application/json",
          "Content-Length": Buffer.byteLength(body),
        },
      },
      (res) => {
        statusCode = res.statusCode || null;
        headerMs = Date.now() - t0;

        if ((res.statusCode || 500) >= 300) {
          let errBody = "";
          res.setEncoding("utf8");
          res.on("data", (c) => {
            errBody += c;
          });
          res.on("end", () => finish({ error: `status_${res.statusCode}: ${errBody.slice(0, 300)}` }));
          return;
        }

        res.setEncoding("utf8");
        res.on("data", (chunk) => {
          rest += chunk;
          const lines = rest.split("\n");
          rest = lines.pop() || "";

          for (const line of lines) {
            const s = line.trim();
            if (!s.startsWith("data:")) continue;
            const data = s.slice(5).trim();
            if (data === "[DONE]") {
              totalMs = Date.now() - t0;
              finish();
              return;
            }
            let obj;
            try {
              obj = JSON.parse(data);
            } catch {
              continue;
            }
            const piece = obj?.choices?.[0]?.delta?.content || obj?.choices?.[0]?.message?.content || "";
            if (piece) {
              if (ttftMs == null) ttftMs = Date.now() - t0;
              output += piece;
              outputChars += piece.length;
            }
          }
        });

        res.on("end", () => {
          totalMs = Date.now() - t0;
          finish();
        });
      }
    );

    req.setTimeout(cfg.timeoutMs, () => {
      req.destroy(new Error(`timeout_${cfg.timeoutMs}ms`));
    });

    req.on("error", (err) => {
      finish({ error: err.message });
    });

    req.write(body);
    req.end();
  });
}

async function main() {
  const cfg = getConfig();
  if (!cfg.apiKey) {
    console.error("OPENAI_API_KEY is required");
    process.exit(1);
  }

  const rows = [];
  for (let round = 1; round <= cfg.rounds; round += 1) {
    for (let i = 0; i < corpus.length; i += 1) {
      const input = corpus[i];
      const result = await streamOnce(cfg, input);
      rows.push({ round, idx: i + 1, ...result });
      const tag = result.error ? "ERR" : "OK ";
      console.log(
        `[${tag}] round=${round} #${String(i + 1).padStart(2, "0")} ttft=${result.ttftMs}ms total=${result.totalMs}ms chars=${result.outputChars}`
      );
    }
  }

  const okRows = rows.filter((r) => !r.error && r.ttftMs != null);
  const ttfts = okRows.map((r) => r.ttftMs);
  const totals = okRows.map((r) => r.totalMs);
  const cps = okRows.map((r) => r.charsPerSecAfterFirstToken).filter((v) => v != null);

  const summary = {
    model: cfg.model,
    baseURL: cfg.baseURL,
    rounds: cfg.rounds,
    samples: rows.length,
    success: okRows.length,
    ttftMs: {
      avg: Number((avg(ttfts) || 0).toFixed(2)),
      p50: percentile(ttfts, 50),
      p90: percentile(ttfts, 90),
      max: ttfts.length ? Math.max(...ttfts) : null,
    },
    totalMs: {
      avg: Number((avg(totals) || 0).toFixed(2)),
      p50: percentile(totals, 50),
      p90: percentile(totals, 90),
      max: totals.length ? Math.max(...totals) : null,
    },
    charsPerSecAfterFirstToken: {
      avg: Number((avg(cps) || 0).toFixed(2)),
      p50: percentile(cps, 50),
      p90: percentile(cps, 90),
    },
  };

  console.log("\n=== SUMMARY ===");
  console.log(JSON.stringify(summary, null, 2));
  console.log("\n=== TOP SLOW TTFT ===");
  const topSlow = [...okRows].sort((a, b) => b.ttftMs - a.ttftMs).slice(0, 5);
  for (const item of topSlow) {
    console.log(
      `round=${item.round} #${item.idx} ttft=${item.ttftMs}ms total=${item.totalMs}ms input="${item.input}"`
    );
  }
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});

