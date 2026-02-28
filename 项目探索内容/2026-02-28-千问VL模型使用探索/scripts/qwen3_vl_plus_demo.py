#!/usr/bin/env python3
"""qwen3-vl-plus OpenAI-compatible demo.

Usage examples:
  python3 scripts/qwen3_vl_plus_demo.py --image-url "https://.../cat.jpg" --prompt "这张图里有什么？"
  python3 scripts/qwen3_vl_plus_demo.py --image-path ./test.png --prompt "请描述图片并给出3个要点"
"""

from __future__ import annotations

import argparse
import base64
import json
import mimetypes
import os
import sys
import urllib.error
import urllib.request
from typing import Any


DEFAULT_BASE_URL = "https://api.newcoin.top/v1"
DEFAULT_MODEL = "qwen3-vl-plus"


def _image_path_to_data_url(path: str) -> str:
    with open(path, "rb") as f:
        data = f.read()
    mime_type, _ = mimetypes.guess_type(path)
    if not mime_type:
        mime_type = "application/octet-stream"
    encoded = base64.b64encode(data).decode("ascii")
    return f"data:{mime_type};base64,{encoded}"


def _extract_text_from_content(content: Any) -> str:
    if isinstance(content, str):
        return content
    if isinstance(content, list):
        parts = []
        for item in content:
            if isinstance(item, dict):
                text = item.get("text")
                if isinstance(text, str) and text.strip():
                    parts.append(text)
        return "\n".join(parts)
    if isinstance(content, dict):
        text = content.get("text")
        if isinstance(text, str):
            return text
    return ""


def main() -> int:
    parser = argparse.ArgumentParser(description="Call qwen3-vl-plus via OpenAI-compatible /chat/completions")
    parser.add_argument("--image-url", help="Remote image URL")
    parser.add_argument("--image-path", help="Local image path")
    parser.add_argument("--prompt", default="请描述这张图并给出关键要点。", help="Question prompt")
    parser.add_argument("--base-url", default=os.getenv("OPENAI_BASE_URL", DEFAULT_BASE_URL), help="OpenAI-compatible base URL")
    parser.add_argument("--api-key", default=os.getenv("OPENAI_API_KEY", ""), help="API key")
    parser.add_argument("--model", default=os.getenv("LLM_MODEL", DEFAULT_MODEL), help="Model name")
    args = parser.parse_args()

    if not args.api_key:
        print("ERROR: missing API key. Set OPENAI_API_KEY or pass --api-key", file=sys.stderr)
        return 1

    has_image_url = bool(args.image_url)
    has_image_path = bool(args.image_path)
    if has_image_url == has_image_path:
        print("ERROR: provide exactly one of --image-url or --image-path", file=sys.stderr)
        return 1

    image_url = args.image_url
    if args.image_path:
        if not os.path.exists(args.image_path):
            print(f"ERROR: image path not found: {args.image_path}", file=sys.stderr)
            return 1
        image_url = _image_path_to_data_url(args.image_path)

    payload = {
        "model": args.model,
        "stream": False,
        "messages": [
            {
                "role": "user",
                "content": [
                    {"type": "text", "text": args.prompt},
                    {"type": "image_url", "image_url": {"url": image_url}},
                ],
            }
        ],
    }

    endpoint = args.base_url.rstrip("/") + "/chat/completions"
    request = urllib.request.Request(
        endpoint,
        data=json.dumps(payload).encode("utf-8"),
        headers={
            "Authorization": f"Bearer {args.api_key}",
            "Content-Type": "application/json",
        },
        method="POST",
    )

    try:
        with urllib.request.urlopen(request, timeout=120) as resp:
            raw = resp.read().decode("utf-8", errors="replace")
    except urllib.error.HTTPError as e:
        body = e.read().decode("utf-8", errors="replace")
        print(f"HTTPError {e.code}: {body}", file=sys.stderr)
        return 2
    except Exception as e:  # pragma: no cover
        print(f"Request failed: {e}", file=sys.stderr)
        return 3

    try:
        obj = json.loads(raw)
    except json.JSONDecodeError:
        print("Non-JSON response:")
        print(raw)
        return 4

    if isinstance(obj, dict) and obj.get("error"):
        print(f"API error: {obj['error']}", file=sys.stderr)
        return 5

    text = ""
    choices = obj.get("choices") if isinstance(obj, dict) else None
    if isinstance(choices, list) and choices:
        first = choices[0]
        if isinstance(first, dict):
            message = first.get("message")
            if isinstance(message, dict):
                text = _extract_text_from_content(message.get("content"))
            if not text:
                text = _extract_text_from_content(first.get("text"))

    if not text:
        print("Response JSON:")
        print(json.dumps(obj, ensure_ascii=False, indent=2))
        return 6

    print(text)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
