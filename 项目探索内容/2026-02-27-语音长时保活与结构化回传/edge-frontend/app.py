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
from contextlib import suppress
import json
import logging
import os
import re
import time
import unicodedata
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
BACKEND_REQ_TIMEOUT_S = max(1.0, float(os.getenv("BACKEND_REQ_TIMEOUT_S", "30")))
BACKEND_CONN_TIMEOUT_S = max(1.0, float(os.getenv("BACKEND_CONN_TIMEOUT_S", "8")))
BACKEND_RECONNECT_S = max(0.5, float(os.getenv("BACKEND_RECONNECT_S", "1.5")))
BACKEND_MAX_PENDING = max(1, int(os.getenv("BACKEND_MAX_PENDING", "8")))
BACKEND_WS_PING_INTERVAL_S = max(5.0, float(os.getenv("BACKEND_WS_PING_INTERVAL_S", "20")))
_backend_ws_ping_timeout_raw = float(os.getenv("BACKEND_WS_PING_TIMEOUT_S", "0"))
BACKEND_WS_PING_TIMEOUT_S: Optional[float]
if _backend_ws_ping_timeout_raw <= 0:
    BACKEND_WS_PING_TIMEOUT_S = None
else:
    BACKEND_WS_PING_TIMEOUT_S = max(5.0, _backend_ws_ping_timeout_raw)

SUBMIT_MIN_TEXT_CHARS = max(1, int(os.getenv("SUBMIT_MIN_TEXT_CHARS", "2")))
SUBMIT_REQUIRE_SPEECH = os.getenv("SUBMIT_REQUIRE_SPEECH", "1").strip() == "1"
SUBMIT_MIN_INTERVAL_MS = max(0, int(os.getenv("SUBMIT_MIN_INTERVAL_MS", "600")))
FILTER_FILLER = os.getenv("FILTER_FILLER", "1").strip() == "1"
FILLER_MAX_CHARS = max(1, int(os.getenv("FILLER_MAX_CHARS", "8")))
FINAL_MERGE_GAP_MS = max(100, int(os.getenv("FINAL_MERGE_GAP_MS", "500")))
FINAL_MERGE_MAX_MS = max(FINAL_MERGE_GAP_MS, int(os.getenv("FINAL_MERGE_MAX_MS", "2200")))
INTERRUPT_PRE_TOKEN = os.getenv("INTERRUPT_PRE_TOKEN", "1").strip() == "1"
INTERRUPT_POST_TOKEN_MODE = os.getenv("INTERRUPT_POST_TOKEN_MODE", "conditional").strip().lower()
INTERRUPT_MIN_CHARS = max(1, int(os.getenv("INTERRUPT_MIN_CHARS", "6")))

VAD_CHUNK_SAMPLES = max(1, int(SAMPLE_RATE * VAD_CHUNK_MS / 1000))
MAX_SEGMENT_SAMPLES = max(1, int(SAMPLE_RATE * MAX_SEGMENT_MS / 1000))
PRE_ROLL_SAMPLES = max(0, int(SAMPLE_RATE * PRE_ROLL_MS / 1000))

TAG_PATTERN = re.compile(r"<\|([^|]+)\|>")
STRIP_TAG_PATTERN = re.compile(r"<\|[^|]+\|>")
PUNCT_SPACE_RE = re.compile(r"[\s\.,!?;:，。！？；：、~…·]+")
EN_WORD_RE = re.compile(r"[a-zA-Z']+")

COMMON_FILLERS = {
    "嗯", "嗯嗯", "嗯嗯嗯",
    "啊", "啊啊", "啊啊啊",
    "呃", "额", "哦", "噢",
    "uh", "um", "hmm", "ah", "oh",
    "yeah", "yep", "mhm", "erm", "huh",
    "yeahyeah", "yepyep",
}
ZH_FILLER_CHARS = set("嗯啊呃额哦噢诶欸哎")
EN_FILLER_WORDS = {"uh", "um", "hmm", "ah", "oh", "yeah", "yep", "mhm", "erm", "huh"}
EN_LOW_SEMANTIC_WORDS = {
    "the", "a", "an",
    "to", "of", "in", "on", "at", "for", "from", "by", "with", "as",
    "and", "or", "but", "if", "so", "than",
    "is", "am", "are", "was", "were", "be", "been", "being",
}
LOW_SEMANTIC_SINGLE_TOKENS = {
    # zh / yue virtual words
    "的", "了", "呢", "吗", "嘛", "吧", "呀", "啊", "哦", "哇", "哈", "欸",
    # en
    "the", "a", "an", "to", "of", "in", "on", "at", "for", "and", "or", "but",
    # ja particles / low semantic singles
    "は", "が", "を", "に", "で", "へ", "の", "と", "も", "や", "ね", "よ", "か", "な", "さ",
    # ko particles / endings
    "은", "는", "이", "가", "을", "를", "에", "에서", "와", "과", "도", "요", "네",
}

KEEP_SHORT_TOKENS = {
    # zh / yue
    "好的", "可以", "可以的", "行", "行的", "明白", "收到", "继续", "停止", "取消", "不对", "对", "是的",
    "得", "得啦", "好呀", "可以呀",
    # en
    "ok", "okay", "sure", "yes", "no", "continue", "stop", "cancel", "wait", "gotit", "gotcha", "roger",
    # ja
    "はい", "了解", "わかった", "オッケー", "いいよ", "続けて", "中止", "キャンセル",
    # ko
    "네", "예", "알겠어", "알겠습니다", "좋아요", "계속", "중지", "취소", "오케이",
}
DROP_FILLER_TOKENS = set(COMMON_FILLERS) | {"응", "...", "。。", ".."}

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

def normalize_text_token(text: str) -> str:
    raw = unicodedata.normalize("NFKC", text.strip().lower())
    compact = PUNCT_SPACE_RE.sub("", raw)
    if compact == "":
        return ""
    folded: List[str] = []
    prev = ""
    repeat = 0
    for ch in compact:
        if ch == prev:
            repeat += 1
        else:
            prev = ch
            repeat = 1
        if repeat <= 2:
            folded.append(ch)
    return "".join(folded)


def classify_utterance(text: str) -> str:
    token = normalize_text_token(text)
    if token == "":
        return "drop_filler"
    if token in KEEP_SHORT_TOKENS:
        return "keep_short"
    if token in DROP_FILLER_TOKENS:
        return "drop_filler"
    if token in LOW_SEMANTIC_SINGLE_TOKENS:
        return "drop_filler"
    if len(token) <= FILLER_MAX_CHARS and all(ch in ZH_FILLER_CHARS for ch in token):
        return "drop_filler"
    normalized = unicodedata.normalize("NFKC", text.strip().lower())
    words = EN_WORD_RE.findall(normalized)
    if words and len(words) <= 2 and all(w in EN_LOW_SEMANTIC_WORDS for w in words):
        return "drop_filler"
    if words and len("".join(words)) <= FILLER_MAX_CHARS * 2:
        if all(w in EN_FILLER_WORDS for w in words):
            return "drop_filler"
    return "normal"


class BackendBridge:
    def __init__(self, url: str) -> None:
        self.url = url
        self._ws = None
        self._connected = asyncio.Event()
        self._send_lock = asyncio.Lock()
        self._pending_streams: Dict[str, asyncio.Queue] = {}
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
        for queue in list(self._pending_streams.values()):
            await queue.put(
                {
                    "type": "llm_error",
                    "error": "backend bridge stopped",
                    "final": True,
                    "ts_ms": int(time.time() * 1000),
                }
            )
        self._pending_streams.clear()

    @property
    def connected(self) -> bool:
        return self._connected.is_set()

    async def request_stream(self, payload: Dict[str, Any], timeout_s: float):
        request_id = payload.get("request_id") or f"req-{uuid.uuid4().hex[:12]}"
        payload["request_id"] = request_id
        queue: asyncio.Queue = asyncio.Queue()
        self._pending_streams[request_id] = queue
        try:
            await asyncio.wait_for(self._connected.wait(), timeout=BACKEND_CONN_TIMEOUT_S)
            if self._ws is None:
                raise RuntimeError("backend websocket not ready")
            async with self._send_lock:
                await self._ws.send(json.dumps(payload, ensure_ascii=False))

            while True:
                msg = await asyncio.wait_for(queue.get(), timeout=timeout_s)
                if not isinstance(msg, dict):
                    raise RuntimeError("invalid backend response")
                yield msg
                if msg.get("final", False):
                    break
        finally:
            self._pending_streams.pop(request_id, None)

    async def _run(self) -> None:
        while not self._stop:
            try:
                async with websockets.connect(
                    self.url,
                    ping_interval=BACKEND_WS_PING_INTERVAL_S,
                    ping_timeout=BACKEND_WS_PING_TIMEOUT_S,
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
                            queue = self._pending_streams.get(request_id)
                            if queue is not None:
                                await queue.put(msg)
            except Exception:
                await asyncio.sleep(BACKEND_RECONNECT_S)
            finally:
                self._connected.clear()
                self._ws = None
                for request_id, queue in list(self._pending_streams.items()):
                    await queue.put(
                        {
                            "type": "llm_error",
                            "request_id": request_id,
                            "error": "backend bridge disconnected",
                            "final": True,
                            "ts_ms": int(time.time() * 1000),
                        }
                    )


backend_bridge = BackendBridge(BACKEND_WS_URL)
app = FastAPI(title="Edge Frontend (ASR + Filter + Backend Bridge)")
logger = logging.getLogger("edge-frontend")


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
            "filter_filler": FILTER_FILLER,
            "filler_max_chars": FILLER_MAX_CHARS,
            "final_merge_gap_ms": FINAL_MERGE_GAP_MS,
            "final_merge_max_ms": FINAL_MERGE_MAX_MS,
            "interrupt_pre_token": INTERRUPT_PRE_TOKEN,
            "interrupt_post_token_mode": INTERRUPT_POST_TOKEN_MODE,
            "interrupt_min_chars": INTERRUPT_MIN_CHARS,
            "backend_max_pending": BACKEND_MAX_PENDING,
            "backend_ws_ping_interval_s": BACKEND_WS_PING_INTERVAL_S,
            "backend_ws_ping_timeout_s": BACKEND_WS_PING_TIMEOUT_S,
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

    send_lock = asyncio.Lock()
    ws_closed = False

    async def send_event(payload: Dict[str, Any]) -> None:
        nonlocal ws_closed
        if ws_closed:
            return
        async with send_lock:
            await websocket.send_json(payload)

    async def emit_backend_state(stage: str, request_id: str = "", detail: str = "") -> None:
        state_payload: Dict[str, Any] = {
            "event": "backend_state",
            "session_id": session_id,
            "stage": stage,
            "request_id": request_id,
            "queue_size": backend_queue.qsize(),
            "ts_ms": int(time.time() * 1000),
        }
        if detail:
            state_payload["detail"] = detail
        await send_event(state_payload)

    session_id = f"s-{uuid.uuid4().hex[:12]}"
    await send_event(
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

    backend_queue: asyncio.Queue[Dict[str, Any]] = asyncio.Queue(maxsize=BACKEND_MAX_PENDING)
    backend_dispatcher_task: Optional[asyncio.Task] = None

    active_backend_task: Optional[asyncio.Task] = None
    active_request_id = ""
    active_request_text = ""
    active_first_token_seen = False

    merge_texts: List[str] = []
    merge_emotion = "EMO_UNKNOWN"
    merge_event = "Speech"
    merge_started_ms = 0
    merge_last_ms = 0
    merge_version = 0
    merge_timer_task: Optional[asyncio.Task] = None
    request_seq = 0

    async def emit_asr(parsed: ParsedText, final: bool) -> None:
        await send_event(
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

    def should_submit(parsed: ParsedText, now_ms: int, prev_submit_ms: int) -> Tuple[bool, str, str]:
        text_class = classify_utterance(parsed.clean_text)
        if FILTER_FILLER and text_class == "drop_filler":
            return False, "filler_text", text_class
        if text_class != "keep_short" and len(parsed.clean_text) < SUBMIT_MIN_TEXT_CHARS:
            return False, "text_too_short", text_class
        if SUBMIT_REQUIRE_SPEECH and parsed.event != "Speech":
            return False, "not_speech_event", text_class
        if now_ms - prev_submit_ms < SUBMIT_MIN_INTERVAL_MS:
            return False, "submit_interval_limited", text_class
        return True, "", text_class

    def should_interrupt_post_token(new_text: str, text_class: str) -> bool:
        if text_class in {"drop_filler", "keep_short"}:
            return False
        mode = INTERRUPT_POST_TOKEN_MODE
        if mode == "always":
            return True
        if mode in {"off", "none", "never", "0"}:
            return False
        t = new_text.strip()
        if len(t) >= INTERRUPT_MIN_CHARS:
            return True
        return any(mark in t for mark in ("?", "？", "吗", "呢"))

    def cancel_merge_timer() -> None:
        nonlocal merge_timer_task
        if merge_timer_task is not None and not merge_timer_task.done():
            merge_timer_task.cancel()
        merge_timer_task = None

    async def commit_merged(reason: str) -> bool:
        nonlocal merge_texts, merge_started_ms, merge_last_ms, merge_version, request_seq
        nonlocal merge_emotion, merge_event
        if not merge_texts:
            return False
        text = " ".join(s.strip() for s in merge_texts if s and s.strip()).strip()
        if text == "":
            merge_texts = []
            merge_started_ms = 0
            merge_last_ms = 0
            cancel_merge_timer()
            return False

        emotion = merge_emotion
        event = merge_event
        merge_count = len(merge_texts)

        merge_texts = []
        merge_started_ms = 0
        merge_last_ms = 0
        merge_version += 1
        cancel_merge_timer()

        request_seq += 1
        req_payload: Dict[str, Any] = {
            "type": "llm_request",
            "request_id": f"{session_id}-r{request_seq}",
            "session_id": session_id,
            "text": text,
            "emotion": emotion,
            "event": event,
            "final": True,
            "ts_ms": int(time.time() * 1000),
            "_merge_reason": reason,
            "_merge_count": merge_count,
        }

        if backend_queue.full():
            # Keep buffered content and retry later instead of dropping user speech.
            merge_texts = [text]
            merge_emotion = emotion
            merge_event = event
            merge_started_ms = int(time.time() * 1000)
            merge_last_ms = merge_started_ms
            await send_event(
                {
                    "event": "filtered",
                    "session_id": session_id,
                    "reason": "backend_queue_busy_buffering",
                    "text": text,
                }
            )
            await emit_backend_state("queue_busy", req_payload["request_id"], "backend_queue_busy_buffering")
            schedule_merge_timer()
            return False

        backend_queue.put_nowait(req_payload)
        await emit_backend_state(
            "queued",
            req_payload["request_id"],
            f"merge_reason={reason} merge_count={merge_count}",
        )
        return True

    def schedule_merge_timer() -> None:
        nonlocal merge_version, merge_timer_task
        if not merge_texts:
            cancel_merge_timer()
            return
        cancel_merge_timer()
        merge_version += 1
        current_version = merge_version
        now_ms = int(time.time() * 1000)
        due_ms = min(merge_last_ms + FINAL_MERGE_GAP_MS, merge_started_ms + FINAL_MERGE_MAX_MS)
        wait_s = max(0.0, (due_ms - now_ms) / 1000.0)

        async def _timer() -> None:
            try:
                await asyncio.sleep(wait_s)
            except asyncio.CancelledError:
                return
            if current_version != merge_version:
                return
            await commit_merged("gap_or_window")

        merge_timer_task = asyncio.create_task(_timer(), name=f"merge-timer-{session_id}")

    async def interrupt_active(reason: str, merge_back_current_request: bool) -> None:
        nonlocal active_backend_task, active_request_text
        if active_backend_task is None or active_backend_task.done():
            return
        await emit_backend_state("interrupting", active_request_id, reason)
        if merge_back_current_request and active_request_text.strip():
            if not merge_texts:
                # start a new aggregation window immediately
                now_ms = int(time.time() * 1000)
                nonlocal merge_started_ms, merge_last_ms
                merge_started_ms = now_ms
                merge_last_ms = now_ms
            merge_texts.insert(0, active_request_text.strip())
        active_backend_task.cancel()
        await send_event(
            {
                "event": "warn",
                "session_id": session_id,
                "message": f"llm interrupted: {reason}",
                "request_id": active_request_id,
            }
        )

    async def run_backend_payload(payload: Dict[str, Any]) -> None:
        nonlocal active_first_token_seen
        req_id = str(payload.get("request_id", "") or "")
        reply_parts: List[str] = []
        streaming_announced = False
        await emit_backend_state("thinking", req_id)
        try:
            async for resp in backend_bridge.request_stream(payload, timeout_s=BACKEND_REQ_TIMEOUT_S):
                resp_type = str(resp.get("type", "") or "")
                request_id = str(resp.get("request_id", "") or req_id)
                final = bool(resp.get("final", False))

                if resp_type == "llm_error":
                    await send_event(
                        {
                            "event": "warn",
                            "session_id": session_id,
                            "message": f"backend error: {str(resp.get('error', '') or '')}",
                            "request_id": request_id,
                        }
                    )
                    await emit_backend_state("failed", request_id, str(resp.get("error", "") or "backend_error"))
                    break

                if resp_type == "llm_stream":
                    delta = str(resp.get("delta", "") or "")
                    if delta:
                        reply_parts.append(delta)
                        if not active_first_token_seen:
                            active_first_token_seen = True
                        if not streaming_announced:
                            streaming_announced = True
                            await emit_backend_state("streaming", request_id)
                        await send_event(
                            {
                                "event": "backend_stream",
                                "session_id": session_id,
                                "request_id": request_id,
                                "delta": delta,
                                "emotion": resp.get("emotion", ""),
                                "audio_event": resp.get("event", ""),
                                "final": False,
                            }
                        )
                    continue

                if resp_type == "llm_response":
                    reply = str(resp.get("reply", "") or "")
                    await send_event(
                        {
                            "event": "backend_result",
                            "session_id": session_id,
                            "request_id": request_id,
                            "reply": reply,
                            "emotion": resp.get("emotion", ""),
                            "audio_event": resp.get("event", ""),
                            "final": final or True,
                        }
                    )
                    if final:
                        await emit_backend_state("completed", request_id)
                    if final:
                        break
        except asyncio.TimeoutError:
            await send_event(
                {
                    "event": "warn",
                    "session_id": session_id,
                    "message": f"backend request timeout after {BACKEND_REQ_TIMEOUT_S:.1f}s",
                    "request_id": req_id,
                }
            )
            await emit_backend_state("timeout", req_id, f"{BACKEND_REQ_TIMEOUT_S:.1f}s")
        except asyncio.CancelledError:
            if active_first_token_seen:
                partial = "".join(reply_parts).strip()
                if partial:
                    await send_event(
                        {
                            "event": "backend_result",
                            "session_id": session_id,
                            "request_id": req_id,
                            "reply": partial,
                            "final": True,
                            "interrupted": True,
                        }
                    )
            await emit_backend_state("interrupted", req_id)
            raise
        except Exception as exc:
            await send_event(
                {
                    "event": "warn",
                    "session_id": session_id,
                    "message": f"backend request failed: {type(exc).__name__}: {exc}",
                    "request_id": req_id,
                }
            )
            await emit_backend_state("failed", req_id, f"{type(exc).__name__}: {exc}")

    async def backend_dispatcher() -> None:
        nonlocal active_backend_task, active_request_id, active_request_text, active_first_token_seen
        try:
            while True:
                payload = await backend_queue.get()
                active_request_id = str(payload.get("request_id", "") or "")
                active_request_text = str(payload.get("text", "") or "")
                active_first_token_seen = False
                active_backend_task = asyncio.create_task(
                    run_backend_payload(payload),
                    name=f"backend-run-{active_request_id}",
                )
                try:
                    await active_backend_task
                except asyncio.CancelledError:
                    if ws_closed:
                        raise
                except Exception:
                    logger.exception("backend dispatcher run failed")
                finally:
                    active_backend_task = None
                    active_request_id = ""
                    active_request_text = ""
                    active_first_token_seen = False
                    backend_queue.task_done()
        finally:
            if active_backend_task is not None and not active_backend_task.done():
                active_backend_task.cancel()
                with suppress(Exception):
                    await active_backend_task

    async def ingest_final_candidate(parsed: ParsedText, now_ms: int, text_class: str) -> None:
        nonlocal merge_started_ms, merge_last_ms, merge_emotion, merge_event
        text = parsed.clean_text.strip()
        if text == "":
            return

        if active_backend_task is not None and not active_backend_task.done():
            if text_class == "normal" and (not active_first_token_seen) and INTERRUPT_PRE_TOKEN:
                await interrupt_active("pre_token", merge_back_current_request=True)
            elif active_first_token_seen and should_interrupt_post_token(text, text_class):
                await interrupt_active("post_token", merge_back_current_request=False)

        if not merge_texts:
            merge_started_ms = now_ms
        merge_last_ms = now_ms
        merge_emotion = parsed.emotion
        merge_event = parsed.event
        merge_texts.append(text)

        if now_ms-merge_started_ms >= FINAL_MERGE_MAX_MS:
            await commit_merged("max_window")
            return
        schedule_merge_timer()

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
        do_submit, reason, text_class = should_submit(parsed, now_ms, last_submit_ms)
        if not do_submit:
            await send_event(
                {
                    "event": "filtered",
                    "session_id": session_id,
                    "reason": reason,
                    "text": parsed.clean_text,
                }
            )
            return True
        last_submit_ms = now_ms
        await ingest_final_candidate(parsed, now_ms, text_class)
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
        await commit_merged("flush")

    backend_dispatcher_task = asyncio.create_task(backend_dispatcher(), name=f"backend-dispatcher-{session_id}")

    try:
        while True:
            msg = await websocket.receive()
            if msg.get("type") == "websocket.disconnect":
                ws_closed = True
                logger.info("client websocket disconnect event received")
                break
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
                    await send_event({"event": "status", "session_id": session_id, "message": "flushed"})
                elif event == "ping":
                    await send_event({"event": "pong", "session_id": session_id})
                continue
    except WebSocketDisconnect as exc:
        ws_closed = True
        logger.info("client websocket disconnected: code=%s", getattr(exc, "code", "unknown"))
    except Exception as exc:
        logger.exception("ws_client unexpected error")
        with suppress(Exception):
            await send_event({"event": "warn", "session_id": session_id, "message": str(exc)})
    finally:
        ws_closed = True
        cancel_merge_timer()
        if backend_dispatcher_task is not None:
            backend_dispatcher_task.cancel()
            with suppress(Exception, asyncio.CancelledError):
                await backend_dispatcher_task


app.mount("/", StaticFiles(directory=WEB_DIR, html=True), name="web")
