#!/usr/bin/env python3
"""
WebSocket ASR bridge powered by FunASR streaming model.

Protocol:
- Binary message: PCM16LE mono audio chunk at 16kHz
- Text/JSON message: {"event":"flush"} to force finalization
- Server response JSON: {"text": "...", "is_final": bool, "error": "..."}
"""

from __future__ import annotations

import json
import os
from typing import Any, Dict, List

import numpy as np
from fastapi import FastAPI, WebSocket, WebSocketDisconnect
from fastapi.responses import JSONResponse
from funasr import AutoModel

SAMPLE_RATE = 16000
STRICT_MODEL = os.getenv("STRICT_MODEL", "1").strip() == "1"
FUNASR_MODEL = os.getenv("FUNASR_MODEL", "paraformer-zh-streaming").strip()
FUNASR_HUB = os.getenv("FUNASR_HUB", "ms").strip()
FUNASR_DEVICE = os.getenv("FUNASR_DEVICE", "cpu").strip()


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

model = None
model_init_error = ""
try:
    try:
        model = AutoModel(model=FUNASR_MODEL, hub=FUNASR_HUB, device=FUNASR_DEVICE)
    except TypeError:
        model = AutoModel(model=FUNASR_MODEL, model_hub=FUNASR_HUB, device=FUNASR_DEVICE)
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


@app.get("/healthz")
async def healthz():
    return JSONResponse(
        {
            "status": "ok",
            "engine": "funasr",
            "model_ready": model is not None,
            "model": FUNASR_MODEL,
            "hub": FUNASR_HUB,
            "chunk_size": CHUNK_SIZE,
            "model_error": model_init_error,
        }
    )


@app.websocket("/ws")
async def ws_asr(websocket: WebSocket):
    await websocket.accept()
    if model is None:
        await websocket.send_json({"text": "", "is_final": False, "error": model_init_error})
        await websocket.close()
        return

    stream_cache: Dict[str, Any] = {}
    pending = np.zeros((0,), dtype=np.float32)

    async def infer_and_emit(audio_chunk: np.ndarray, is_final: bool) -> None:
        result = model.generate(
            input=audio_chunk,
            cache=stream_cache,
            is_final=is_final,
            chunk_size=CHUNK_SIZE,
            encoder_chunk_look_back=ENCODER_CHUNK_LOOK_BACK,
            decoder_chunk_look_back=DECODER_CHUNK_LOOK_BACK,
        )
        text = extract_text(result)
        if text:
            await websocket.send_json({"text": text, "is_final": is_final})

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
                while pending.size >= CHUNK_STRIDE_SAMPLES:
                    chunk = pending[:CHUNK_STRIDE_SAMPLES]
                    pending = pending[CHUNK_STRIDE_SAMPLES:]
                    await infer_and_emit(chunk, is_final=False)
                continue

            if "text" in message and message["text"]:
                try:
                    event = json.loads(message["text"]).get("event", "")
                except Exception:
                    event = ""
                if event == "flush":
                    if pending.size > 0:
                        await infer_and_emit(pending, is_final=True)
                    else:
                        await websocket.send_json({"text": "", "is_final": True})
                    pending = np.zeros((0,), dtype=np.float32)
                    continue
    except WebSocketDisconnect:
        return
    except Exception as exc:
        await websocket.send_json({"text": "", "is_final": True, "error": str(exc)})
