from __future__ import annotations

import base64
import json
import os
import sqlite3
import threading
import time
import uuid
from dataclasses import dataclass
from pathlib import Path
from typing import Any, Optional

import cv2
import numpy as np
from fastapi import FastAPI, HTTPException
from fastapi.responses import FileResponse
from fastapi.staticfiles import StaticFiles
from pydantic import BaseModel, Field
from ultralytics import YOLO


APP_DIR = Path(__file__).resolve().parent
ROOT_DIR = APP_DIR.parent
WEB_DIR = ROOT_DIR / "web"
DB_PATH = Path(os.getenv("DB_PATH", str(ROOT_DIR / "data" / "identity.db")))
PERSON_MODEL_PATH = os.getenv("PERSON_MODEL_PATH", str(ROOT_DIR / "models" / "yolov8n.pt"))

MAX_PEOPLE = int(os.getenv("MAX_PEOPLE", "5"))
PERSON_CONF = float(os.getenv("PERSON_CONF", "0.35"))
PERSON_IOU = float(os.getenv("PERSON_IOU", "0.45"))
FACE_SIM_THRESHOLD = float(os.getenv("FACE_SIM_THRESHOLD", "0.62"))
BODY_SIM_THRESHOLD = float(os.getenv("BODY_SIM_THRESHOLD", "0.72"))
OCCLUSION_HOLD_MS = int(os.getenv("OCCLUSION_HOLD_MS", "1500"))
TRACK_TTL_MS = int(os.getenv("TRACK_TTL_MS", "2200"))
ALLOW_FALLBACK = os.getenv("ALLOW_FALLBACK", "0").strip().lower() in {"1", "true", "yes", "on"}


def now_ms() -> int:
    return int(time.time() * 1000)


def normalize(vec: np.ndarray) -> np.ndarray:
    norm = float(np.linalg.norm(vec))
    if norm < 1e-8:
        return vec
    return vec / norm


def clamp_box(box: tuple[int, int, int, int], width: int, height: int) -> tuple[int, int, int, int]:
    x1, y1, x2, y2 = box
    x1 = max(0, min(int(x1), width - 1))
    y1 = max(0, min(int(y1), height - 1))
    x2 = max(0, min(int(x2), width - 1))
    y2 = max(0, min(int(y2), height - 1))
    if x2 <= x1:
        x2 = min(width - 1, x1 + 1)
    if y2 <= y1:
        y2 = min(height - 1, y1 + 1)
    return x1, y1, x2, y2


@dataclass
class MatchResult:
    person_id: Optional[str]
    score: float


@dataclass
class PersonDetection:
    track_id: int
    bbox: tuple[int, int, int, int]
    conf: float


@dataclass
class TrackState:
    track_id: int
    bbox: tuple[int, int, int, int]
    last_seen_ms: int
    person_id: Optional[str] = None
    display_name: Optional[str] = None
    hold_until_ms: int = 0
    last_face_vector: Optional[np.ndarray] = None
    last_body_vector: Optional[np.ndarray] = None


@dataclass
class PersonProfile:
    person_id: str
    display_name: Optional[str]
    relation_desc: Optional[str]


class IdentityStore:
    def __init__(self, db_path: Path) -> None:
        self.db_path = db_path
        self._lock = threading.Lock()
        self._people: dict[str, PersonProfile] = {}
        self._face_person_ids: list[str] = []
        self._body_person_ids: list[str] = []
        self._face_matrix: Optional[np.ndarray] = None
        self._body_matrix: Optional[np.ndarray] = None
        self._init_db()
        self._refresh_cache()

    def _connect(self) -> sqlite3.Connection:
        conn = sqlite3.connect(self.db_path)
        conn.row_factory = sqlite3.Row
        return conn

    def _init_db(self) -> None:
        self.db_path.parent.mkdir(parents=True, exist_ok=True)
        with self._connect() as conn:
            conn.execute(
                """
                CREATE TABLE IF NOT EXISTS persons (
                    person_id TEXT PRIMARY KEY,
                    display_name TEXT,
                    relation_desc TEXT,
                    created_at INTEGER NOT NULL,
                    updated_at INTEGER NOT NULL
                )
                """
            )
            conn.execute(
                """
                CREATE TABLE IF NOT EXISTS templates (
                    person_id TEXT NOT NULL,
                    modality TEXT NOT NULL,
                    vector_json TEXT NOT NULL,
                    sample_count INTEGER NOT NULL,
                    updated_at INTEGER NOT NULL,
                    PRIMARY KEY (person_id, modality),
                    FOREIGN KEY (person_id) REFERENCES persons(person_id)
                )
                """
            )
            self._migrate_persons_schema(conn)
            conn.commit()

    @staticmethod
    def _normalize_optional_text(value: Optional[str]) -> Optional[str]:
        if value is None:
            return None
        text = str(value).strip()
        return text if text else None

    def _migrate_persons_schema(self, conn: sqlite3.Connection) -> None:
        cols = conn.execute("PRAGMA table_info(persons)").fetchall()
        if not cols:
            return
        col_names = {str(c["name"]) for c in cols}
        if "relation_desc" not in col_names:
            conn.execute("ALTER TABLE persons ADD COLUMN relation_desc TEXT")

        # Legacy schema used NOT NULL display_name; migrate to nullable.
        display_name_notnull = any(
            str(c["name"]) == "display_name" and int(c["notnull"]) == 1 for c in cols
        )
        if not display_name_notnull:
            return

        conn.execute("PRAGMA foreign_keys = OFF")
        conn.execute(
            """
            CREATE TABLE IF NOT EXISTS persons_new (
                person_id TEXT PRIMARY KEY,
                display_name TEXT,
                relation_desc TEXT,
                created_at INTEGER NOT NULL,
                updated_at INTEGER NOT NULL
            )
            """
        )
        conn.execute(
            """
            INSERT INTO persons_new(person_id, display_name, relation_desc, created_at, updated_at)
            SELECT person_id, NULLIF(display_name, ''), relation_desc, created_at, updated_at
            FROM persons
            """
        )
        conn.execute("DROP TABLE persons")
        conn.execute("ALTER TABLE persons_new RENAME TO persons")
        conn.execute("PRAGMA foreign_keys = ON")

    def _refresh_cache(self) -> None:
        with self._lock:
            with self._connect() as conn:
                people_rows = conn.execute(
                    "SELECT person_id, display_name, relation_desc FROM persons"
                ).fetchall()
                template_rows = conn.execute(
                    "SELECT person_id, modality, vector_json FROM templates"
                ).fetchall()

            people: dict[str, PersonProfile] = {}
            for row in people_rows:
                person_id = str(row["person_id"])
                people[person_id] = PersonProfile(
                    person_id=person_id,
                    display_name=self._normalize_optional_text(row["display_name"]),
                    relation_desc=self._normalize_optional_text(row["relation_desc"]),
                )
            self._people = people
            face_ids: list[str] = []
            body_ids: list[str] = []
            face_vecs: list[np.ndarray] = []
            body_vecs: list[np.ndarray] = []

            for row in template_rows:
                modality = str(row["modality"])
                person_id = str(row["person_id"])
                vector = np.asarray(json.loads(str(row["vector_json"])), dtype=np.float32)
                vector = normalize(vector)
                if modality == "face":
                    face_ids.append(person_id)
                    face_vecs.append(vector)
                elif modality == "body":
                    body_ids.append(person_id)
                    body_vecs.append(vector)

            self._face_person_ids = face_ids
            self._body_person_ids = body_ids
            self._face_matrix = np.vstack(face_vecs) if face_vecs else None
            self._body_matrix = np.vstack(body_vecs) if body_vecs else None

    def list_people(self) -> list[dict[str, Any]]:
        with self._lock:
            rows = [
                {
                    "person_id": p.person_id,
                    "display_name": p.display_name,
                    "relation_desc": p.relation_desc,
                    "unknown": not bool(p.display_name),
                }
                for p in self._people.values()
            ]
        rows.sort(key=lambda r: ((r["display_name"] or "~"), r["person_id"]))
        return rows

    def get_display_name(self, person_id: str) -> Optional[str]:
        with self._lock:
            profile = self._people.get(person_id)
        return profile.display_name if profile else None

    def get_profile(self, person_id: str) -> Optional[PersonProfile]:
        with self._lock:
            profile = self._people.get(person_id)
        return profile

    def create_person(
        self, display_name: Optional[str] = None, relation_desc: Optional[str] = None
    ) -> str:
        person_id = f"p_{uuid.uuid4().hex[:10]}"
        ts = now_ms()
        display_name_v = self._normalize_optional_text(display_name)
        relation_v = self._normalize_optional_text(relation_desc)
        with self._connect() as conn:
            conn.execute(
                "INSERT INTO persons(person_id, display_name, relation_desc, created_at, updated_at) VALUES (?, ?, ?, ?, ?)",
                (person_id, display_name_v, relation_v, ts, ts),
            )
            conn.commit()
        self._refresh_cache()
        return person_id

    def update_person_profile(
        self, person_id: str, display_name: Optional[str], relation_desc: Optional[str]
    ) -> None:
        ts = now_ms()
        display_name_v = self._normalize_optional_text(display_name)
        relation_v = self._normalize_optional_text(relation_desc)
        with self._connect() as conn:
            row = conn.execute("SELECT person_id FROM persons WHERE person_id = ?", (person_id,)).fetchone()
            if row is None:
                raise KeyError(f"person not found: {person_id}")
            conn.execute(
                "UPDATE persons SET display_name = ?, relation_desc = ?, updated_at = ? WHERE person_id = ?",
                (display_name_v, relation_v, ts, person_id),
            )
            conn.commit()
        self._refresh_cache()

    def upsert_template(self, person_id: str, modality: str, vector: np.ndarray) -> None:
        if modality not in {"face", "body"}:
            raise ValueError("invalid modality")
        vector = normalize(vector.astype(np.float32))
        vector_json = json.dumps(vector.tolist(), ensure_ascii=False)
        ts = now_ms()

        with self._connect() as conn:
            row = conn.execute(
                "SELECT vector_json, sample_count FROM templates WHERE person_id = ? AND modality = ?",
                (person_id, modality),
            ).fetchone()
            if row is None:
                conn.execute(
                    "INSERT INTO templates(person_id, modality, vector_json, sample_count, updated_at) VALUES (?, ?, ?, ?, ?)",
                    (person_id, modality, vector_json, 1, ts),
                )
            else:
                old_vec = np.asarray(json.loads(str(row["vector_json"])), dtype=np.float32)
                count = int(row["sample_count"])
                merged = normalize((old_vec * count + vector) / float(count + 1))
                conn.execute(
                    "UPDATE templates SET vector_json = ?, sample_count = ?, updated_at = ? WHERE person_id = ? AND modality = ?",
                    (json.dumps(merged.tolist(), ensure_ascii=False), count + 1, ts, person_id, modality),
                )
            conn.commit()

        self._refresh_cache()

    def match(self, modality: str, vector: Optional[np.ndarray]) -> MatchResult:
        if vector is None:
            return MatchResult(person_id=None, score=0.0)

        with self._lock:
            if modality == "face":
                matrix = self._face_matrix
                ids = self._face_person_ids
            elif modality == "body":
                matrix = self._body_matrix
                ids = self._body_person_ids
            else:
                return MatchResult(person_id=None, score=0.0)

            if matrix is None or len(ids) == 0:
                return MatchResult(person_id=None, score=0.0)

            scores = matrix @ vector
            idx = int(np.argmax(scores))
            return MatchResult(person_id=ids[idx], score=float(scores[idx]))


class FaceRecognizer:
    def __init__(self) -> None:
        self.mode = "fallback"
        self.face_model = None
        self.cascade = cv2.CascadeClassifier(cv2.data.haarcascades + "haarcascade_frontalface_default.xml")
        self._init_insightface()

    def _init_insightface(self) -> None:
        try:
            from insightface.app import FaceAnalysis

            model = FaceAnalysis(name=os.getenv("INSIGHTFACE_MODEL", "buffalo_l"), providers=["CPUExecutionProvider"])
            model.prepare(ctx_id=-1, det_size=(640, 640))
            self.face_model = model
            self.mode = "insightface"
        except Exception as exc:
            self.face_model = None
            if ALLOW_FALLBACK:
                self.mode = "fallback"
                return
            raise RuntimeError(
                "InsightFace initialization failed and fallback is disabled. "
                "Set ALLOW_FALLBACK=1 to enable fallback mode."
            ) from exc

    def _fallback_embedding(self, crop_bgr: np.ndarray) -> Optional[np.ndarray]:
        if crop_bgr.size == 0:
            return None
        gray = cv2.cvtColor(crop_bgr, cv2.COLOR_BGR2GRAY)
        gray = cv2.resize(gray, (96, 96), interpolation=cv2.INTER_AREA)
        gray_f = np.float32(gray) / 255.0
        dct = cv2.dct(gray_f)[:16, :16].flatten()
        hist = cv2.calcHist([gray], [0], None, [64], [0, 256]).flatten().astype(np.float32)
        feat = np.concatenate([dct.astype(np.float32), hist], axis=0)
        return normalize(feat)

    def detect_and_embed(self, frame_bgr: np.ndarray) -> list[dict[str, Any]]:
        if self.mode == "insightface" and self.face_model is not None:
            out: list[dict[str, Any]] = []
            faces = self.face_model.get(frame_bgr)
            for face in faces:
                if getattr(face, "normed_embedding", None) is None:
                    continue
                x1, y1, x2, y2 = [int(v) for v in face.bbox]
                emb = normalize(np.asarray(face.normed_embedding, dtype=np.float32))
                out.append(
                    {
                        "bbox": (x1, y1, x2, y2),
                        "embedding": emb,
                        "det_score": float(getattr(face, "det_score", 0.0)),
                    }
                )
            return out

        gray = cv2.cvtColor(frame_bgr, cv2.COLOR_BGR2GRAY)
        faces = self.cascade.detectMultiScale(
            gray,
            scaleFactor=1.1,
            minNeighbors=5,
            minSize=(36, 36),
            flags=cv2.CASCADE_SCALE_IMAGE,
        )
        out = []
        for x, y, w, h in faces:
            x1, y1, x2, y2 = int(x), int(y), int(x + w), int(y + h)
            crop = frame_bgr[y1:y2, x1:x2]
            emb = self._fallback_embedding(crop)
            if emb is None:
                continue
            out.append({"bbox": (x1, y1, x2, y2), "embedding": emb, "det_score": 0.5})
        return out


class BodyEmbedder:
    def __init__(self) -> None:
        self.mode = "hist"
        self.model = None
        self.device = "cpu"
        self.transform = None
        self._init_resnet()

    def _init_resnet(self) -> None:
        try:
            import torch
            from torchvision import models, transforms

            model = models.resnet18(weights=models.ResNet18_Weights.DEFAULT)
            model = torch.nn.Sequential(*list(model.children())[:-1])
            model.eval()
            self.model = model
            self.transform = transforms.Compose(
                [
                    transforms.ToPILImage(),
                    transforms.Resize((224, 224)),
                    transforms.ToTensor(),
                    transforms.Normalize([0.485, 0.456, 0.406], [0.229, 0.224, 0.225]),
                ]
            )
            self.mode = "resnet18"
        except Exception:
            self.mode = "hist"
            self.model = None
            self.transform = None

    def _hist_embedding(self, crop_bgr: np.ndarray) -> Optional[np.ndarray]:
        if crop_bgr.size == 0:
            return None
        hsv = cv2.cvtColor(crop_bgr, cv2.COLOR_BGR2HSV)
        hist = cv2.calcHist([hsv], [0, 1, 2], None, [8, 8, 8], [0, 180, 0, 256, 0, 256]).flatten()
        hist = hist.astype(np.float32)
        return normalize(hist)

    def embed(self, crop_bgr: np.ndarray) -> Optional[np.ndarray]:
        if crop_bgr.size == 0:
            return None

        if self.mode == "resnet18" and self.model is not None and self.transform is not None:
            try:
                import torch

                rgb = cv2.cvtColor(crop_bgr, cv2.COLOR_BGR2RGB)
                tensor = self.transform(rgb).unsqueeze(0)
                with torch.no_grad():
                    vec = self.model(tensor).flatten().cpu().numpy().astype(np.float32)
                return normalize(vec)
            except Exception:
                return self._hist_embedding(crop_bgr)

        return self._hist_embedding(crop_bgr)


class IdentityEngine:
    def __init__(self, store: IdentityStore) -> None:
        self.store = store
        self.face = FaceRecognizer()
        self.body = BodyEmbedder()
        self.person_model = YOLO(PERSON_MODEL_PATH)
        self._lock = threading.Lock()
        self.tracks: dict[int, TrackState] = {}
        self._local_track_id = 1_000_000

    def health(self) -> dict[str, Any]:
        return {
            "status": "ok",
            "person_model": PERSON_MODEL_PATH,
            "face_mode": self.face.mode,
            "body_mode": self.body.mode,
            "max_people": MAX_PEOPLE,
            "track_count": len(self.tracks),
        }

    def _next_local_track_id(self) -> int:
        self._local_track_id += 1
        return self._local_track_id

    def _decode_data_url(self, data_url: str) -> np.ndarray:
        if "," not in data_url:
            raise ValueError("invalid image payload")
        _, raw = data_url.split(",", 1)
        arr = np.frombuffer(base64.b64decode(raw), dtype=np.uint8)
        frame = cv2.imdecode(arr, cv2.IMREAD_COLOR)
        if frame is None:
            raise ValueError("decode failed")
        return frame

    def _detect_people(self, frame_bgr: np.ndarray) -> list[PersonDetection]:
        h, w = frame_bgr.shape[:2]
        results = self.person_model.track(
            frame_bgr,
            persist=True,
            classes=[0],
            conf=PERSON_CONF,
            iou=PERSON_IOU,
            verbose=False,
            tracker="bytetrack.yaml",
        )
        output: list[PersonDetection] = []
        for res in results:
            boxes = getattr(res, "boxes", None)
            if boxes is None:
                continue
            for box in boxes:
                conf = float(box.conf.item()) if box.conf is not None else 0.0
                x1, y1, x2, y2 = box.xyxy[0].tolist()
                bbox = clamp_box((int(x1), int(y1), int(x2), int(y2)), w, h)
                tid = None
                if box.id is not None:
                    tid = int(box.id.item())
                if tid is None:
                    tid = self._next_local_track_id()
                output.append(PersonDetection(track_id=tid, bbox=bbox, conf=conf))

        output.sort(key=lambda d: d.conf, reverse=True)
        return output[:MAX_PEOPLE]

    @staticmethod
    def _face_to_track(
        face_box: tuple[int, int, int, int], detections: list[PersonDetection]
    ) -> Optional[int]:
        fx1, fy1, fx2, fy2 = face_box
        cx = (fx1 + fx2) // 2
        cy = (fy1 + fy2) // 2
        candidate: Optional[int] = None
        candidate_area: Optional[int] = None
        for det in detections:
            x1, y1, x2, y2 = det.bbox
            if x1 <= cx <= x2 and y1 <= cy <= y2:
                area = (x2 - x1) * (y2 - y1)
                # Overlap scenes: prefer the tightest person box that contains face center.
                if candidate_area is None or area < candidate_area:
                    candidate_area = area
                    candidate = det.track_id
        return candidate

    def _cleanup_stale_tracks(self, ts_ms: int) -> None:
        stale = [
            tid
            for tid, state in self.tracks.items()
            if ts_ms - state.last_seen_ms > TRACK_TTL_MS
        ]
        for tid in stale:
            self.tracks.pop(tid, None)

    def process_frame(self, data_url: str) -> dict[str, Any]:
        with self._lock:
            started = now_ms()
            frame = self._decode_data_url(data_url)
            h, w = frame.shape[:2]
            detections = self._detect_people(frame)
            faces = self.face.detect_and_embed(frame)

            face_map: dict[int, dict[str, Any]] = {}
            for face in faces:
                tid = self._face_to_track(face["bbox"], detections)
                if tid is None:
                    continue
                old = face_map.get(tid)
                if old is None or float(face.get("det_score", 0.0)) > float(old.get("det_score", 0.0)):
                    face_map[tid] = face

            ts = now_ms()
            payload: list[dict[str, Any]] = []
            assigned_person_ids: set[str] = set()

            for det in detections:
                track = self.tracks.get(det.track_id)
                if track is None:
                    track = TrackState(
                        track_id=det.track_id,
                        bbox=det.bbox,
                        last_seen_ms=ts,
                        person_id=None,
                        display_name=None,
                    )

                track.bbox = det.bbox
                track.last_seen_ms = ts

                x1, y1, x2, y2 = det.bbox
                person_crop = frame[y1:y2, x1:x2]
                body_vec = self.body.embed(person_crop)
                face_info = face_map.get(det.track_id)
                face_vec = face_info["embedding"] if face_info is not None else None

                face_match = self.store.match("face", face_vec)
                body_match = self.store.match("body", body_vec)

                person_id: Optional[str] = None
                display_name: Optional[str] = None
                relation_desc: Optional[str] = None
                source = "unknown"

                if face_match.person_id and face_match.score >= FACE_SIM_THRESHOLD:
                    person_id = face_match.person_id
                    source = "face"
                elif track.person_id and ts <= track.hold_until_ms:
                    person_id = track.person_id
                    source = "hold"
                elif body_match.person_id and body_match.score >= BODY_SIM_THRESHOLD:
                    person_id = body_match.person_id
                    source = "body"

                if person_id is None:
                    if track.person_id:
                        person_id = track.person_id
                        source = "track_cache"
                    else:
                        person_id = self.store.create_person()
                        source = "new_unknown"

                # Ensure one person_id is used by at most one visible track in one frame.
                if person_id is not None and person_id in assigned_person_ids:
                    if track.person_id and track.person_id not in assigned_person_ids:
                        person_id = track.person_id
                        source = "track_cache"
                    else:
                        person_id = self.store.create_person()
                        source = "new_unknown"

                profile = self.store.get_profile(person_id)
                if profile is None:
                    person_id = self.store.create_person()
                    profile = self.store.get_profile(person_id)
                if profile is not None:
                    display_name = profile.display_name
                    relation_desc = profile.relation_desc

                track.person_id = person_id
                track.display_name = display_name
                track.hold_until_ms = ts + OCCLUSION_HOLD_MS
                if track.person_id:
                    assigned_person_ids.add(track.person_id)

                if face_vec is not None:
                    track.last_face_vector = face_vec
                if body_vec is not None:
                    track.last_body_vector = body_vec

                # Persist template once when creating a new unknown identity.
                if track.person_id and source == "new_unknown":
                    if track.last_face_vector is not None:
                        self.store.upsert_template(track.person_id, "face", track.last_face_vector)
                    if track.last_body_vector is not None:
                        self.store.upsert_template(track.person_id, "body", track.last_body_vector)

                self.tracks[det.track_id] = track
                payload.append(
                    {
                        "track_id": det.track_id,
                        "bbox": [x1, y1, x2, y2],
                        "person_id": track.person_id,
                        "display_name": display_name or f"person_{det.track_id}",
                        "relation_desc": relation_desc,
                        "source": source,
                        "scores": {
                            "face": round(face_match.score, 4),
                            "body": round(body_match.score, 4),
                        },
                        "confidence": round(max(face_match.score, body_match.score), 4),
                        "unknown": not bool(display_name),
                    }
                )

            self._cleanup_stale_tracks(ts)
            latency = now_ms() - started
            return {
                "timestamp": ts,
                "frame_size": {"width": w, "height": h},
                "count": len(payload),
                "max_people": MAX_PEOPLE,
                "latency_ms": latency,
                "detections": payload,
            }

    def name_track(
        self, track_id: int, display_name: Optional[str], relation_desc: Optional[str]
    ) -> dict[str, Any]:
        with self._lock:
            track = self.tracks.get(track_id)
            if track is None:
                raise KeyError("track not found")
            if (display_name is None or not str(display_name).strip()) and (
                relation_desc is None or not str(relation_desc).strip()
            ):
                raise ValueError("name and relation cannot both be empty")

            name = display_name.strip() if display_name is not None else None
            relation = relation_desc.strip() if relation_desc is not None else None
            if track.person_id:
                person_id = track.person_id
            else:
                person_id = self.store.create_person()
            self.store.update_person_profile(person_id, name, relation)

            if track.last_face_vector is not None:
                self.store.upsert_template(person_id, "face", track.last_face_vector)
            if track.last_body_vector is not None:
                self.store.upsert_template(person_id, "body", track.last_body_vector)

            profile = self.store.get_profile(person_id)
            track.person_id = person_id
            track.display_name = profile.display_name if profile else name
            track.hold_until_ms = now_ms() + OCCLUSION_HOLD_MS
            self.tracks[track_id] = track

            return {
                "track_id": track_id,
                "person_id": person_id,
                "display_name": profile.display_name if profile else None,
                "relation_desc": profile.relation_desc if profile else None,
            }


class FrameRequest(BaseModel):
    image: str = Field(..., description="data:image/jpeg;base64,...")


class NameRequest(BaseModel):
    track_id: int
    name: Optional[str] = None
    relation: Optional[str] = None


store = IdentityStore(DB_PATH)
engine = IdentityEngine(store)
app = FastAPI(title="Identity Service", version="0.1.0")
app.mount("/static", StaticFiles(directory=str(WEB_DIR)), name="static")


@app.get("/healthz")
def healthz() -> dict[str, Any]:
    return {
        "db_path": str(DB_PATH),
        "people": len(store.list_people()),
        "engine": engine.health(),
    }


@app.get("/")
def index() -> FileResponse:
    return FileResponse(str(WEB_DIR / "index.html"))


@app.post("/api/frame")
def api_frame(payload: FrameRequest) -> dict[str, Any]:
    try:
        return engine.process_frame(payload.image)
    except ValueError as exc:
        raise HTTPException(status_code=400, detail=str(exc)) from exc


@app.post("/api/name")
def api_name(payload: NameRequest) -> dict[str, Any]:
    try:
        return engine.name_track(payload.track_id, payload.name, payload.relation)
    except KeyError as exc:
        raise HTTPException(status_code=404, detail=str(exc)) from exc
    except ValueError as exc:
        raise HTTPException(status_code=400, detail=str(exc)) from exc


@app.get("/api/people")
def api_people() -> dict[str, Any]:
    return {"people": store.list_people()}
