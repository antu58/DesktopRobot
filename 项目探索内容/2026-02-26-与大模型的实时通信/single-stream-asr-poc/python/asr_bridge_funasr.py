#!/usr/bin/env python3
"""
WebSocket ASR bridge powered by FunASR.

Protocol:
- Binary message: PCM16LE mono audio chunk at 16kHz
- Text/JSON message: {"event":"flush"} to force finalization
- Server response JSON: {"text": "...", "is_final": bool, "error": "..."}
"""

from __future__ import annotations

import json
import os
from typing import Any, Dict, List, Tuple

import numpy as np
from fastapi import FastAPI, WebSocket, WebSocketDisconnect
from fastapi.responses import JSONResponse
from funasr import AutoModel

SAMPLE_RATE = 16000
STRICT_MODEL = os.getenv("STRICT_MODEL", "1").strip() == "1"
PIPELINE_MODE = os.getenv("PIPELINE_MODE", "vad_segment").strip().lower()
FUNASR_MODEL = os.getenv("FUNASR_MODEL", "iic/SenseVoiceSmall").strip()
FUNASR_HUB = os.getenv("FUNASR_HUB", "ms").strip()
FUNASR_DEVICE = os.getenv("FUNASR_DEVICE", "cpu").strip()
VAD_MODEL = os.getenv("VAD_MODEL", "fsmn-vad").strip()
VAD_CHUNK_MS = max(50, int(os.getenv("VAD_CHUNK_MS", "200")))
ASR_LANGUAGE = os.getenv("ASR_LANGUAGE", "auto").strip()
ASR_USE_ITN = os.getenv("ASR_USE_ITN", "1").strip() == "1"
ASR_BATCH_SIZE_S = max(1, int(os.getenv("ASR_BATCH_SIZE_S", "60")))
MAX_SEGMENT_MS = max(1000, int(os.getenv("MAX_SEGMENT_MS", "30000")))
PRE_ROLL_MS = max(0, int(os.getenv("PRE_ROLL_MS", "120")))
if PIPELINE_MODE not in {"vad_segment", "streaming"}:
    PIPELINE_MODE = "vad_segment"


def parse_chunk_size(value: str) -> List[int]:
    raw = [s.strip() for s in value.split(",") if s.strip()]
    if len(raw) != 3:
        return [0, 10, 5]
    try:
        return [int(raw[0]), int(raw[1]), int(raw[2])]
    except ValueError:
        return [0, 10, 5]


CHUNK_SIZE = parse_chunk_size(os.getenv("CHUNK_SIZE", "0,10,5"))
ENCODER_CHUNK_LOOK_BACK = int(os.getenv("ENCODER_CHUNK_LOOK_BACK", "4"))
DECODER_CHUNK_LOOK_BACK = int(os.getenv("DECODER_CHUNK_LOOK_BACK", "1"))
CHUNK_STRIDE_SAMPLES = max(1, CHUNK_SIZE[1]) * 960
VAD_CHUNK_SAMPLES = max(1, int(SAMPLE_RATE * VAD_CHUNK_MS / 1000))
MAX_SEGMENT_SAMPLES = max(1, int(SAMPLE_RATE * MAX_SEGMENT_MS / 1000))
PRE_ROLL_SAMPLES = max(0, int(SAMPLE_RATE * PRE_ROLL_MS / 1000))

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
    if PIPELINE_MODE == "vad_segment":
        vad_model = create_model(VAD_MODEL)
except Exception as exc:  # pragma: no cover
    model_init_error = str(exc)
    if STRICT_MODEL:
        raise RuntimeError(f"failed to load FunASR model: {model_init_error}") from exc

app = FastAPI(title="ASR Bridge (FunASR)")


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
    for i in range(len(optional_keys) + 1):
        try:
            return model.generate(**kwargs)
        except TypeError:
            if i >= len(optional_keys):
                raise
            kwargs.pop(optional_keys[i], None)


@app.get("/healthz")
async def healthz():
    return JSONResponse(
        {
            "status": "ok",
            "engine": "funasr",
            "model_ready": asr_model is not None and (PIPELINE_MODE != "vad_segment" or vad_model is not None),
            "mode": PIPELINE_MODE,
            "asr_model": FUNASR_MODEL,
            "vad_model": VAD_MODEL if PIPELINE_MODE == "vad_segment" else "",
            "hub": FUNASR_HUB,
            "chunk_size": CHUNK_SIZE,
            "vad_chunk_ms": VAD_CHUNK_MS,
            "max_segment_ms": MAX_SEGMENT_MS,
            "asr_language": ASR_LANGUAGE,
            "model_error": model_init_error,
        }
    )


@app.websocket("/ws")
async def ws_asr(websocket: WebSocket):
    await websocket.accept()
    if asr_model is None or (PIPELINE_MODE == "vad_segment" and vad_model is None):
        await websocket.send_json({"text": "", "is_final": False, "error": model_init_error})
        await websocket.close()
        return

    pending = np.zeros((0,), dtype=np.float32)

    stream_cache: Dict[str, Any] = {}
    vad_cache: Dict[str, Any] = {}
    history = np.zeros((0,), dtype=np.float32)
    segment_buffer = np.zeros((0,), dtype=np.float32)
    in_segment = False

    async def emit_streaming(audio_chunk: np.ndarray, is_final: bool) -> bool:
        result = asr_model.generate(
            input=audio_chunk,
            cache=stream_cache,
            is_final=is_final,
            chunk_size=CHUNK_SIZE,
            encoder_chunk_look_back=ENCODER_CHUNK_LOOK_BACK,
            decoder_chunk_look_back=DECODER_CHUNK_LOOK_BACK,
        )
        text = extract_text(result)
        if text or is_final:
            await websocket.send_json({"text": text, "is_final": is_final})
            return True if is_final else bool(text)
        return False

    async def emit_segment_final(audio_chunk: np.ndarray) -> bool:
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
        await websocket.send_json({"text": text, "is_final": True})
        return True

    async def process_vad_chunk(audio_chunk: np.ndarray, is_final: bool) -> bool:
        nonlocal history, segment_buffer, in_segment

        prior_history = history
        history = append_tail(history, audio_chunk, PRE_ROLL_SAMPLES)
        vad_res = vad_model.generate(
            input=audio_chunk,
            cache=vad_cache,
            is_final=is_final,
            chunk_size=VAD_CHUNK_MS,
        )
        events = extract_vad_events(vad_res)
        has_begin = any(beg >= 0 for beg, _ in events)
        has_end = any(end >= 0 for _, end in events)

        emitted = False
        was_in_segment = in_segment

        if was_in_segment:
            segment_buffer = np.concatenate((segment_buffer, audio_chunk))

        if has_begin and not was_in_segment:
            in_segment = True
            prefix = prior_history[-PRE_ROLL_SAMPLES:] if PRE_ROLL_SAMPLES > 0 else np.zeros((0,), dtype=np.float32)
            segment_buffer = np.concatenate((prefix, audio_chunk))

        if in_segment and segment_buffer.size >= MAX_SEGMENT_SAMPLES:
            emitted = await emit_segment_final(segment_buffer)
            segment_buffer = np.zeros((0,), dtype=np.float32)
            in_segment = False

        if has_end and in_segment:
            emitted = (await emit_segment_final(segment_buffer)) or emitted
            segment_buffer = np.zeros((0,), dtype=np.float32)
            in_segment = False

        return emitted

    async def flush_all() -> None:
        nonlocal pending, stream_cache, vad_cache, history, segment_buffer, in_segment
        emitted = False
        if PIPELINE_MODE == "streaming":
            if pending.size > 0:
                emitted = await emit_streaming(pending, is_final=True)
            pending = np.zeros((0,), dtype=np.float32)
            stream_cache = {}
            if not emitted:
                await websocket.send_json({"text": "", "is_final": True})
            return

        while pending.size >= VAD_CHUNK_SAMPLES:
            chunk = pending[:VAD_CHUNK_SAMPLES]
            pending = pending[VAD_CHUNK_SAMPLES:]
            emitted = (await process_vad_chunk(chunk, is_final=False)) or emitted

        if pending.size > 0:
            emitted = (await process_vad_chunk(pending, is_final=True)) or emitted
            pending = np.zeros((0,), dtype=np.float32)

        if in_segment and segment_buffer.size > 0:
            emitted = (await emit_segment_final(segment_buffer)) or emitted
            segment_buffer = np.zeros((0,), dtype=np.float32)
            in_segment = False

        vad_cache = {}
        history = np.zeros((0,), dtype=np.float32)
        if not emitted:
            await websocket.send_json({"text": "", "is_final": True})

    try:
        while True:
            message = await websocket.receive()
            if "bytes" in message and message["bytes"] is not None:
                audio_bytes = message["bytes"]
                if len(audio_bytes) == 0:
                    continue
                pcm16 = np.frombuffer(audio_bytes, dtype=np.int16)
                if pcm16.size == 0:
                    continue
                float_pcm = pcm16.astype(np.float32) / 32768.0
                pending = np.concatenate((pending, float_pcm))
                if PIPELINE_MODE == "streaming":
                    while pending.size >= CHUNK_STRIDE_SAMPLES:
                        chunk = pending[:CHUNK_STRIDE_SAMPLES]
                        pending = pending[CHUNK_STRIDE_SAMPLES:]
                        await emit_streaming(chunk, is_final=False)
                else:
                    while pending.size >= VAD_CHUNK_SAMPLES:
                        chunk = pending[:VAD_CHUNK_SAMPLES]
                        pending = pending[VAD_CHUNK_SAMPLES:]
                        await process_vad_chunk(chunk, is_final=False)
                continue

            if "text" in message and message["text"]:
                try:
                    event = json.loads(message["text"]).get("event", "")
                except Exception:
                    event = ""
                if event == "flush":
                    await flush_all()
                    continue
    except WebSocketDisconnect:
        return
    except Exception as exc:
        await websocket.send_json({"text": "", "is_final": True, "error": str(exc)})
