#!/usr/bin/env python3
import argparse
import json
import os
import sys
import urllib.error
import urllib.request


def request_json(method: str, url: str, api_key: str, payload: dict | None = None, timeout: int = 30):
    body = None
    headers = {
        "Authorization": f"Bearer {api_key}",
        "Content-Type": "application/json",
    }
    if payload is not None:
        body = json.dumps(payload).encode("utf-8")
    req = urllib.request.Request(url=url, data=body, method=method, headers=headers)
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            raw = resp.read().decode("utf-8", errors="replace")
            return resp.status, raw
    except urllib.error.HTTPError as e:
        raw = e.read().decode("utf-8", errors="replace")
        return e.code, raw


def main() -> int:
    parser = argparse.ArgumentParser(description="Test OpenAI-compatible model reachability")
    parser.add_argument("--base-url", default="https://api.newcoin.tech/v1")
    parser.add_argument("--model", default="doubao-seed-1-6-251015")
    parser.add_argument("--api-key", default=os.getenv("OPENAI_API_KEY", ""))
    parser.add_argument("--timeout", type=int, default=30)
    args = parser.parse_args()

    if not args.api_key:
        print("ERROR: api key is required (use --api-key or OPENAI_API_KEY)")
        return 2

    base = args.base_url.rstrip("/")
    print(f"[1/2] GET {base}/models")
    code, body = request_json("GET", f"{base}/models", args.api_key, timeout=args.timeout)
    print(f"status={code}")
    if code >= 300:
        print(body)
        return 1
    try:
        models = json.loads(body).get("data", [])
        ids = [m.get("id", "") for m in models][:10]
        print(f"models(sample)={ids}")
    except Exception:
        print("WARN: /models response is not standard JSON list")

    print(f"[2/2] POST {base}/chat/completions model={args.model}")
    code, body = request_json(
        "POST",
        f"{base}/chat/completions",
        args.api_key,
        payload={
            "model": args.model,
            "messages": [{"role": "user", "content": "请回复 OK"}],
            "temperature": 0,
        },
        timeout=args.timeout,
    )
    print(f"status={code}")
    print(body)
    return 0 if code < 300 else 1


if __name__ == "__main__":
    sys.exit(main())
