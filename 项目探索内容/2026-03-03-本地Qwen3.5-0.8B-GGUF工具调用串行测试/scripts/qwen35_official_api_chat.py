#!/usr/bin/env python3
"""
Qwen3.5-0.8B official-style client script (OpenAI-compatible API).

Official serve command (from Qwen README):
  transformers serve --force-model Qwen/Qwen3.5-0.8B --port 8000 --continuous-batching

Environment variables:
  OPENAI_BASE_URL=http://localhost:8000/v1
  OPENAI_API_KEY=EMPTY
"""

from __future__ import annotations

import argparse
import json
import os
import re
import statistics
import sys
import time
from dataclasses import dataclass
from typing import Any, Dict, List

from openai import OpenAI

DEFAULT_CHAT_SYSTEM_PROMPT = (
    "你是手机端中文助手。回答尽量短，1-2句、优先不超过60字；"
    "语气自然温和；仅基于用户提供信息回答，不要脑补未给出的事实。"
)

DEFAULT_TOOL_SYSTEM_PROMPT = (
    "你是工具路由器。你必须且只能调用一个最匹配的工具函数来处理用户请求。"
    "不要输出自然语言解释，不要拒答，不要返回多个工具。"
)


def build_parser() -> argparse.ArgumentParser:
    p = argparse.ArgumentParser(description="Qwen3.5 official API chat client")
    p.add_argument("--model", default="Qwen/Qwen3.5-0.8B")
    p.add_argument("--prompt", default="你好，请用一句话介绍你自己。")
    p.add_argument("--max-tokens", type=int, default=2048)
    p.add_argument("--temperature", type=float, default=1.0)
    p.add_argument("--top-p", type=float, default=1.0)
    p.add_argument(
        "--top-k",
        type=int,
        default=None,
        help="Only send top_k when explicitly set; transformers serve rejects unknown extra keys.",
    )
    p.add_argument("--presence-penalty", type=float, default=2.0)
    p.add_argument("--enable-thinking", action="store_true")
    p.add_argument(
        "--system-prompt",
        default=DEFAULT_CHAT_SYSTEM_PROMPT,
        help="System prompt for normal chat mode.",
    )
    p.add_argument(
        "--tool-system-prompt",
        default=DEFAULT_TOOL_SYSTEM_PROMPT,
        help="System prompt for tool benchmark mode.",
    )
    p.add_argument(
        "--tool-api",
        choices=["chat", "responses"],
        default="chat",
        help="Tool benchmark API route.",
    )
    p.add_argument(
        "--with-tools",
        action="store_true",
        help="Send a function-calling request to validate tool-call behavior.",
    )
    p.add_argument(
        "--tool-bench",
        action="store_true",
        help="Run built-in 10-case tool-routing benchmark.",
    )
    p.add_argument(
        "--tool-bench-rounds",
        type=int,
        default=1,
        help="Repeat each benchmark case for multiple rounds.",
    )
    p.add_argument(
        "--base-url",
        default=os.getenv("OPENAI_BASE_URL", "http://localhost:8000/v1"),
        help="OpenAI-compatible API base URL.",
    )
    p.add_argument(
        "--api-key",
        default=os.getenv("OPENAI_API_KEY", "EMPTY"),
        help="API key for OpenAI-compatible endpoint.",
    )
    return p


def build_tools() -> List[Dict[str, Any]]:
    return [
        {
            "type": "function",
            "function": {
                "name": "todo_create",
                "description": "Create a new todo task.",
                "parameters": {
                    "type": "object",
                    "properties": {
                        "title": {"type": "string"},
                        "due_time": {"type": "string"},
                        "priority": {"type": "string", "enum": ["low", "medium", "high"]},
                        "tags": {"type": "array", "items": {"type": "string"}},
                    },
                    "required": ["title"],
                },
            },
        },
        {
            "type": "function",
            "function": {
                "name": "todo_query",
                "description": "Query todo tasks by filter conditions.",
                "parameters": {
                    "type": "object",
                    "properties": {
                        "status": {"type": "string", "enum": ["all", "pending", "done", "overdue"]},
                        "time_range": {"type": "string"},
                        "tag": {"type": "string"},
                        "keyword": {"type": "string"},
                    },
                },
            },
        },
        {
            "type": "function",
            "function": {
                "name": "todo_update",
                "description": "Update a todo task, such as marking completed.",
                "parameters": {
                    "type": "object",
                    "properties": {
                        "task_id": {"type": "string"},
                        "title": {"type": "string"},
                        "status": {"type": "string", "enum": ["pending", "done"]},
                        "priority": {"type": "string", "enum": ["low", "medium", "high"]},
                    },
                },
            },
        },
        {
            "type": "function",
            "function": {
                "name": "weather_current",
                "description": "Get current weather by city.",
                "parameters": {
                    "type": "object",
                    "properties": {
                        "location": {"type": "string"},
                        "units": {"type": "string", "enum": ["metric", "imperial"]},
                    },
                    "required": ["location"],
                },
            },
        },
        {
            "type": "function",
            "function": {
                "name": "weather_forecast",
                "description": "Get weather forecast for upcoming days.",
                "parameters": {
                    "type": "object",
                    "properties": {
                        "location": {"type": "string"},
                        "days": {"type": "integer", "minimum": 1, "maximum": 10},
                    },
                    "required": ["location"],
                },
            },
        },
        {
            "type": "function",
            "function": {
                "name": "map_place_search",
                "description": "Search places/POI around a location.",
                "parameters": {
                    "type": "object",
                    "properties": {
                        "query": {"type": "string"},
                        "city": {"type": "string"},
                        "radius_m": {"type": "integer"},
                    },
                    "required": ["query"],
                },
            },
        },
        {
            "type": "function",
            "function": {
                "name": "map_route_plan",
                "description": "Plan a route between two locations.",
                "parameters": {
                    "type": "object",
                    "properties": {
                        "origin": {"type": "string"},
                        "destination": {"type": "string"},
                        "mode": {"type": "string", "enum": ["driving", "walking", "transit", "cycling"]},
                    },
                    "required": ["origin", "destination"],
                },
            },
        },
        {
            "type": "function",
            "function": {
                "name": "calendar_create_event",
                "description": "Create a calendar event.",
                "parameters": {
                    "type": "object",
                    "properties": {
                        "title": {"type": "string"},
                        "start_time": {"type": "string"},
                        "duration_minutes": {"type": "integer"},
                        "participants": {"type": "array", "items": {"type": "string"}},
                    },
                    "required": ["title", "start_time"],
                },
            },
        },
        {
            "type": "function",
            "function": {
                "name": "reminder_set",
                "description": "Set a reminder notification.",
                "parameters": {
                    "type": "object",
                    "properties": {
                        "content": {"type": "string"},
                        "trigger_time": {"type": "string"},
                        "repeat": {"type": "string", "enum": ["none", "daily", "weekly"]},
                    },
                    "required": ["content", "trigger_time"],
                },
            },
        },
        {
            "type": "function",
            "function": {
                "name": "note_create",
                "description": "Create a quick note.",
                "parameters": {
                    "type": "object",
                    "properties": {
                        "title": {"type": "string"},
                        "content": {"type": "string"},
                        "tags": {"type": "array", "items": {"type": "string"}},
                    },
                    "required": ["content"],
                },
            },
        },
    ]


@dataclass(frozen=True)
class BenchCase:
    prompt: str
    expected_tool: str


BENCH_CASES: List[BenchCase] = [
    BenchCase("帮我创建一个待办：明天上午10点给客户回电话，优先级高。", "todo_create"),
    BenchCase("我今天还有哪些待办？只看工作标签。", "todo_query"),
    BenchCase("把待办“给客户回电话”标记为已完成。", "todo_update"),
    BenchCase("北京现在天气怎么样？", "weather_current"),
    BenchCase("帮我看上海未来三天的天气。", "weather_forecast"),
    BenchCase("在杭州帮我搜一下西湖附近的咖啡店。", "map_place_search"),
    BenchCase("从人民广场到虹桥火车站，给我规划地铁路线。", "map_route_plan"),
    BenchCase("下周一上午9点创建产品评审会议，时长60分钟。", "calendar_create_event"),
    BenchCase("提醒我今晚8点吃药。", "reminder_set"),
    BenchCase("记一条笔记：LFM2.5 作为意图路由速度很快。", "note_create"),
]


def extract_predicted_tool(choice: Any) -> str:
    tool_calls = getattr(choice, "tool_calls", None) or []
    if tool_calls:
        fn = getattr(tool_calls[0], "function", None)
        if fn is not None:
            return getattr(fn, "name", "") or "NO_TOOL"

    # Some backends may return tool call text in content.
    content = getattr(choice, "content", "") or ""
    if isinstance(content, str):
        for tool in build_tools():
            name = tool["function"]["name"]
            if f"{name}(" in content:
                return name
    return "NO_TOOL"


def extract_tool_from_text(text: str) -> str:
    if not text:
        return "NO_TOOL"

    for match in re.finditer(r"<tool_call>\s*(\{.*?\})\s*</tool_call>", text, re.DOTALL):
        payload = match.group(1)
        try:
            obj = json.loads(payload)
        except Exception:
            continue
        name = str(obj.get("name", "")).strip()
        if name:
            return name

    for tool in build_tools():
        name = tool["function"]["name"]
        if f'"name":"{name}"' in text or f'"name": "{name}"' in text or f"{name}(" in text:
            return name
    return "NO_TOOL"


def extract_predicted_tool_from_response_api(resp: Any) -> str:
    output = getattr(resp, "output", None) or []
    for item in output:
        item_type = getattr(item, "type", "") or ""
        if item_type in {"function_call", "tool_call"}:
            name = getattr(item, "name", "") or ""
            if name:
                return name
        if item_type == "message":
            content_items = getattr(item, "content", None) or []
            for c in content_items:
                text = getattr(c, "text", None)
                if text:
                    parsed = extract_tool_from_text(text)
                    if parsed != "NO_TOOL":
                        return parsed

    output_text = getattr(resp, "output_text", "") or ""
    return extract_tool_from_text(output_text)


def build_generation_config(args: argparse.Namespace) -> Dict[str, Any]:
    cfg: Dict[str, Any] = {
        "max_new_tokens": args.max_tokens,
        "temperature": args.temperature,
        "top_p": args.top_p,
        "presence_penalty": args.presence_penalty,
    }
    if args.top_k is not None:
        cfg["top_k"] = args.top_k
    return cfg


def run_tool_bench(client: OpenAI, args: argparse.Namespace) -> int:
    tools = build_tools()
    total = 0
    passed = 0
    latencies: List[float] = []

    print(f"[bench] cases={len(BENCH_CASES)} rounds={max(1, args.tool_bench_rounds)}")
    for round_idx in range(max(1, args.tool_bench_rounds)):
        print(f"[bench] round={round_idx + 1}")
        for idx, case in enumerate(BENCH_CASES, 1):
            extra_body: Dict[str, Any] = {
                # Official serve tool-calling docs recommend generation_config in extra_body.
                "generation_config": json.dumps(build_generation_config(args), ensure_ascii=False),
            }
            if args.enable_thinking:
                extra_body["enable_thinking"] = True

            t0 = time.time()
            try:
                if args.tool_api == "responses":
                    resp = client.responses.create(
                        model=args.model,
                        instructions=args.tool_system_prompt,
                        input=case.prompt,
                        tools=tools,
                        tool_choice="auto",
                        extra_body=extra_body,
                    )
                else:
                    resp = client.chat.completions.create(
                        model=args.model,
                        messages=[
                            {"role": "system", "content": args.tool_system_prompt},
                            {"role": "user", "content": case.prompt},
                        ],
                        max_tokens=args.max_tokens,
                        temperature=args.temperature,
                        top_p=args.top_p,
                        presence_penalty=args.presence_penalty,
                        tools=tools,
                        tool_choice="auto",
                        extra_body=extra_body,
                    )
            except Exception as e:
                print(f"[FAIL] #{idx} expected={case.expected_tool} predicted=REQUEST_ERROR")
                print(f"  error={e}")
                total += 1
                continue

            latency = time.time() - t0
            if args.tool_api == "responses":
                predicted = extract_predicted_tool_from_response_api(resp)
            else:
                choice = resp.choices[0].message
                predicted = extract_predicted_tool(choice)
            ok = predicted == case.expected_tool

            total += 1
            passed += 1 if ok else 0
            latencies.append(latency)
            print(
                f"[{'PASS' if ok else 'FAIL'}] #{idx} "
                f"expected={case.expected_tool} predicted={predicted} latency={latency:.2f}s"
            )

            if args.tool_api == "responses":
                output_text = getattr(resp, "output_text", "") or ""
                if output_text:
                    print(f"  output_text={output_text.replace(chr(10), ' ')[:220]}")
            else:
                tool_calls = getattr(choice, "tool_calls", None) or []
                if tool_calls:
                    fn = getattr(tool_calls[0], "function", None)
                    if fn is not None:
                        print(
                            "  tool_call="
                            + json.dumps(
                                {"name": getattr(fn, "name", ""), "arguments": getattr(fn, "arguments", "")},
                                ensure_ascii=False,
                            )
                        )
                else:
                    content = getattr(choice, "content", "") or ""
                    if content:
                        print(f"  content={str(content).replace(chr(10), ' ')[:180]}")

    acc = (passed / total * 100) if total else 0.0
    avg = statistics.mean(latencies) if latencies else 0.0
    p95 = sorted(latencies)[max(0, int(len(latencies) * 0.95) - 1)] if latencies else 0.0
    print(
        f"[bench-summary] accuracy={acc:.1f}% ({passed}/{total}) "
        f"avg_latency={avg:.2f}s p95_latency={p95:.2f}s"
    )
    return 0


def main() -> int:
    args = build_parser().parse_args()

    client = OpenAI(base_url=args.base_url, api_key=args.api_key)
    if args.tool_bench:
        return run_tool_bench(client, args)

    system_prompt = args.tool_system_prompt if args.with_tools else args.system_prompt
    messages: List[Dict[str, Any]] = [
        {"role": "system", "content": system_prompt},
        {"role": "user", "content": args.prompt},
    ]

    extra_body: Dict[str, Any] = {}
    if args.top_k is not None:
        extra_body["top_k"] = args.top_k
    if args.enable_thinking:
        extra_body["enable_thinking"] = True

    kwargs: Dict[str, Any] = {
        "model": args.model,
        "messages": messages,
        "max_tokens": args.max_tokens,
        "temperature": args.temperature,
        "top_p": args.top_p,
        "presence_penalty": args.presence_penalty,
    }
    extra_body["generation_config"] = json.dumps(build_generation_config(args), ensure_ascii=False)
    if extra_body:
        kwargs["extra_body"] = extra_body

    if args.with_tools:
        kwargs["tools"] = build_tools()
        kwargs["tool_choice"] = "auto"

    try:
        resp = client.chat.completions.create(**kwargs)
    except Exception as e:
        print(f"[error] request failed: {e}")
        print(
            "[hint] Ensure server is running with: "
            "transformers serve --force-model Qwen/Qwen3.5-0.8B --port 8000 --host 127.0.0.1"
        )
        return 1

    choice = resp.choices[0].message
    print(f"[model] {args.model}")
    if getattr(choice, "content", None):
        print(choice.content)

    tool_calls = getattr(choice, "tool_calls", None) or []
    if tool_calls:
        print("[tool_calls]")
        for call in tool_calls:
            fn = getattr(call, "function", None)
            if fn is None:
                continue
            print(
                json.dumps(
                    {
                        "name": getattr(fn, "name", ""),
                        "arguments": getattr(fn, "arguments", ""),
                    },
                    ensure_ascii=False,
                )
            )

    return 0


if __name__ == "__main__":
    sys.exit(main())
