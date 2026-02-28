#!/usr/bin/env python3
from __future__ import annotations

import asyncio
import base64
import logging
import os
import time
from dataclasses import dataclass, field
from typing import Any, Dict, List, Optional, Tuple

import cv2
import numpy as np
import torch
from easyocr import Reader
from fastapi import FastAPI, WebSocket, WebSocketDisconnect
from fastapi.responses import JSONResponse
from fastapi.staticfiles import StaticFiles
from PIL import Image
from torchvision import models, transforms
from transformers import BlipForConditionalGeneration, BlipProcessor
from ultralytics import YOLO


LOG_LEVEL = os.getenv("LOG_LEVEL", "INFO").upper()
logging.basicConfig(level=getattr(logging, LOG_LEVEL, logging.INFO))
logger = logging.getLogger("video-service")

# Fast path.
YOLO_MODEL = os.getenv("YOLO_MODEL", "yolov8n.pt").strip()
YOLO_CONF = float(os.getenv("YOLO_CONF", "0.25"))
YOLO_IMGSZ = int(os.getenv("YOLO_IMGSZ", "640"))
OCR_LANGS = [s.strip() for s in os.getenv("OCR_LANGS", "ch_sim,en").split(",") if s.strip()]
OCR_CONF = float(os.getenv("OCR_CONF", "0.40"))

# Slow path (keyframe-only VLM).
VLM_MODEL = os.getenv("VLM_MODEL", "Salesforce/blip-image-captioning-large").strip()
VLM_MAX_NEW_TOKENS = int(os.getenv("VLM_MAX_NEW_TOKENS", "48"))
VLM_KEYFRAME_THRESHOLD = float(os.getenv("VLM_KEYFRAME_THRESHOLD", "0.16"))
VLM_COOLDOWN_MS = int(os.getenv("VLM_COOLDOWN_MS", "3500"))
VLM_ENABLE = os.getenv("VLM_ENABLE", "1").strip() == "1"

# Tracking / events.
TRACK_IOU_THRESHOLD = float(os.getenv("TRACK_IOU_THRESHOLD", "0.35"))
TRACK_MAX_MISSING = int(os.getenv("TRACK_MAX_MISSING", "5"))
FACE_MATCH_THRESHOLD = float(os.getenv("FACE_MATCH_THRESHOLD", "0.78"))
PHONE_START_FRAMES = int(os.getenv("PHONE_START_FRAMES", "2"))
PHONE_STOP_FRAMES = int(os.getenv("PHONE_STOP_FRAMES", "2"))
EAT_START_FRAMES = int(os.getenv("EAT_START_FRAMES", "2"))
EAT_STOP_FRAMES = int(os.getenv("EAT_STOP_FRAMES", "3"))
CONV_START_FRAMES = int(os.getenv("CONV_START_FRAMES", "3"))
CONV_STOP_FRAMES = int(os.getenv("CONV_STOP_FRAMES", "2"))
MAX_LOGS = int(os.getenv("MAX_LOGS", "300"))

DEFAULT_VLM_PROMPT = "请用简洁中文描述这个画面里人的关键变化。"
WEB_DIR = os.path.join(os.path.dirname(os.path.dirname(__file__)), "web")

PHONE_LABELS = {"cell phone", "phone"}
EAT_OBJECT_LABELS = {
    "bowl", "cup", "fork", "spoon", "knife", "wine glass", "bottle",
    "sandwich", "apple", "banana", "orange", "pizza", "hot dog", "cake", "donut",
}


@dataclass
class TrackState:
    track_id: int
    bbox: Tuple[int, int, int, int]
    last_seen_ms: int
    missing_frames: int = 0
    identity: str = "unknown"
    phone_active: bool = False
    phone_hits: int = 0
    phone_miss: int = 0
    eat_active: bool = False
    eat_hits: int = 0
    eat_miss: int = 0
    has_face: bool = False


@dataclass
class SessionState:
    session_id: str
    prev_small_gray: Optional[np.ndarray] = None
    last_vlm_ts_ms: int = 0
    last_vlm_summary: str = ""
    tracks: Dict[int, TrackState] = field(default_factory=dict)
    next_track_id: int = 1
    identities: Dict[str, np.ndarray] = field(default_factory=dict)
    next_identity_id: int = 1
    logs: List[Dict[str, Any]] = field(default_factory=list)
    last_person_count: int = 0
    pair_hits: Dict[Tuple[int, int], int] = field(default_factory=dict)
    pair_miss: Dict[Tuple[int, int], int] = field(default_factory=dict)
    active_pairs: set[Tuple[int, int]] = field(default_factory=set)


class VisionEngine:
    def __init__(self) -> None:
        self.device = "mps" if torch.backends.mps.is_available() else ("cuda" if torch.cuda.is_available() else "cpu")
        self.yolo = YOLO(YOLO_MODEL)
        self.ocr_reader = Reader(OCR_LANGS, gpu=(self.device == "cuda"), verbose=False)
        self.face_detector = cv2.CascadeClassifier(
            cv2.data.haarcascades + "haarcascade_frontalface_default.xml"
        )

        # Face embedding extractor.
        self.face_embed_error = ""
        self.face_embedder = None
        self.face_transform = transforms.Compose(
            [
                transforms.Resize((224, 224)),
                transforms.ToTensor(),
                transforms.Normalize([0.485, 0.456, 0.406], [0.229, 0.224, 0.225]),
            ]
        )
        self._load_face_embedder()

        # Keyframe VLM.
        self.vlm_processor: Optional[BlipProcessor] = None
        self.vlm_model: Optional[BlipForConditionalGeneration] = None
        self.vlm_error = ""
        if VLM_ENABLE:
            self._load_vlm()

    def _load_face_embedder(self) -> None:
        try:
            backbone = models.resnet18(weights=models.ResNet18_Weights.DEFAULT)
            self.face_embedder = torch.nn.Sequential(*list(backbone.children())[:-1])
            self.face_embedder.to(self.device)
            self.face_embedder.eval()
            logger.info("Face embedder loaded: resnet18 on %s", self.device)
        except Exception as exc:  # pragma: no cover
            self.face_embedder = None
            self.face_embed_error = f"{type(exc).__name__}: {exc}"
            logger.warning("Face embedder disabled: %s", self.face_embed_error)

    def _load_vlm(self) -> None:
        try:
            self.vlm_processor = BlipProcessor.from_pretrained(VLM_MODEL)
            self.vlm_model = BlipForConditionalGeneration.from_pretrained(VLM_MODEL)
            self.vlm_model.to(self.device)
            self.vlm_model.eval()
            logger.info("VLM loaded: %s on %s", VLM_MODEL, self.device)
        except Exception as exc:  # pragma: no cover
            self.vlm_error = f"{type(exc).__name__}: {exc}"
            self.vlm_processor = None
            self.vlm_model = None
            logger.warning("VLM disabled: %s", self.vlm_error)

    def health(self) -> Dict[str, Any]:
        return {
            "status": "ok",
            "device": self.device,
            "yolo_model": YOLO_MODEL,
            "ocr_langs": OCR_LANGS,
            "vlm_model": VLM_MODEL,
            "vlm_ready": self.vlm_model is not None and self.vlm_processor is not None,
            "vlm_error": self.vlm_error,
            "face_embedder_ready": self.face_embedder is not None,
            "face_embedder_error": self.face_embed_error,
        }

    @staticmethod
    def decode_data_url_image(data_url: str) -> np.ndarray:
        if "," not in data_url:
            raise ValueError("invalid data url")
        _, b64 = data_url.split(",", 1)
        raw = base64.b64decode(b64)
        arr = np.frombuffer(raw, dtype=np.uint8)
        frame = cv2.imdecode(arr, cv2.IMREAD_COLOR)
        if frame is None:
            raise ValueError("failed to decode image")
        return frame

    @staticmethod
    def change_score(prev_small_gray: Optional[np.ndarray], frame_bgr: np.ndarray) -> Tuple[float, np.ndarray]:
        gray = cv2.cvtColor(frame_bgr, cv2.COLOR_BGR2GRAY)
        small = cv2.resize(gray, (96, 54), interpolation=cv2.INTER_AREA)
        if prev_small_gray is None:
            return 1.0, small
        diff = cv2.absdiff(small, prev_small_gray)
        return float(np.mean(diff) / 255.0), small

    def run_yolo_detections(self, frame_bgr: np.ndarray) -> List[Dict[str, Any]]:
        results = self.yolo.predict(frame_bgr, conf=YOLO_CONF, imgsz=YOLO_IMGSZ, verbose=False)
        out: List[Dict[str, Any]] = []
        h, w = frame_bgr.shape[:2]
        for res in results:
            names = res.names if isinstance(res.names, dict) else {}
            boxes = getattr(res, "boxes", None)
            if boxes is None:
                continue
            for box in boxes:
                conf = float(box.conf.item())
                cls_idx = int(box.cls.item())
                label = str(names.get(cls_idx, cls_idx))
                x1, y1, x2, y2 = box.xyxy[0].tolist()
                bx = clamp_box((int(x1), int(y1), int(x2), int(y2)), w, h)
                out.append({"label": label, "conf": round(conf, 3), "bbox": bx})
        return out

    def run_ocr(self, frame_bgr: np.ndarray) -> List[str]:
        items = self.ocr_reader.readtext(frame_bgr)
        out: List[str] = []
        seen = set()
        for item in items:
            if len(item) < 3:
                continue
            text = str(item[1]).strip()
            conf = float(item[2])
            if not text or conf < OCR_CONF or text in seen:
                continue
            seen.add(text)
            out.append(text)
        return out

    def detect_face_in_person(
        self, frame_bgr: np.ndarray, person_box: Tuple[int, int, int, int]
    ) -> Optional[Tuple[int, int, int, int]]:
        x1, y1, x2, y2 = person_box
        if x2 <= x1 or y2 <= y1:
            return None
        roi = frame_bgr[y1:y2, x1:x2]
        if roi.size == 0:
            return None
        gray = cv2.cvtColor(roi, cv2.COLOR_BGR2GRAY)
        faces = self.face_detector.detectMultiScale(
            gray,
            scaleFactor=1.1,
            minNeighbors=4,
            minSize=(36, 36),
            flags=cv2.CASCADE_SCALE_IMAGE,
        )
        if len(faces) == 0:
            return None
        fx, fy, fw, fh = max(faces, key=lambda f: f[2] * f[3])
        return (x1 + int(fx), y1 + int(fy), x1 + int(fx + fw), y1 + int(fy + fh))

    def face_embedding(self, frame_bgr: np.ndarray, face_box: Tuple[int, int, int, int]) -> Optional[np.ndarray]:
        if self.face_embedder is None:
            return None
        x1, y1, x2, y2 = face_box
        crop = frame_bgr[y1:y2, x1:x2]
        if crop.size == 0:
            return None
        rgb = cv2.cvtColor(crop, cv2.COLOR_BGR2RGB)
        image = Image.fromarray(rgb)
        tensor = self.face_transform(image).unsqueeze(0).to(self.device)
        with torch.no_grad():
            feat = self.face_embedder(tensor).flatten().detach().cpu().numpy().astype(np.float32)
        n = float(np.linalg.norm(feat))
        if n < 1e-6:
            return None
        return feat / n

    def run_vlm(self, frame_bgr: np.ndarray) -> str:
        if self.vlm_model is None or self.vlm_processor is None:
            return ""
        rgb = cv2.cvtColor(frame_bgr, cv2.COLOR_BGR2RGB)
        image = Image.fromarray(rgb)
        inputs = self.vlm_processor(images=image, text=DEFAULT_VLM_PROMPT, return_tensors="pt")
        inputs = {k: v.to(self.device) for k, v in inputs.items()}
        with torch.no_grad():
            output = self.vlm_model.generate(**inputs, max_new_tokens=VLM_MAX_NEW_TOKENS)
        return self.vlm_processor.decode(output[0], skip_special_tokens=True).strip()


def json_or_none(raw: str) -> Optional[Dict[str, Any]]:
    import json

    try:
        obj = json.loads(raw)
    except Exception:
        return None
    return obj if isinstance(obj, dict) else None


def clamp_box(box: Tuple[int, int, int, int], width: int, height: int) -> Tuple[int, int, int, int]:
    x1, y1, x2, y2 = box
    x1 = max(0, min(x1, width - 1))
    y1 = max(0, min(y1, height - 1))
    x2 = max(0, min(x2, width - 1))
    y2 = max(0, min(y2, height - 1))
    if x2 <= x1:
        x2 = min(width - 1, x1 + 1)
    if y2 <= y1:
        y2 = min(height - 1, y1 + 1)
    return x1, y1, x2, y2


def iou(a: Tuple[int, int, int, int], b: Tuple[int, int, int, int]) -> float:
    ax1, ay1, ax2, ay2 = a
    bx1, by1, bx2, by2 = b
    ix1 = max(ax1, bx1)
    iy1 = max(ay1, by1)
    ix2 = min(ax2, bx2)
    iy2 = min(ay2, by2)
    iw = max(0, ix2 - ix1)
    ih = max(0, iy2 - iy1)
    inter = iw * ih
    if inter <= 0:
        return 0.0
    area_a = max(1, (ax2 - ax1) * (ay2 - ay1))
    area_b = max(1, (bx2 - bx1) * (by2 - by1))
    return inter / float(area_a + area_b - inter)


def center(box: Tuple[int, int, int, int]) -> Tuple[float, float]:
    x1, y1, x2, y2 = box
    return ((x1 + x2) / 2.0, (y1 + y2) / 2.0)


def inside(box: Tuple[int, int, int, int], p: Tuple[float, float]) -> bool:
    x1, y1, x2, y2 = box
    px, py = p
    return x1 <= px <= x2 and y1 <= py <= y2


def expand_box(box: Tuple[int, int, int, int], ratio: float, width: int, height: int) -> Tuple[int, int, int, int]:
    x1, y1, x2, y2 = box
    w = x2 - x1
    h = y2 - y1
    dx = int(w * ratio)
    dy = int(h * ratio)
    return clamp_box((x1 - dx, y1 - dy, x2 + dx, y2 + dy), width, height)


def person_name(track: TrackState) -> str:
    return track.identity if track.identity != "unknown" else f"track_{track.track_id}"


def append_log(session: SessionState, ts_ms: int, text: str, new_logs: List[Dict[str, Any]]) -> None:
    item = {"ts_ms": int(ts_ms), "text": text}
    session.logs.append(item)
    if len(session.logs) > MAX_LOGS:
        session.logs = session.logs[-MAX_LOGS:]
    new_logs.append(item)


def match_identity(session: SessionState, emb: np.ndarray) -> Tuple[str, float, bool]:
    best_name = ""
    best_score = -1.0
    for name, ref in session.identities.items():
        score = float(np.dot(ref, emb))
        if score > best_score:
            best_score = score
            best_name = name

    if best_name and best_score >= FACE_MATCH_THRESHOLD:
        # EMA update.
        updated = 0.85 * session.identities[best_name] + 0.15 * emb
        norm = float(np.linalg.norm(updated))
        if norm > 1e-6:
            session.identities[best_name] = updated / norm
        return best_name, best_score, False

    name = f"person_{session.next_identity_id}"
    session.next_identity_id += 1
    session.identities[name] = emb
    return name, 1.0, True


def update_tracks(
    session: SessionState,
    person_boxes: List[Tuple[int, int, int, int]],
    ts_ms: int,
    new_logs: List[Dict[str, Any]],
) -> List[int]:
    assigned: Dict[int, Tuple[int, int, int, int]] = {}
    remaining_track_ids = set(session.tracks.keys())

    for box in person_boxes:
        best_id = -1
        best_iou = 0.0
        for track_id in list(remaining_track_ids):
            score = iou(box, session.tracks[track_id].bbox)
            if score > best_iou:
                best_iou = score
                best_id = track_id
        if best_id >= 0 and best_iou >= TRACK_IOU_THRESHOLD:
            assigned[best_id] = box
            remaining_track_ids.discard(best_id)
        else:
            tid = session.next_track_id
            session.next_track_id += 1
            session.tracks[tid] = TrackState(track_id=tid, bbox=box, last_seen_ms=ts_ms)
            assigned[tid] = box
            append_log(session, ts_ms, f"画面出现新人物: track_{tid}", new_logs)

    active_ids: List[int] = []
    for track_id, track in list(session.tracks.items()):
        if track_id in assigned:
            track.bbox = assigned[track_id]
            track.last_seen_ms = ts_ms
            track.missing_frames = 0
            active_ids.append(track_id)
            continue

        track.missing_frames += 1
        if track.missing_frames > TRACK_MAX_MISSING:
            name = person_name(track)
            append_log(session, ts_ms, f"{name} 离开画面", new_logs)
            session.tracks.pop(track_id, None)
            # Clear pair states with removed person.
            for pair in list(session.active_pairs):
                if track_id in pair:
                    other = pair[0] if pair[1] == track_id else pair[1]
                    if other in session.tracks:
                        append_log(
                            session,
                            ts_ms,
                            f"{person_name(session.tracks[other])} 与 {name} 面对面交流结束",
                            new_logs,
                        )
                    session.active_pairs.discard(pair)
            for pair in list(session.pair_hits.keys()):
                if track_id in pair:
                    session.pair_hits.pop(pair, None)
            for pair in list(session.pair_miss.keys()):
                if track_id in pair:
                    session.pair_miss.pop(pair, None)

    if len(active_ids) != session.last_person_count:
        append_log(session, ts_ms, f"画面人数变化: {len(active_ids)} 人", new_logs)
        session.last_person_count = len(active_ids)

    return active_ids


def update_identity_memory(
    engine: VisionEngine,
    session: SessionState,
    frame_bgr: np.ndarray,
    active_ids: List[int],
    ts_ms: int,
    new_logs: List[Dict[str, Any]],
) -> None:
    for tid in active_ids:
        track = session.tracks[tid]
        face_box = engine.detect_face_in_person(frame_bgr, track.bbox)
        if face_box is None:
            track.has_face = False
            continue
        track.has_face = True
        emb = engine.face_embedding(frame_bgr, face_box)
        if emb is None:
            continue
        name, score, created = match_identity(session, emb)
        if track.identity != name:
            old = track.identity
            track.identity = name
            if created:
                append_log(session, ts_ms, f"记录新人物特征: {name}", new_logs)
            elif old == "unknown":
                append_log(session, ts_ms, f"识别到已知人物: {name}", new_logs)
            else:
                append_log(session, ts_ms, f"{old} 更正为 {name}", new_logs)
        if score >= FACE_MATCH_THRESHOLD and track.identity != "unknown":
            track.identity = name


def update_actions(
    session: SessionState,
    active_ids: List[int],
    detections: List[Dict[str, Any]],
    frame_shape: Tuple[int, int, int],
    ts_ms: int,
    new_logs: List[Dict[str, Any]],
) -> None:
    h, w = frame_shape[:2]
    phone_points: List[Tuple[float, float]] = []
    eat_points: List[Tuple[float, float]] = []
    for d in detections:
        p = center(d["bbox"])
        label = str(d["label"]).lower()
        if label in PHONE_LABELS:
            phone_points.append(p)
        if label in EAT_OBJECT_LABELS:
            eat_points.append(p)

    for tid in active_ids:
        tr = session.tracks[tid]
        name = person_name(tr)
        ex = expand_box(tr.bbox, 0.12, w, h)
        phone_hit = any(inside(ex, p) for p in phone_points)

        x1, y1, x2, y2 = tr.bbox
        pw = x2 - x1
        ph = y2 - y1
        mouth_region = clamp_box(
            (x1 + int(0.25 * pw), y1 + int(0.12 * ph), x1 + int(0.75 * pw), y1 + int(0.55 * ph)),
            w,
            h,
        )
        eat_hit = any(inside(mouth_region, p) for p in eat_points)

        # Phone state.
        if phone_hit:
            tr.phone_hits += 1
            tr.phone_miss = 0
        else:
            tr.phone_miss += 1
            tr.phone_hits = 0
        if not tr.phone_active and tr.phone_hits >= PHONE_START_FRAMES:
            tr.phone_active = True
            append_log(session, ts_ms, f"{name} 开始使用手机", new_logs)
        if tr.phone_active and tr.phone_miss >= PHONE_STOP_FRAMES:
            tr.phone_active = False
            append_log(session, ts_ms, f"{name} 停止使用手机", new_logs)

        # Eating state (heuristic).
        if eat_hit:
            tr.eat_hits += 1
            tr.eat_miss = 0
        else:
            tr.eat_miss += 1
            tr.eat_hits = 0
        if not tr.eat_active and tr.eat_hits >= EAT_START_FRAMES:
            tr.eat_active = True
            append_log(session, ts_ms, f"{name} 疑似在吃东西", new_logs)
        if tr.eat_active and tr.eat_miss >= EAT_STOP_FRAMES:
            tr.eat_active = False
            append_log(session, ts_ms, f"{name} 结束进食动作", new_logs)


def update_conversations(
    session: SessionState, active_ids: List[int], ts_ms: int, new_logs: List[Dict[str, Any]]
) -> None:
    current_pairs: set[Tuple[int, int]] = set()
    tracks = session.tracks

    for i in range(len(active_ids)):
        for j in range(i + 1, len(active_ids)):
            a = tracks[active_ids[i]]
            b = tracks[active_ids[j]]
            acx, acy = center(a.bbox)
            bcx, bcy = center(b.bbox)
            dist = float(((acx - bcx) ** 2 + (acy - bcy) ** 2) ** 0.5)
            aw = a.bbox[2] - a.bbox[0]
            bw = b.bbox[2] - b.bbox[0]
            max_gap = 1.8 * float(min(aw, bw))
            y_overlap = min(a.bbox[3], b.bbox[3]) - max(a.bbox[1], b.bbox[1])
            min_h = min(a.bbox[3] - a.bbox[1], b.bbox[3] - b.bbox[1])
            overlap_ok = y_overlap > 0.3 * min_h
            face_ok = a.has_face and b.has_face
            close_ok = dist <= max_gap
            if close_ok and overlap_ok and face_ok:
                pair = tuple(sorted((a.track_id, b.track_id)))
                current_pairs.add(pair)

    for pair in current_pairs:
        hits = session.pair_hits.get(pair, 0) + 1
        session.pair_hits[pair] = hits
        session.pair_miss[pair] = 0
        if pair not in session.active_pairs and hits >= CONV_START_FRAMES:
            session.active_pairs.add(pair)
            p1 = person_name(tracks[pair[0]])
            p2 = person_name(tracks[pair[1]])
            append_log(session, ts_ms, f"{p1} 与 {p2} 开始面对面交流", new_logs)

    for pair in list(session.active_pairs):
        if pair in current_pairs:
            continue
        miss = session.pair_miss.get(pair, 0) + 1
        session.pair_miss[pair] = miss
        if miss >= CONV_STOP_FRAMES:
            p1 = person_name(tracks[pair[0]]) if pair[0] in tracks else f"track_{pair[0]}"
            p2 = person_name(tracks[pair[1]]) if pair[1] in tracks else f"track_{pair[1]}"
            append_log(session, ts_ms, f"{p1} 与 {p2} 面对面交流结束", new_logs)
            session.active_pairs.discard(pair)
            session.pair_hits[pair] = 0
            session.pair_miss[pair] = 0


def analyze_frame(
    engine: VisionEngine, session: SessionState, frame_bgr: np.ndarray, ts_ms: int, change_score: float
) -> Dict[str, Any]:
    started = time.time()
    new_logs: List[Dict[str, Any]] = []

    detections = engine.run_yolo_detections(frame_bgr)
    ocr_texts = engine.run_ocr(frame_bgr)
    person_boxes = [d["bbox"] for d in detections if str(d["label"]).lower() == "person"]

    active_ids = update_tracks(session, person_boxes, ts_ms, new_logs)
    update_identity_memory(engine, session, frame_bgr, active_ids, ts_ms, new_logs)
    update_actions(session, active_ids, detections, frame_bgr.shape, ts_ms, new_logs)
    update_conversations(session, active_ids, ts_ms, new_logs)

    # Slow path only for clear scene changes or when key events happened.
    is_keyframe = (
        (change_score >= VLM_KEYFRAME_THRESHOLD or len(new_logs) > 0)
        and (ts_ms - session.last_vlm_ts_ms >= VLM_COOLDOWN_MS)
    )
    slow_summary = ""
    if is_keyframe and engine.vlm_model is not None and engine.vlm_processor is not None:
        slow_summary = engine.run_vlm(frame_bgr)
        session.last_vlm_ts_ms = ts_ms
        if slow_summary and slow_summary != session.last_vlm_summary:
            session.last_vlm_summary = slow_summary
            if len(new_logs) == 0:
                append_log(session, ts_ms, f"场景变化: {slow_summary}", new_logs)

    visible_people = [
        {
            "track_id": t.track_id,
            "name": person_name(t),
            "bbox": list(t.bbox),
            "phone_active": t.phone_active,
            "eat_active": t.eat_active,
        }
        for tid, t in sorted(session.tracks.items(), key=lambda kv: kv[0])
        if tid in active_ids
    ]

    latency_ms = int((time.time() - started) * 1000)
    return {
        "event": "analysis",
        "ts_ms": ts_ms,
        "change_score": round(change_score, 4),
        "is_keyframe": is_keyframe,
        "people_count": len(active_ids),
        "visible_people": visible_people,
        "ocr_texts": ocr_texts[:8],
        "slow_summary": slow_summary,
        "new_logs": new_logs,
        "latency_ms": latency_ms,
    }


engine: Optional[VisionEngine] = None
engine_error = ""
app = FastAPI(title="Video Recognition Web Test")


@app.on_event("startup")
async def on_startup() -> None:
    global engine, engine_error
    try:
        engine = VisionEngine()
    except Exception as exc:  # pragma: no cover
        engine_error = f"{type(exc).__name__}: {exc}"
        logger.exception("failed to init vision engine")


@app.get("/healthz")
async def healthz():
    if engine is None:
        return JSONResponse({"status": "error", "error": engine_error}, status_code=500)
    return JSONResponse(engine.health())


@app.websocket("/ws/vision")
async def ws_vision(websocket: WebSocket):
    await websocket.accept()
    if engine is None:
        await websocket.send_json({"event": "error", "error": f"engine_not_ready: {engine_error}"})
        await websocket.close()
        return

    session = SessionState(session_id=f"s-{int(time.time() * 1000)}")
    await websocket.send_json(
        {
            "event": "status",
            "session_id": session.session_id,
            "message": "connected",
            "device": engine.device,
            "vlm_model": VLM_MODEL,
            "vlm_ready": engine.vlm_model is not None,
            "face_memory_ready": engine.face_embedder is not None,
        }
    )

    try:
        while True:
            try:
                raw = await websocket.receive_text()
                payload = json_or_none(raw)
                if not payload:
                    continue
                if payload.get("event") == "ping":
                    await websocket.send_json({"event": "pong", "ts_ms": int(time.time() * 1000)})
                    continue
                if payload.get("type") != "frame":
                    continue

                data_url = str(payload.get("image", "") or "")
                ts_ms = int(payload.get("ts_ms", time.time() * 1000))
                if not data_url:
                    continue

                frame_bgr = VisionEngine.decode_data_url_image(data_url)
                score, small_gray = VisionEngine.change_score(session.prev_small_gray, frame_bgr)
                session.prev_small_gray = small_gray

                result = await asyncio.to_thread(analyze_frame, engine, session, frame_bgr, ts_ms, score)
                result["session_id"] = session.session_id
                await websocket.send_json(result)
            except WebSocketDisconnect:
                raise
            except Exception as inner_exc:
                # Keep websocket alive even if a single frame fails.
                await websocket.send_json({"event": "error", "error": f"frame_error: {type(inner_exc).__name__}: {inner_exc}"})
                continue
    except WebSocketDisconnect:
        return
    except Exception as exc:
        await websocket.send_json({"event": "error", "error": f"{type(exc).__name__}: {exc}"})


if os.path.isdir(WEB_DIR):
    app.mount("/", StaticFiles(directory=WEB_DIR, html=True), name="web")
