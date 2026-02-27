#!/usr/bin/env python3
"""
Edge frontend service:
- Receives browser audio stream over WebSocket
- Runs FunASR (VAD + segment ASR)
- Filters structured events to reduce backend pressure
- Sends structured payload to Go LLM backend over persistent WebSocket
- Returns backend response to browser
"""

from __future__ import annotations

import asyncio
import json
import os
import re
import time
import uuid
from dataclasses import dataclass
from typing import Any, Dict, List, Optional, Tuple

import numpy as np
import websockets
from fastapi import FastAPI, WebSocket, WebSocketDisconnect
from fastapi.responses import JSONResponse
from fastapi.staticfiles import StaticFiles
from funasr import AutoModel

SAMPLE_RATE = 16000

STRICT_MODEL = os.getenv("STRICT_MODEL", "1").strip() == "1"
FUNASR_MODEL = os.getenv("FUNASR_MODEL", "iic/SenseVoiceSmall").strip()
FUNASR_HUB = os.getenv("FUNASR_HUB", "ms").strip()
FUNASR_DEVICE = os.getenv("FUNASR_DEVICE", "cpu").strip()
VAD_MODEL = os.getenv("VAD_MODEL", "fsmn-vad").strip()
VAD_CHUNK_MS = max(50, int(os.getenv("VAD_CHUNK_MS", "200")))
MAX_SEGMENT_MS = max(1000, int(os.getenv("MAX_SEGMENT_MS", "30000")))
PRE_ROLL_MS = max(0, int(os.getenv("PRE_ROLL_MS", "120")))
ASR_LANGUAGE = os.getenv("ASR_LANGUAGE", "auto").strip()
ASR_USE_ITN = os.getenv("ASR_USE_ITN", "1").strip() == "1"
ASR_BATCH_SIZE_S = max(1, int(os.getenv("ASR_BATCH_SIZE_S", "60")))

BACKEND_WS_URL = os.getenv("BACKEND_WS_URL", "ws://127.0.0.1:8090/ws/edge").strip()
BACKEND_REQ_TIMEOUT_S = max(1.0, float(os.getenv("BACKEND_REQ_TIMEOUT_S", "8")))
BACKEND_CONN_TIMEOUT_S = max(1.0, float(os.getenv("BACKEND_CONN_TIMEOUT_S", "8")))
BACKEND_RECONNECT_S = max(0.5, float(os.getenv("BACKEND_RECONNECT_S", "1.5")))

SUBMIT_MIN_TEXT_CHARS = max(1, int(os.getenv("SUBMIT_MIN_TEXT_CHARS", "2")))
SUBMIT_REQUIRE_SPEECH = os.getenv("SUBMIT_REQUIRE_SPEECH", "1").strip() == "1"
SUBMIT_MIN_INTERVAL_MS = max(0, int(os.getenv("SUBMIT_MIN_INTERVAL_MS", "600")))

VAD_CHUNK_SAMPLES = max(1, int(SAMPLE_RATE * VAD_CHUNK_MS / 1000))
MAX_SEGMENT_SAMPLES = max(1, int(SAMPLE_RATE * MAX_SEGMENT_MS / 1000))
PRE_ROLL_SAMPLES = max(0, int(SAMPLE_RATE * PRE_ROLL_MS / 1000))

TAG_PATTERN = re.compile(r"<\|([^|]+)\|>")
STRIP_TAG_PATTERN = re.compile(r"<\|[^|]+\|>")

WEB_DIR = os.path.join(os.path.dirname(__file__), "web")

asr_model = None
vad_model = None
model_init_error = ""


def create_model(model_name: str) -> AutoModel:
    try:
        return AutoModel(model=model_name, hub=FUNASR_HUB, device=FUNASR_DEVICE)
    except TypeError:
        return AutoModel(model=model_name, model_hub=FUNASR_HUB, device=FUNASR_DEVICE)


try:
    asr_model = create_model(FUNASR_MODEL)
    vad_model = create_model(VAD_MODEL)
except Exception as exc:  # pragma: no cover
    model_init_error = str(exc)
    if STRICT_MODEL:
        raise RuntimeError(f"failed to load FunASR models: {model_init_error}") from exc


def extract_text(result: Any) -> str:
    if result is None:
        return ""
    if isinstance(result, list) and result:
        result = result[0]
    if isinstance(result, dict):
        text = result.get("text", "")
        if isinstance(text, str):
            return text.strip()
        return str(text).strip()
    return str(result).strip()


def extract_vad_events(result: Any) -> List[Tuple[int, int]]:
    if result is None:
        return []
    if isinstance(result, list) and result:
        result = result[0]
    if not isinstance(result, dict):
        return []
    value = result.get("value", [])
    events: List[Tuple[int, int]] = []
    if not isinstance(value, list):
        return events
    for item in value:
        if (
            isinstance(item, (list, tuple))
            and len(item) == 2
            and isinstance(item[0], (int, float))
            and isinstance(item[1], (int, float))
        ):
            events.append((int(item[0]), int(item[1])))
    return events


def append_tail(buffer: np.ndarray, chunk: np.ndarray, max_samples: int) -> np.ndarray:
    if max_samples <= 0:
        return np.zeros((0,), dtype=np.float32)
    merged = np.concatenate((buffer, chunk))
    if merged.size <= max_samples:
        return merged
    return merged[-max_samples:]


def safe_generate(model: AutoModel, base_kwargs: Dict[str, Any], optional_keys: List[str]) -> Any:
    kwargs = dict(base_kwargs)
    for idx in range(len(optional_keys) + 1):
        try:
            return model.generate(**kwargs)
        except TypeError:
            if idx >= len(optional_keys):
                raise
            kwargs.pop(optional_keys[idx], None)


@dataclass
class ParsedText:
    raw_text: str
    clean_text: str
    language: str
    emotion: str
    event: str
    itn: str


def parse_funasr_text(text: str) -> ParsedText:
    tags = TAG_PATTERN.findall(text)
    clean = STRIP_TAG_PATTERN.sub("", text).strip()
    language = tags[0] if len(tags) > 0 else "unknown"
    emotion = tags[1] if len(tags) > 1 else "EMO_UNKNOWN"
    event = tags[2] if len(tags) > 2 else "Event_UNK"
    itn = tags[3] if len(tags) > 3 else "unknown"
    return ParsedText(
        raw_text=text,
        clean_text=clean,
        language=language,
        emotion=emotion,
        event=event,
        itn=itn,
    )


class BackendBridge:
    def __init__(self, url: str) -> None:
        self.url = url
        self._ws = None
        self._connected = asyncio.Event()
        self._send_lock = asyncio.Lock()
        self._pending: Dict[str, asyncio.Future] = {}
        self._runner: Optional[asyncio.Task] = None
        self._stop = False

    def start(self) -> None:
        if self._runner is None:
            self._runner = asyncio.create_task(self._run(), name="backend-bridge-runner")

    async def stop(self) -> None:
        self._stop = True
        self._connected.clear()
        if self._ws is not None:
            try:
                await self._ws.close()
            except Exception:
                pass
        if self._runner is not None:
            self._runner.cancel()
            try:
                await self._runner
            except Exception:
                pass
        for fut in list(self._pending.values()):
            if not fut.done():
                fut.set_exception(RuntimeError("backend bridge stopped"))
        self._pending.clear()

    @property
    def connected(self) -> bool:
        return self._connected.is_set()

    async def request(self, payload: Dict[str, Any], timeout_s: float) -> Dict[str, Any]:
        request_id = payload.get("request_id") or f"req-{uuid.uuid4().hex[:12]}"
        payload["request_id"] = request_id
        loop = asyncio.get_running_loop()
        fut: asyncio.Future = loop.create_future()
        self._pending[request_id] = fut
        try:
            await asyncio.wait_for(self._connected.wait(), timeout=BACKEND_CONN_TIMEOUT_S)
            if self._ws is None:
                raise RuntimeError("backend websocket not ready")
            async with self._send_lock:
                await self._ws.send(json.dumps(payload, ensure_ascii=False))
            resp = await asyncio.wait_for(fut, timeout=timeout_s)
            if not isinstance(resp, dict):
                raise RuntimeError("invalid backend response")
            return resp
        finally:
            self._pending.pop(request_id, None)

    async def _run(self) -> None:
        while not self._stop:
            try:
                async with websockets.connect(
                    self.url,
                    ping_interval=20,
                    ping_timeout=20,
                    max_size=1 << 20,
                ) as ws:
                    self._ws = ws
                    self._connected.set()
                    while not self._stop:
                        raw = await ws.recv()
                        if isinstance(raw, bytes):
                            continue
                        try:
                            msg = json.loads(raw)
                        except Exception:
                            continue
                        request_id = msg.get("request_id", "")
                        if request_id:
                            fut = self._pending.get(request_id)
                            if fut is not None and not fut.done():
                                fut.set_result(msg)
            except Exception:
                await asyncio.sleep(BACKEND_RECONNECT_S)
            finally:
                self._connected.clear()
                self._ws = None
                for fut in list(self._pending.values()):
                    if not fut.done():
                        fut.set_exception(RuntimeError("backend bridge disconnected"))


backend_bridge = BackendBridge(BACKEND_WS_URL)
app = FastAPI(title="Edge Frontend (ASR + Filter + Backend Bridge)")


@app.on_event("startup")
async def on_startup() -> None:
    backend_bridge.start()


@app.on_event("shutdown")
async def on_shutdown() -> None:
    await backend_bridge.stop()


@app.get("/healthz")
async def healthz():
    return JSONResponse(
        {
            "status": "ok",
            "model_ready": asr_model is not None and vad_model is not None,
            "asr_model": FUNASR_MODEL,
            "vad_model": VAD_MODEL,
            "backend_ws_url": BACKEND_WS_URL,
            "backend_connected": backend_bridge.connected,
            "submit_min_text_chars": SUBMIT_MIN_TEXT_CHARS,
            "submit_require_speech": SUBMIT_REQUIRE_SPEECH,
            "submit_min_interval_ms": SUBMIT_MIN_INTERVAL_MS,
            "model_error": model_init_error,
        }
    )


@app.websocket("/ws/client")
async def ws_client(websocket: WebSocket):
    await websocket.accept()
    if asr_model is None or vad_model is None:
        await websocket.send_json({"event": "warn", "message": f"model not ready: {model_init_error}"})
        await websocket.close()
        return

    session_id = f"s-{uuid.uuid4().hex[:12]}"
    await websocket.send_json(
        {
            "event": "status",
            "session_id": session_id,
            "message": "connected",
            "backend_connected": backend_bridge.connected,
        }
    )

    pending = np.zeros((0,), dtype=np.float32)
    history = np.zeros((0,), dtype=np.float32)
    segment = np.zeros((0,), dtype=np.float32)
    vad_cache: Dict[str, Any] = {}
    in_segment = False
    last_submit_ms = 0

    async def emit_asr(parsed: ParsedText, final: bool) -> None:
        await websocket.send_json(
            {
                "event": "asr",
                "session_id": session_id,
                "text": parsed.clean_text,
                "raw_text": parsed.raw_text,
                "language": parsed.language,
                "emotion": parsed.emotion,
                "audio_event": parsed.event,
                "itn": parsed.itn,
                "final": final,
            }
        )

    def should_submit(parsed: ParsedText, now_ms: int, prev_submit_ms: int) -> Tuple[bool, str]:
        if len(parsed.clean_text) < SUBMIT_MIN_TEXT_CHARS:
            return False, "text_too_short"
        if SUBMIT_REQUIRE_SPEECH and parsed.event != "Speech":
            return False, "not_speech_event"
        if now_ms - prev_submit_ms < SUBMIT_MIN_INTERVAL_MS:
            return False, "submit_interval_limited"
        return True, ""

    async def run_backend(parsed: ParsedText, now_ms: int) -> None:
        payload = {
            "type": "llm_request",
            "session_id": session_id,
            "text": parsed.clean_text,
            "emotion": parsed.emotion,
            "event": parsed.event,
            "final": True,
            "ts_ms": now_ms,
        }
        try:
            resp = await backend_bridge.request(payload, timeout_s=BACKEND_REQ_TIMEOUT_S)
            await websocket.send_json(
                {
                    "event": "backend_result",
                    "session_id": session_id,
                    "request_id": resp.get("request_id", ""),
                    "reply": resp.get("reply", ""),
                    "emotion": resp.get("emotion", ""),
                    "audio_event": resp.get("event", ""),
                    "final": resp.get("final", True),
                }
            )
        except Exception as exc:
            await websocket.send_json({"event": "warn", "message": f"backend request failed: {exc}"})

    async def finalize_segment(audio_chunk: np.ndarray) -> bool:
        nonlocal last_submit_ms
        if audio_chunk.size == 0:
            return False
        base_kwargs: Dict[str, Any] = {
            "input": audio_chunk,
            "language": ASR_LANGUAGE,
            "use_itn": ASR_USE_ITN,
            "batch_size_s": ASR_BATCH_SIZE_S,
        }
        result = safe_generate(asr_model, base_kwargs, ["language", "use_itn", "batch_size_s"])
        text = extract_text(result)
        if not text:
            return False
        parsed = parse_funasr_text(text)
        await emit_asr(parsed, final=True)
        now_ms = int(time.time() * 1000)
        do_submit, reason = should_submit(parsed, now_ms, last_submit_ms)
        if not do_submit:
            await websocket.send_json(
                {
                    "event": "filtered",
                    "session_id": session_id,
                    "reason": reason,
                    "text": parsed.clean_text,
                }
            )
            return True
        last_submit_ms = now_ms
        await run_backend(parsed, now_ms)
        return True

    async def process_vad_chunk(chunk: np.ndarray, is_final: bool) -> None:
        nonlocal history, segment, in_segment
        prior_history = history
        history = append_tail(history, chunk, PRE_ROLL_SAMPLES)

        vad_result = vad_model.generate(
            input=chunk,
            cache=vad_cache,
            is_final=is_final,
            chunk_size=VAD_CHUNK_MS,
        )
        events = extract_vad_events(vad_result)
        has_begin = any(beg >= 0 for beg, _ in events)
        has_end = any(end >= 0 for _, end in events)
        was_in_segment = in_segment

        if was_in_segment:
            segment = np.concatenate((segment, chunk))
        if has_begin and not was_in_segment:
            prefix = prior_history[-PRE_ROLL_SAMPLES:] if PRE_ROLL_SAMPLES > 0 else np.zeros((0,), dtype=np.float32)
            segment = np.concatenate((prefix, chunk))
            in_segment = True

        if in_segment and segment.size >= MAX_SEGMENT_SAMPLES:
            await finalize_segment(segment)
            segment = np.zeros((0,), dtype=np.float32)
            in_segment = False
        if has_end and in_segment:
            await finalize_segment(segment)
            segment = np.zeros((0,), dtype=np.float32)
            in_segment = False

    async def flush_all() -> None:
        nonlocal pending, history, segment, in_segment
        while pending.size >= VAD_CHUNK_SAMPLES:
            chunk = pending[:VAD_CHUNK_SAMPLES]
            pending = pending[VAD_CHUNK_SAMPLES:]
            await process_vad_chunk(chunk, is_final=False)
        if pending.size > 0:
            await process_vad_chunk(pending, is_final=True)
            pending = np.zeros((0,), dtype=np.float32)
        if in_segment and segment.size > 0:
            await finalize_segment(segment)
            segment = np.zeros((0,), dtype=np.float32)
            in_segment = False
        history = np.zeros((0,), dtype=np.float32)
        vad_cache.clear()

    try:
        while True:
            msg = await websocket.receive()
            if "bytes" in msg and msg["bytes"] is not None:
                audio_bytes = msg["bytes"]
                if len(audio_bytes) == 0:
                    continue
                pcm16 = np.frombuffer(audio_bytes, dtype=np.int16)
                if pcm16.size == 0:
                    continue
                float_pcm = pcm16.astype(np.float32) / 32768.0
                pending = np.concatenate((pending, float_pcm))
                while pending.size >= VAD_CHUNK_SAMPLES:
                    chunk = pending[:VAD_CHUNK_SAMPLES]
                    pending = pending[VAD_CHUNK_SAMPLES:]
                    await process_vad_chunk(chunk, is_final=False)
                continue

            if "text" in msg and msg["text"]:
                event = ""
                try:
                    event = json.loads(msg["text"]).get("event", "")
                except Exception:
                    event = ""
                if event == "flush":
                    await flush_all()
                    await websocket.send_json({"event": "status", "session_id": session_id, "message": "flushed"})
                elif event == "ping":
                    await websocket.send_json({"event": "pong", "session_id": session_id})
                continue
    except WebSocketDisconnect:
        return
    except Exception as exc:
        await websocket.send_json({"event": "warn", "session_id": session_id, "message": str(exc)})


app.mount("/", StaticFiles(directory=WEB_DIR, html=True), name="web")
