import base64
import io
import json
import os
import re
import shutil
import subprocess
import sys
import tempfile
import time
from urllib.parse import quote
import wave
from pathlib import Path
from typing import Any

import numpy as np
from fastapi import FastAPI, HTTPException
from fastapi.responses import FileResponse, JSONResponse, Response, StreamingResponse
from fastapi.staticfiles import StaticFiles
from pydantic import BaseModel, Field

COSYVOICE_REPO = os.getenv("COSYVOICE_REPO", "/opt/CosyVoice")
COSY_MODEL_DIR = os.getenv("COSY_MODEL_DIR", "iic/CosyVoice-300M-SFT")
MODELSCOPE_CACHE_DIR = Path(os.getenv("MODELSCOPE_CACHE", "/models/modelscope"))
CLONE_ROOT_DIR = Path(os.getenv("COSY_CLONE_DIR", "/models/clones"))
CLONE_REGISTRY_PATH = CLONE_ROOT_DIR / "registry.json"
CLONE_PROMPT_DIR = CLONE_ROOT_DIR / "prompts"
MAX_PROMPT_AUDIO_MB = int(os.getenv("MAX_PROMPT_AUDIO_MB", "20"))
CLONE_ID_RE = re.compile(r"^[A-Za-z0-9_-]{3,64}$")
ALLOWED_AUDIO_EXT = {".wav", ".mp3", ".m4a", ".aac", ".flac", ".ogg", ".webm", ".mp4"}

sys.path.append(COSYVOICE_REPO)
sys.path.append(f"{COSYVOICE_REPO}/third_party/Matcha-TTS")

from cosyvoice.cli.cosyvoice import AutoModel  # noqa: E402

app = FastAPI(title="CosyVoice TTS Playground")

web_root = Path("/app/web")
app.mount("/static", StaticFiles(directory=web_root), name="static")

cosyvoice = None
available_voices: list[str] = []
base_voices: list[str] = []
clone_registry: dict[str, dict[str, Any]] = {}
sample_rate: int = 22050
runtime_model_dir: str = COSY_MODEL_DIR


class TTSRequest(BaseModel):
    text: str = Field(min_length=1, max_length=1200)
    speaker: str | None = None
    speed: float = Field(default=1.0, ge=0.5, le=2.0)


class CloneTTSRequest(BaseModel):
    text: str = Field(min_length=1, max_length=1200)
    clone_id: str = Field(min_length=3, max_length=64)
    speed: float = Field(default=1.0, ge=0.5, le=2.0)


class CreateCloneRequest(BaseModel):
    clone_id: str = Field(min_length=3, max_length=64)
    prompt_text: str = Field(min_length=1, max_length=200)
    audio_filename: str = Field(min_length=1, max_length=128)
    audio_base64: str = Field(min_length=16, max_length=30_000_000)


def _to_float_array(value: Any) -> np.ndarray:
    if hasattr(value, "detach"):
        value = value.detach()
    if hasattr(value, "cpu"):
        value = value.cpu()
    if hasattr(value, "numpy"):
        value = value.numpy()
    arr = np.asarray(value, dtype=np.float32).reshape(-1)
    return arr


def _wav_bytes_from_float(audio_float: np.ndarray, sr: int) -> bytes:
    pcm16 = np.clip(audio_float, -1.0, 1.0)
    pcm16 = (pcm16 * 32767.0).astype(np.int16)
    buf = io.BytesIO()
    with wave.open(buf, "wb") as wf:
        wf.setnchannels(1)
        wf.setsampwidth(2)
        wf.setframerate(sr)
        wf.writeframes(pcm16.tobytes())
    return buf.getvalue()


def _refresh_available_voices() -> None:
    global available_voices
    voices = cosyvoice.list_available_spks() if cosyvoice is not None else []
    available_voices = voices if voices else []


def _resolve_model_dir() -> str:
    if Path(COSY_MODEL_DIR).exists():
        return COSY_MODEL_DIR
    local_candidate = MODELSCOPE_CACHE_DIR / "hub" / COSY_MODEL_DIR
    if local_candidate.exists():
        return str(local_candidate)
    return COSY_MODEL_DIR


def _load_clone_registry() -> dict[str, dict[str, Any]]:
    if not CLONE_REGISTRY_PATH.exists():
        return {}
    try:
        data = json.loads(CLONE_REGISTRY_PATH.read_text(encoding="utf-8"))
        if not isinstance(data, dict):
            return {}
        return data
    except Exception:
        return {}


def _save_clone_registry() -> None:
    CLONE_ROOT_DIR.mkdir(parents=True, exist_ok=True)
    CLONE_REGISTRY_PATH.write_text(
        json.dumps(clone_registry, ensure_ascii=False, indent=2),
        encoding="utf-8",
    )


def _validate_clone_id(clone_id: str) -> str:
    normalized = clone_id.strip()
    if not CLONE_ID_RE.fullmatch(normalized):
        raise HTTPException(
            status_code=400,
            detail="clone_id must match ^[A-Za-z0-9_-]{3,64}$",
        )
    return normalized


def _save_base64_audio_to_temp_file(audio_base64: str, audio_filename: str) -> Path:
    suffix = Path(audio_filename).suffix.lower()
    if not suffix:
        raise HTTPException(status_code=400, detail="Missing file extension")
    if suffix not in ALLOWED_AUDIO_EXT:
        raise HTTPException(
            status_code=400,
            detail=f"Unsupported audio format: {suffix}. Allowed: {sorted(ALLOWED_AUDIO_EXT)}",
        )
    try:
        raw = base64.b64decode(audio_base64, validate=True)
    except Exception as exc:
        raise HTTPException(status_code=400, detail=f"Invalid audio_base64: {exc}") from exc
    with tempfile.NamedTemporaryFile(delete=False, suffix=suffix, prefix="cosy_prompt_src_") as src:
        src.write(raw)
        src_path = Path(src.name)
    size_limit = MAX_PROMPT_AUDIO_MB * 1024 * 1024
    if src_path.stat().st_size > size_limit:
        src_path.unlink(missing_ok=True)
        raise HTTPException(status_code=413, detail=f"Audio is too large, max {MAX_PROMPT_AUDIO_MB}MB")
    return src_path


def _convert_to_wav_16k(src_path: Path) -> Path:
    with tempfile.NamedTemporaryFile(delete=False, suffix=".wav", prefix="cosy_prompt_16k_") as out:
        out_path = Path(out.name)
    cmd = [
        "ffmpeg",
        "-nostdin",
        "-hide_banner",
        "-loglevel",
        "error",
        "-y",
        "-i",
        str(src_path),
        "-vn",
        "-ac",
        "1",
        "-ar",
        "16000",
        "-f",
        "wav",
        str(out_path),
    ]
    result = subprocess.run(cmd, capture_output=True, text=True)
    if result.returncode != 0:
        out_path.unlink(missing_ok=True)
        stderr = (result.stderr or "").strip()
        raise HTTPException(status_code=400, detail=f"Failed to decode audio with ffmpeg: {stderr[:300]}")
    return out_path


def _persist_prompt_audio(clone_id: str, prompt_wav_16k_path: Path) -> str:
    CLONE_PROMPT_DIR.mkdir(parents=True, exist_ok=True)
    final_path = CLONE_PROMPT_DIR / f"{clone_id}.wav"
    shutil.copy2(prompt_wav_16k_path, final_path)
    return str(final_path)


def _list_detected_clone_ids() -> list[str]:
    known = set(clone_registry.keys())
    for voice in available_voices:
        if voice not in base_voices:
            known.add(voice)
    return sorted(known)


def _build_synthesis_response(pieces: list[np.ndarray], voice_name: str, elapsed_ms: int, mode: str) -> Response:
    if not pieces:
        raise HTTPException(status_code=500, detail="CosyVoice returned empty audio")
    audio = np.concatenate(pieces)
    duration_sec = float(len(audio) / sample_rate)
    wav_bytes = _wav_bytes_from_float(audio, sample_rate)
    headers = {
        "X-Voice": quote(voice_name, safe=""),
        "X-Mode": mode,
        "X-Sample-Rate": str(sample_rate),
        "X-Duration-Sec": f"{duration_sec:.3f}",
        "X-Elapsed-Ms": str(elapsed_ms),
    }
    return Response(content=wav_bytes, media_type="audio/wav", headers=headers)


def _to_pcm16_bytes(audio_float: np.ndarray) -> bytes:
    pcm16 = np.clip(audio_float, -1.0, 1.0)
    pcm16 = (pcm16 * 32767.0).astype(np.int16)
    return pcm16.tobytes()


@app.on_event("startup")
def startup() -> None:
    global cosyvoice, available_voices, base_voices, clone_registry, sample_rate, runtime_model_dir
    CLONE_ROOT_DIR.mkdir(parents=True, exist_ok=True)
    CLONE_PROMPT_DIR.mkdir(parents=True, exist_ok=True)

    runtime_model_dir = _resolve_model_dir()
    cosyvoice = AutoModel(model_dir=runtime_model_dir)
    _refresh_available_voices()
    base_voices = list(available_voices)
    clone_registry = _load_clone_registry()
    sample_rate = int(cosyvoice.sample_rate)


@app.get("/")
def index() -> FileResponse:
    return FileResponse(web_root / "index.html")


@app.get("/stream-test")
def stream_test_page() -> FileResponse:
    return FileResponse(web_root / "stream_test.html")


@app.get("/api/healthz")
def healthz() -> JSONResponse:
    return JSONResponse(
        {
            "status": "ok",
            "model_dir": COSY_MODEL_DIR,
            "runtime_model_dir": runtime_model_dir,
            "sample_rate": sample_rate,
            "voice_count": len(available_voices),
            "clone_count": len(_list_detected_clone_ids()),
        }
    )


@app.get("/api/voices")
def voices() -> JSONResponse:
    return JSONResponse(
        {
            "voices": available_voices,
            "base_voices": base_voices,
            "clone_voices": _list_detected_clone_ids(),
            "sample_rate": sample_rate,
        }
    )


@app.get("/api/clones")
def list_clones() -> JSONResponse:
    clones: list[dict[str, Any]] = []
    detected = _list_detected_clone_ids()
    for clone_id in detected:
        meta = clone_registry.get(clone_id, {})
        clones.append(
            {
                "clone_id": clone_id,
                "prompt_text": meta.get("prompt_text", ""),
                "source_filename": meta.get("source_filename", ""),
                "prompt_wav": meta.get("prompt_wav", ""),
                "created_at": meta.get("created_at", 0),
            }
        )
    return JSONResponse({"clones": clones})


@app.post("/api/clones")
def create_clone(req: CreateCloneRequest) -> JSONResponse:
    if cosyvoice is None:
        raise HTTPException(status_code=503, detail="CosyVoice model is not ready")

    clone_id = _validate_clone_id(req.clone_id)
    if clone_id in base_voices:
        raise HTTPException(status_code=409, detail="clone_id conflicts with built-in voice")

    normalized_prompt_text = req.prompt_text.strip()
    if not normalized_prompt_text:
        raise HTTPException(status_code=400, detail="prompt_text cannot be empty")
    if len(normalized_prompt_text) > 200:
        raise HTTPException(status_code=400, detail="prompt_text too long (max 200 chars)")

    src_path: Path | None = None
    wav_16k_path: Path | None = None
    try:
        src_path = _save_base64_audio_to_temp_file(req.audio_base64, req.audio_filename)
        wav_16k_path = _convert_to_wav_16k(src_path)
        cosyvoice.add_zero_shot_spk(normalized_prompt_text, str(wav_16k_path), clone_id)
        cosyvoice.save_spkinfo()
        prompt_wav_path = _persist_prompt_audio(clone_id, wav_16k_path)
    finally:
        if src_path is not None:
            src_path.unlink(missing_ok=True)
        if wav_16k_path is not None:
            wav_16k_path.unlink(missing_ok=True)

    clone_registry[clone_id] = {
        "prompt_text": normalized_prompt_text,
        "source_filename": req.audio_filename,
        "prompt_wav": prompt_wav_path,
        "created_at": int(time.time()),
    }
    _save_clone_registry()
    _refresh_available_voices()
    return JSONResponse(
        {
            "ok": True,
            "clone_id": clone_id,
            "voice_count": len(available_voices),
            "clone_count": len(_list_detected_clone_ids()),
        }
    )


@app.post("/api/synthesize")
def synthesize(req: TTSRequest) -> Response:
    if cosyvoice is None:
        raise HTTPException(status_code=503, detail="CosyVoice model is not ready")
    if not available_voices:
        raise HTTPException(status_code=500, detail="No available voices from current model")

    speaker = req.speaker if req.speaker in available_voices else available_voices[0]
    text = req.text.strip()
    if not text:
        raise HTTPException(status_code=400, detail="Text is empty after trim")

    started = time.time()
    pieces: list[np.ndarray] = []
    for output in cosyvoice.inference_sft(text, speaker, stream=False, speed=req.speed):
        speech = output.get("tts_speech")
        if speech is None:
            continue
        pieces.append(_to_float_array(speech))

    elapsed_ms = int((time.time() - started) * 1000)
    return _build_synthesis_response(pieces, speaker, elapsed_ms, mode="sft")


@app.post("/api/synthesize/clone")
def synthesize_clone(req: CloneTTSRequest) -> Response:
    if cosyvoice is None:
        raise HTTPException(status_code=503, detail="CosyVoice model is not ready")

    clone_id = _validate_clone_id(req.clone_id)
    if clone_id not in _list_detected_clone_ids():
        raise HTTPException(status_code=404, detail=f"clone_id not found: {clone_id}")

    text = req.text.strip()
    if not text:
        raise HTTPException(status_code=400, detail="Text is empty after trim")

    started = time.time()
    pieces: list[np.ndarray] = []
    for output in cosyvoice.inference_zero_shot(text, "", "", zero_shot_spk_id=clone_id, stream=False, speed=req.speed):
        speech = output.get("tts_speech")
        if speech is None:
            continue
        pieces.append(_to_float_array(speech))
    elapsed_ms = int((time.time() - started) * 1000)
    return _build_synthesis_response(pieces, clone_id, elapsed_ms, mode="clone")


@app.post("/api/synthesize/clone/stream")
def synthesize_clone_stream(req: CloneTTSRequest) -> StreamingResponse:
    if cosyvoice is None:
        raise HTTPException(status_code=503, detail="CosyVoice model is not ready")

    clone_id = _validate_clone_id(req.clone_id)
    if clone_id not in _list_detected_clone_ids():
        raise HTTPException(status_code=404, detail=f"clone_id not found: {clone_id}")

    text = req.text.strip()
    if not text:
        raise HTTPException(status_code=400, detail="Text is empty after trim")

    def generate_pcm():
        for output in cosyvoice.inference_zero_shot(
            text,
            "",
            "",
            zero_shot_spk_id=clone_id,
            stream=True,
            speed=req.speed,
        ):
            speech = output.get("tts_speech")
            if speech is None:
                continue
            audio_float = _to_float_array(speech)
            if audio_float.size == 0:
                continue
            yield _to_pcm16_bytes(audio_float)

    headers = {
        "X-Voice": quote(clone_id, safe=""),
        "X-Mode": "clone_stream",
        "X-Sample-Rate": str(sample_rate),
        "X-Channels": "1",
        "X-Audio-Format": "pcm_s16le",
    }
    return StreamingResponse(generate_pcm(), media_type="application/octet-stream", headers=headers)
