import logging
import os
import shutil
import time
from functools import lru_cache
from math import sqrt
from pathlib import Path
from typing import Any

from fastapi import FastAPI, HTTPException
from pydantic import BaseModel, Field
from transformers import AutoTokenizer, pipeline

MODEL_ID = os.getenv(
    "EMOTION_MODEL_ID", "MoritzLaurer/mDeBERTa-v3-base-xnli-multilingual-nli-2mil7"
)
ENGINE = "python-mdeberta-xnli-pad"
HYPOTHESIS_TEMPLATE = "这句话表达的是{}。"
WARMUP_TEXT = os.getenv("EMOTION_WARMUP_TEXT", "你好")
USE_ONNX = os.getenv("EMOTION_USE_ONNX", "1") == "1"
USE_ONNX_INT8 = os.getenv("EMOTION_ONNX_INT8", "1") == "1"
HF_HOME = os.getenv("HF_HOME", "/models")
ONNX_ROOT = Path(os.getenv("EMOTION_ONNX_DIR", str(Path(HF_HOME) / "onnx")))
ONNX_MODEL_DIR = ONNX_ROOT / MODEL_ID.replace("/", "--")
ONNX_INT8_DIR = ONNX_MODEL_DIR / "int8"

logger = logging.getLogger("emotion-server")
logging.basicConfig(level=os.getenv("LOG_LEVEL", "INFO"))

RUNTIME_STATE: dict[str, Any] = {
    "backend": "pending",
    "int8": False,
    "model_dir": "",
    "warmup_ok": False,
    "warmup_ms": None,
    "warmup_error": "",
}

# 15-class PAD table kept as canonical downstream space.
PAD_MAP: dict[str, dict[str, float]] = {
    "neutral": {"p": 0.00, "a": 0.05, "d": 0.00},
    "joy": {"p": 0.70, "a": 0.55, "d": 0.20},
    "surprise": {"p": 0.10, "a": 0.75, "d": -0.05},
    "sadness": {"p": -0.65, "a": -0.15, "d": -0.35},
    "fear": {"p": -0.70, "a": 0.70, "d": -0.60},
    "anger": {"p": -0.60, "a": 0.75, "d": 0.25},
    "disgust": {"p": -0.55, "a": 0.35, "d": 0.10},
    "calm": {"p": 0.20, "a": -0.35, "d": 0.15},
    "relief": {"p": 0.50, "a": -0.20, "d": 0.30},
    "gratitude": {"p": 0.60, "a": 0.20, "d": 0.35},
    "excitement": {"p": 0.78, "a": 0.82, "d": 0.30},
    "anxiety": {"p": -0.62, "a": 0.72, "d": -0.48},
    "frustration": {"p": -0.52, "a": 0.58, "d": -0.08},
    "disappointment": {"p": -0.58, "a": -0.08, "d": -0.28},
    "boredom": {"p": -0.20, "a": -0.45, "d": -0.15},
}

AXIS_ANCHORS: dict[str, dict[str, list[str]]] = {
    "p": {
        "pos": ["积极愉悦", "满足开心", "被认可"],
        "neg": ["痛苦消极", "失落难受", "被否定"],
    },
    "a": {
        "pos": ["激动紧张", "高唤醒状态", "情绪强烈"],
        "neg": ["平静放松", "低唤醒状态", "情绪平缓"],
    },
    "d": {
        "pos": ["掌控自信", "主导主动", "有能力应对"],
        "neg": ["无力被压制", "受控退缩", "难以掌控局面"],
    },
}

ALL_ANCHOR_LABELS: list[str] = [
    label for axis in AXIS_ANCHORS.values() for side in axis.values() for label in side
]

# Alias normalization only for /convert compatibility.
LABEL_ALIASES = {
    "neutral": "neutral",
    "joy": "joy",
    "happy": "joy",
    "happiness": "joy",
    "surprise": "surprise",
    "sadness": "sadness",
    "sad": "sadness",
    "fear": "fear",
    "anger": "anger",
    "angry": "anger",
    "disgust": "disgust",
    "anxiety": "anxiety",
    "frustration": "frustration",
    "disappointment": "disappointment",
    "calm": "calm",
    "relief": "relief",
    "gratitude": "gratitude",
    "excitement": "excitement",
    "boredom": "boredom",
}


class AnalyzeRequest(BaseModel):
    text: str = Field(..., min_length=1)


class ConvertRequest(BaseModel):
    emotion: str
    confidence: float = Field(..., ge=0.0, le=1.0)


def normalize_label(label: str) -> str | None:
    if not label:
        return None
    key = label.strip().lower()
    if key in PAD_MAP:
        return key
    return LABEL_ALIASES.get(key)


def clamp(v: float, lo: float = -1.0, hi: float = 1.0) -> float:
    return max(lo, min(hi, v))


def _ensure_onnx_export() -> Path:
    if (ONNX_MODEL_DIR / "config.json").exists() and list(ONNX_MODEL_DIR.glob("*.onnx")):
        return ONNX_MODEL_DIR

    ONNX_MODEL_DIR.mkdir(parents=True, exist_ok=True)

    from optimum.onnxruntime import ORTModelForSequenceClassification

    logger.info("Exporting model to ONNX: %s", MODEL_ID)
    ort_model = ORTModelForSequenceClassification.from_pretrained(
        MODEL_ID,
        export=True,
        provider="CPUExecutionProvider",
    )
    tokenizer = AutoTokenizer.from_pretrained(MODEL_ID)
    ort_model.save_pretrained(str(ONNX_MODEL_DIR))
    tokenizer.save_pretrained(str(ONNX_MODEL_DIR))
    return ONNX_MODEL_DIR


def _copy_support_files(src_dir: Path, dst_dir: Path) -> None:
    for src in src_dir.iterdir():
        if src.is_file() and src.suffix != ".onnx":
            shutil.copy2(src, dst_dir / src.name)


def _ensure_onnx_int8(src_dir: Path) -> tuple[Path, bool]:
    if not USE_ONNX_INT8:
        return src_dir, False

    if (ONNX_INT8_DIR / "config.json").exists() and list(ONNX_INT8_DIR.glob("*.onnx")):
        return ONNX_INT8_DIR, True

    from onnxruntime.quantization import QuantType, quantize_dynamic

    onnx_files = list(src_dir.glob("*.onnx"))
    if not onnx_files:
        return src_dir, False

    ONNX_INT8_DIR.mkdir(parents=True, exist_ok=True)
    _copy_support_files(src_dir, ONNX_INT8_DIR)

    logger.info("Quantizing ONNX model to int8: %s", src_dir)
    for onnx_file in onnx_files:
        quantize_dynamic(
            model_input=str(onnx_file),
            model_output=str(ONNX_INT8_DIR / onnx_file.name),
            weight_type=QuantType.QInt8,
        )

    return ONNX_INT8_DIR, True


def _build_onnx_pipeline():
    from optimum.onnxruntime import ORTModelForSequenceClassification

    exported_dir = _ensure_onnx_export()
    load_dir, int8_enabled = _ensure_onnx_int8(exported_dir)

    model = ORTModelForSequenceClassification.from_pretrained(
        str(load_dir),
        provider="CPUExecutionProvider",
    )
    tokenizer = AutoTokenizer.from_pretrained(str(load_dir))
    classifier = pipeline(
        task="zero-shot-classification",
        model=model,
        tokenizer=tokenizer,
        device=-1,
    )

    RUNTIME_STATE["backend"] = "onnxruntime"
    RUNTIME_STATE["int8"] = bool(int8_enabled)
    RUNTIME_STATE["model_dir"] = str(load_dir)
    logger.info("Emotion backend initialized: onnxruntime (int8=%s)", int8_enabled)
    return classifier


@lru_cache(maxsize=1)
def get_classifier():
    if not USE_ONNX:
        raise RuntimeError("ONNX backend is required. Set EMOTION_USE_ONNX=1.")
    return _build_onnx_pipeline()


def infer_pad(text: str) -> tuple[float, float, float, float]:
    classifier = get_classifier()
    result = classifier(
        text,
        candidate_labels=ALL_ANCHOR_LABELS,
        multi_label=True,
        hypothesis_template=HYPOTHESIS_TEMPLATE,
    )

    labels = [str(x) for x in result.get("labels", [])]
    scores = [float(x) for x in result.get("scores", [])]
    score_map = dict(zip(labels, scores))

    axis_scores: dict[str, float] = {}
    axis_certainty: dict[str, float] = {}
    for axis, anchors in AXIS_ANCHORS.items():
        pos_values = [score_map.get(v, 0.0) for v in anchors["pos"]]
        neg_values = [score_map.get(v, 0.0) for v in anchors["neg"]]
        pos_mean = sum(pos_values) / max(len(pos_values), 1)
        neg_mean = sum(neg_values) / max(len(neg_values), 1)

        delta = pos_mean - neg_mean
        axis_scores[axis] = clamp(delta)
        axis_certainty[axis] = clamp(abs(delta), 0.0, 1.0)

    p = axis_scores["p"]
    a = axis_scores["a"]
    d = axis_scores["d"]

    norm = clamp(sqrt((p * p + a * a + d * d) / 3.0), 0.0, 1.0)
    certainty = (
        axis_certainty["p"] + axis_certainty["a"] + axis_certainty["d"]
    ) / 3.0
    intensity = clamp(0.65 * norm + 0.35 * certainty, 0.0, 1.0)

    return p, a, d, intensity


def infer_emotion_from_pad(p: float, a: float, d: float) -> str:
    best_label = "neutral"
    best_distance = float("inf")

    for label, pad in PAD_MAP.items():
        dp = p - pad["p"]
        da = a - pad["a"]
        dd = d - pad["d"]
        distance = dp * dp + da * da + dd * dd
        if distance < best_distance:
            best_distance = distance
            best_label = label

    return best_label


def convert_to_pad(emotion: str, confidence: float) -> dict[str, Any]:
    key = normalize_label(emotion) or "neutral"
    base = PAD_MAP.get(key, PAD_MAP["neutral"])
    return {
        "emotion": key,
        "p": round(base["p"], 3),
        "a": round(base["a"], 3),
        "d": round(base["d"], 3),
        "intensity": round(confidence, 6),
    }


app = FastAPI(title="Soul Emotion Server", version="1.0.0")


@app.on_event("startup")
def warmup_on_startup() -> None:
    start = time.perf_counter()
    try:
        get_classifier()
        infer_pad(WARMUP_TEXT)
        RUNTIME_STATE["warmup_ok"] = True
        RUNTIME_STATE["warmup_ms"] = round((time.perf_counter() - start) * 1000.0, 3)
        RUNTIME_STATE["warmup_error"] = ""
        logger.info("Emotion warmup done in %sms", RUNTIME_STATE["warmup_ms"])
    except Exception as exc:
        RUNTIME_STATE["warmup_ok"] = False
        RUNTIME_STATE["warmup_ms"] = round((time.perf_counter() - start) * 1000.0, 3)
        RUNTIME_STATE["warmup_error"] = str(exc)
        logger.warning("Emotion warmup failed in %sms: %s", RUNTIME_STATE["warmup_ms"], exc)
        raise RuntimeError(f"Emotion warmup failed: {exc}") from exc


@app.get("/healthz")
def healthz() -> dict[str, Any]:
    return {
        "ok": True,
        "engine": ENGINE,
        "model": MODEL_ID,
        "analyze_mode": "pad_direct_nli",
        "nli_hypothesis_template": HYPOTHESIS_TEMPLATE,
        "runtime_backend": RUNTIME_STATE["backend"],
        "runtime_int8": RUNTIME_STATE["int8"],
        "runtime_model_dir": RUNTIME_STATE["model_dir"],
        "warmup_ok": RUNTIME_STATE["warmup_ok"],
        "warmup_ms": RUNTIME_STATE["warmup_ms"],
        "warmup_error": RUNTIME_STATE["warmup_error"],
        "pad_labels": sorted(list(PAD_MAP.keys())),
    }


@app.get("/v1/emotion/pad-table")
def pad_table() -> dict[str, Any]:
    return {"pad_table": PAD_MAP}


@app.post("/v1/emotion/convert")
def convert(req: ConvertRequest) -> dict[str, Any]:
    start = time.perf_counter()
    out = convert_to_pad(req.emotion, req.confidence)
    out["latency_ms"] = round((time.perf_counter() - start) * 1000.0, 3)
    return out


@app.post("/v1/emotion/analyze")
def analyze(req: AnalyzeRequest) -> dict[str, Any]:
    try:
        start = time.perf_counter()
        p, a, d, intensity = infer_pad(req.text)
        emotion = infer_emotion_from_pad(p, a, d)
        out = {
            "emotion": emotion,
            "p": round(p, 3),
            "a": round(a, 3),
            "d": round(d, 3),
            "intensity": round(intensity, 6),
        }
        out["latency_ms"] = round((time.perf_counter() - start) * 1000.0, 3)
        return out
    except Exception as exc:
        raise HTTPException(status_code=500, detail=str(exc))
