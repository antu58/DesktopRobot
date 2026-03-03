#!/usr/bin/env python3
"""
Minimal local chat runner for LiquidAI LFM2.5 models.

Usage:
  python lfm25_chat.py
  python lfm25_chat.py --model LiquidAI/LFM2.5-1.2B-Thinking --strip-think
  python lfm25_chat.py --once "Give me a 3-step MVP plan for a desktop robot."
"""

from __future__ import annotations

import argparse
import json
import re
import statistics
import sys
import time
from dataclasses import dataclass
from typing import Dict, List, Optional, Sequence, Tuple

from transformers import AutoModelForCausalLM, AutoTokenizer


TOOL_DEFS: List[Dict[str, object]] = [
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
            "description": "Get current weather for a city or location.",
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

TOOL_NAMES: List[str] = [
    str(tool.get("function", {}).get("name", ""))
    for tool in TOOL_DEFS
    if isinstance(tool.get("function", {}), dict)
]
TOOL_NAME_SET = set(TOOL_NAMES)

TOOL_ROUTER_SYSTEM = (
    "你是一个工具路由器。"
    "当用户请求可以由工具完成时，必须只输出工具调用，不输出解释。"
    "每次只选一个最匹配的工具。"
    f"可用工具仅有: {', '.join(TOOL_NAMES)}。绝不能输出其他工具名。"
    "若参数不完整，可先填你能确定的参数。"
)


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


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description="Run local chat with LFM2.5 model")
    parser.add_argument(
        "--model",
        default="LiquidAI/LFM2-700M",
        help="Hugging Face model id",
    )
    parser.add_argument(
        "--max-new-tokens",
        type=int,
        default=256,
        help="Max generated tokens per turn",
    )
    parser.add_argument(
        "--temperature",
        type=float,
        default=0.0,
        help="Sampling temperature; 0 means greedy",
    )
    parser.add_argument(
        "--top-p",
        type=float,
        default=0.9,
        help="Top-p for sampling",
    )
    parser.add_argument(
        "--system",
        default="You are a concise and helpful Chinese assistant.",
        help="System message",
    )
    parser.add_argument(
        "--once",
        default="",
        help="Run one single user prompt and exit",
    )
    parser.add_argument(
        "--strip-think",
        action="store_true",
        help="Remove <think>...</think> blocks from output",
    )
    parser.add_argument(
        "--enable-tools",
        action="store_true",
        help="Enable tool call mode for interactive chat or --once",
    )
    parser.add_argument(
        "--tool-bench",
        action="store_true",
        help="Run built-in tool routing benchmark and exit",
    )
    parser.add_argument(
        "--tool-bench-rounds",
        type=int,
        default=1,
        help="Repeat each benchmark case for multiple rounds",
    )
    return parser


def remove_think_blocks(text: str) -> str:
    return re.sub(r"<think>.*?</think>", "", text, flags=re.DOTALL).strip()


def extract_tool_calls(text: str) -> List[Dict[str, str]]:
    tool_calls: List[Dict[str, str]] = []
    blocks = re.findall(r"<\|tool_call_start\|>(.*?)<\|tool_call_end\|>", text, flags=re.DOTALL)
    if not blocks:
        blocks = [text]

    for block in blocks:
        # Pattern like: [tool_name(arg="x")]
        for match in re.finditer(r"([a-zA-Z_][a-zA-Z0-9_]*)\s*\((.*?)\)", block, flags=re.DOTALL):
            tool_calls.append({"name": match.group(1), "arguments_raw": match.group(2).strip()})

        # Fallback for truncated outputs, e.g. `[tool_name(arg=...`
        if not tool_calls:
            for match in re.finditer(r"\[([a-zA-Z_][a-zA-Z0-9_]*)\s*\(", block):
                tool_calls.append({"name": match.group(1), "arguments_raw": ""})

        # Fallback for JSON-ish outputs with `"name":"..."`
        if not tool_calls:
            for match in re.finditer(r'"name"\s*:\s*"([a-zA-Z_][a-zA-Z0-9_]*)"', block):
                tool_calls.append({"name": match.group(1), "arguments_raw": ""})

    return tool_calls


def sanitize_tool_calls(calls: List[Dict[str, str]]) -> List[Dict[str, str]]:
    # Enforce a strict whitelist so unknown/hallucinated tools are dropped.
    return [c for c in calls if c.get("name", "") in TOOL_NAME_SET]


def generate_reply(
    tokenizer: AutoTokenizer,
    model: AutoModelForCausalLM,
    messages: List[Dict[str, str]],
    max_new_tokens: int,
    temperature: float,
    top_p: float,
    tools: Optional[Sequence[Dict[str, object]]] = None,
) -> str:
    try:
        prompt = tokenizer.apply_chat_template(
            messages,
            tokenize=False,
            add_generation_prompt=True,
            tools=tools,
        )
    except TypeError:
        prompt = tokenizer.apply_chat_template(messages, tokenize=False, add_generation_prompt=True)

    inputs = tokenizer([prompt], return_tensors="pt").to(model.device)

    kwargs = {
        "max_new_tokens": max_new_tokens,
    }

    if tools:
        eos_ids: List[int] = []
        base_eos = model.generation_config.eos_token_id
        if isinstance(base_eos, int):
            eos_ids.append(base_eos)
        elif isinstance(base_eos, list):
            eos_ids.extend([x for x in base_eos if isinstance(x, int)])
        tool_end_id = tokenizer.convert_tokens_to_ids("<|tool_call_end|>")
        if isinstance(tool_end_id, int) and tool_end_id >= 0:
            eos_ids.append(tool_end_id)
        if eos_ids:
            # End as soon as tool call is complete to cut routing latency.
            kwargs["eos_token_id"] = sorted(set(eos_ids))

    if temperature > 0:
        kwargs.update(
            {
                "do_sample": True,
                "temperature": temperature,
                "top_p": top_p,
            }
        )
    else:
        kwargs.update({"do_sample": False})

    output = model.generate(**inputs, **kwargs)
    generated_ids = output[0][inputs["input_ids"].shape[1] :]
    return tokenizer.decode(generated_ids, skip_special_tokens=True).strip()


def run_tool_bench(
    tokenizer: AutoTokenizer,
    model: AutoModelForCausalLM,
    max_new_tokens: int,
    temperature: float,
    top_p: float,
    rounds: int,
) -> int:
    total = 0
    passed = 0
    latencies: List[float] = []

    print(f"[bench] cases={len(BENCH_CASES)} rounds={rounds}")
    for round_idx in range(rounds):
        print(f"[bench] round={round_idx + 1}")
        for idx, case in enumerate(BENCH_CASES, 1):
            messages = [
                {"role": "system", "content": TOOL_ROUTER_SYSTEM},
                {"role": "user", "content": case.prompt},
            ]
            t0 = time.time()
            raw = generate_reply(
                tokenizer,
                model,
                messages,
                max_new_tokens=max_new_tokens,
                temperature=temperature,
                top_p=top_p,
                tools=TOOL_DEFS,
            )
            latency = time.time() - t0
            calls = sanitize_tool_calls(extract_tool_calls(raw))
            predicted = calls[0]["name"] if calls else "NO_TOOL"
            ok = predicted == case.expected_tool

            total += 1
            passed += 1 if ok else 0
            latencies.append(latency)

            status = "PASS" if ok else "FAIL"
            print(
                f"[{status}] #{idx} expected={case.expected_tool} predicted={predicted} latency={latency:.2f}s"
            )
            if calls:
                print(f"  tool_call={json.dumps(calls[0], ensure_ascii=False)}")
            else:
                preview = raw.replace("\n", " ")[:180]
                print(f"  raw={preview}")

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

    print(f"[load] tokenizer: {args.model}")
    t0 = time.time()
    tokenizer = AutoTokenizer.from_pretrained(args.model, trust_remote_code=True)
    print(f"[ok] tokenizer loaded in {time.time() - t0:.2f}s")

    print(f"[load] model: {args.model}")
    t1 = time.time()
    model = AutoModelForCausalLM.from_pretrained(
        args.model,
        trust_remote_code=True,
        torch_dtype="auto",
        device_map="auto",
    )
    print(f"[ok] model loaded in {time.time() - t1:.2f}s")
    print(f"[device] {model.device}")

    if args.tool_bench:
        return run_tool_bench(
            tokenizer=tokenizer,
            model=model,
            max_new_tokens=args.max_new_tokens,
            temperature=args.temperature,
            top_p=args.top_p,
            rounds=max(1, args.tool_bench_rounds),
        )

    messages: List[Dict[str, str]] = [{"role": "system", "content": args.system}]
    tools = TOOL_DEFS if args.enable_tools else None
    if args.enable_tools:
        messages = [{"role": "system", "content": TOOL_ROUTER_SYSTEM}]

    if args.once:
        messages.append({"role": "user", "content": args.once})
        t = time.time()
        answer = generate_reply(
            tokenizer,
            model,
            messages,
            args.max_new_tokens,
            args.temperature,
            args.top_p,
            tools=tools,
        )
        if args.strip_think:
            answer = remove_think_blocks(answer)
        print(f"[latency] {time.time() - t:.2f}s")
        if args.enable_tools:
            calls = sanitize_tool_calls(extract_tool_calls(answer))
            if calls:
                print(f"[tool] {json.dumps(calls, ensure_ascii=False)}")
        print(answer)
        return 0

    print('Type your message. Enter "exit" to quit.')
    while True:
        try:
            user = input("\nYou> ").strip()
        except (EOFError, KeyboardInterrupt):
            print()
            return 0
        if not user:
            continue
        if user.lower() in {"exit", "quit", "q"}:
            return 0

        messages.append({"role": "user", "content": user})
        t = time.time()
        answer = generate_reply(
            tokenizer,
            model,
            messages,
            args.max_new_tokens,
            args.temperature,
            args.top_p,
            tools=tools,
        )
        if args.strip_think:
            answer = remove_think_blocks(answer)
        if args.enable_tools:
            calls = sanitize_tool_calls(extract_tool_calls(answer))
            if calls:
                print(f"Bot(tool)> {json.dumps(calls, ensure_ascii=False)}")
            else:
                print(f"Bot(tool)> NO_TOOL: {answer}")
        else:
            print(f"Bot> {answer}")
        print(f"[latency] {time.time() - t:.2f}s")
        messages.append({"role": "assistant", "content": answer})


if __name__ == "__main__":
    sys.exit(main())
