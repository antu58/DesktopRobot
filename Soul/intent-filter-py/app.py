import logging
import os
import re
import time
import unicodedata
import uuid
from datetime import datetime, timedelta
from typing import Any, Literal
from zoneinfo import ZoneInfo

from fastapi import FastAPI
from pydantic import BaseModel, Field
from rapidfuzz import fuzz

logger = logging.getLogger("intent-filter")
logging.basicConfig(level="INFO")

DEFAULT_LOCALE = os.getenv("INTENT_FILTER_DEFAULT_LOCALE", "zh-CN")
DEFAULT_TIMEZONE = os.getenv("INTENT_FILTER_DEFAULT_TIMEZONE", "Asia/Shanghai")

SUPPORTED_LOCALES = {"zh-CN", "zh-TW", "en-US", "ko-KR", "ja-JP"}
SYSTEM_INTENT_FALLBACK = "sys.fallback_reasoning"
SYSTEM_INTENT_NO_ACTION = "sys.no_action"

TRADITIONAL_HINT_CHARS = set("臺萬與為這麼鐘點開關燈臥廳鬧鐘後嗎讓給備註記憶")

LANGUAGE_PROFILES: dict[str, dict[str, Any]] = {
    "zh-CN": {
        "polite_prefixes": ["请问", "请你", "请帮我", "帮我", "麻烦你", "麻烦", "可以帮我", "能不能", "给我", "帮忙"],
        "connectors": ["，", ",", "并且", "然后", "再", "同时", "并", "而且", "接着", "顺便"],
        "action_aliases": {
            "打开": "open",
            "开启": "open",
            "开": "open",
            "关掉": "close",
            "关闭": "close",
            "关": "close",
            "拉开": "open",
            "拉上": "close",
            "设置": "set",
            "设": "set",
            "定": "set",
            "提醒": "remind",
            "记": "memo",
            "记录": "memo",
        },
        "device_aliases": {
            "窗帘": "curtain",
            "台灯": "lamp",
            "灯": "light",
            "闹钟": "alarm",
            "提醒": "reminder",
            "备忘录": "memo",
            "备忘": "memo",
        },
        "room_aliases": {
            "卧室": "bedroom",
            "客厅": "living_room",
            "厨房": "kitchen",
            "书房": "study_room",
            "卫生间": "bathroom",
            "洗手间": "bathroom",
        },
        "no_action_patterns": [
            r"^(啊+|哦+|嗯+|唉+|哎+)$",
            r"^吓我一跳$",
            r"^吓死我(了)?$",
            r"^笑死我了?$",
            r"^无语$",
            r"^太离谱了?$",
            r"^真离谱$",
            r"^服了$",
            r"^好烦(啊)?$",
            r"^我(真)?(好)?(烦|累|困|难受)(死了)?$",
        ],
        "question_pattern": r"(怎么|如何|为什么|咋|吗|么|？|\?)",
    },
    "zh-TW": {
        "polite_prefixes": ["請問", "請你", "請幫我", "幫我", "麻煩你", "麻煩", "可以幫我", "能不能", "給我", "幫忙"],
        "connectors": ["，", ",", "並且", "然後", "再", "同時", "並", "而且", "接著", "順便"],
        "action_aliases": {
            "打開": "open",
            "開啟": "open",
            "開": "open",
            "關掉": "close",
            "關閉": "close",
            "關": "close",
            "拉開": "open",
            "拉上": "close",
            "設定": "set",
            "設": "set",
            "定": "set",
            "提醒": "remind",
            "記": "memo",
            "記錄": "memo",
        },
        "device_aliases": {
            "窗簾": "curtain",
            "台燈": "lamp",
            "檯燈": "lamp",
            "燈": "light",
            "鬧鐘": "alarm",
            "提醒": "reminder",
            "備忘錄": "memo",
            "備忘": "memo",
        },
        "room_aliases": {
            "臥室": "bedroom",
            "客廳": "living_room",
            "廚房": "kitchen",
            "書房": "study_room",
            "衛生間": "bathroom",
            "洗手間": "bathroom",
        },
        "no_action_patterns": [
            r"^(啊+|哦+|嗯+|唉+|哎+)$",
            r"^嚇我一跳$",
            r"^嚇死我(了)?$",
            r"^笑死我了?$",
            r"^無語$",
            r"^太離譜了?$",
            r"^真離譜$",
            r"^服了$",
            r"^好煩(啊)?$",
            r"^我(真)?(好)?(煩|累|睏|難受)(死了)?$",
        ],
        "question_pattern": r"(怎麼|如何|為什麼|嗎|麼|？|\?)",
    },
    "en-US": {
        "polite_prefixes": [
            "please",
            "can you",
            "could you",
            "would you",
            "can u",
            "help me",
            "would you please",
        ],
        "connectors": [",", ";", " and then ", " then ", " and ", " also ", " plus "],
        "action_aliases": {
            "turn on": "open",
            "switch on": "open",
            "open": "open",
            "turn off": "close",
            "switch off": "close",
            "close": "close",
            "set": "set",
            "create": "set",
            "remind": "remind",
            "remember": "memo",
            "note": "memo",
        },
        "device_aliases": {
            "light": "light",
            "lamp": "lamp",
            "curtain": "curtain",
            "blinds": "curtain",
            "alarm": "alarm",
            "reminder": "reminder",
            "memo": "memo",
            "note": "memo",
        },
        "room_aliases": {
            "bedroom": "bedroom",
            "living room": "living_room",
            "kitchen": "kitchen",
            "study": "study_room",
            "bathroom": "bathroom",
            "restroom": "bathroom",
        },
        "no_action_patterns": [
            r"^(wow|omg|ugh|huh)$",
            r"^(that\s+)?scared\s+me$",
            r"^that\s+was\s+close$",
            r"^so\s+annoying$",
            r"^i\s*(am|'m)\s*(so\s*)?(tired|upset|annoyed)$",
        ],
        "question_pattern": r"(\?|how|what|why|when|where|who|can\s+you|could\s+you|would\s+you)",
    },
    "ko-KR": {
        "polite_prefixes": ["부탁해", "부탁해요", "도와줘", "도와주세요", "해줘", "좀"],
        "connectors": [",", "，", " 그리고 ", " 그다음 ", " 다음에 ", " 또한 ", " 또 "],
        "action_aliases": {
            "켜줘": "open",
            "켜": "open",
            "열어": "open",
            "꺼줘": "close",
            "꺼": "close",
            "닫아": "close",
            "설정": "set",
            "맞춰": "set",
            "알려줘": "remind",
            "리마인드": "remind",
            "메모": "memo",
            "기억": "memo",
        },
        "device_aliases": {
            "불": "light",
            "조명": "light",
            "스탠드": "lamp",
            "커튼": "curtain",
            "알람": "alarm",
            "알림": "reminder",
            "메모": "memo",
        },
        "room_aliases": {
            "침실": "bedroom",
            "거실": "living_room",
            "주방": "kitchen",
            "서재": "study_room",
            "화장실": "bathroom",
        },
        "no_action_patterns": [
            r"^(아+|어+|음+|하+)$",
            r"^깜짝\s*놀랐어$",
            r"^무섭다$",
            r"^짜증나$",
            r"^피곤해$",
        ],
        "question_pattern": r"(\?|어떻게|왜|뭐|무엇|인가요|할까|할까요)",
    },
    "ja-JP": {
        "polite_prefixes": ["お願い", "お願いします", "手伝って", "ちょっと", "ねえ"],
        "connectors": ["、", ",", " そして ", " それから ", " あと ", " それと ", " さらに "],
        "action_aliases": {
            "つけて": "open",
            "オン": "open",
            "開けて": "open",
            "消して": "close",
            "オフ": "close",
            "閉めて": "close",
            "設定": "set",
            "セット": "set",
            "リマインド": "remind",
            "知らせて": "remind",
            "メモ": "memo",
            "覚えて": "memo",
        },
        "device_aliases": {
            "ライト": "light",
            "電気": "light",
            "照明": "light",
            "ランプ": "lamp",
            "カーテン": "curtain",
            "アラーム": "alarm",
            "目覚まし": "alarm",
            "リマインダー": "reminder",
            "メモ": "memo",
        },
        "room_aliases": {
            "寝室": "bedroom",
            "リビング": "living_room",
            "キッチン": "kitchen",
            "書斎": "study_room",
            "トイレ": "bathroom",
            "浴室": "bathroom",
        },
        "no_action_patterns": [
            r"^(あ+|え+|う+)$",
            r"^びっくりした$",
            r"^こわい$",
            r"^最悪$",
            r"^疲れた$",
        ],
        "question_pattern": r"(\?|？|どう|なぜ|何|ですか|ますか)",
    },
}

ZH_CONNECTOR_PATTERN = re.compile(r"(，|,|并且|並且|然后|然後|再|同时|同時|并|並|而且|接着|接著|顺便|順便)")
EN_CONNECTOR_PATTERN = re.compile(r"(,|;|\band\s+then\b|\bthen\b|\band\b|\balso\b|\bplus\b)", re.IGNORECASE)
KO_CONNECTOR_PATTERN = re.compile(r"(,|，|그리고|그다음|다음에|또한|또)")
JA_CONNECTOR_PATTERN = re.compile(r"(、|,|そして|それから|あと|それと|さらに)")

ZH_DURATION_PATTERN = re.compile(
    r"(?P<num>[0-9零一二兩两三四五六七八九十百千]+)\s*"
    r"(?P<unit>秒鐘?|秒|分鐘?|分|小时|小時|個?小時|钟头|鐘頭|天)\s*"
    r"(?P<suffix>后|後|以后|以後|之后|之後)?"
)
ZH_ABSOLUTE_TIME_PATTERN = re.compile(
    r"(?:(?P<day>今天|今日|今晚|今早|今晨|明天|明早|明晨|明晚|后天|後天)\s*)?"
    r"(?:(?P<period>凌晨|早上|上午|中午|下午|晚上|傍晚|夜里|今晚|明晚)\s*)?"
    r"(?P<hour>[0-9零一二兩两三四五六七八九十]{1,3})\s*(?:点|點|:|：)\s*(?P<minute>[0-9零一二兩两三四五六七八九十]{1,2})?\s*(?P<half>半)?"
)

EN_DURATION_PATTERNS = [
    re.compile(r"\bin\s+(?P<num>\d+)\s*(?P<unit>seconds?|minutes?|hours?|days?)\b", re.IGNORECASE),
    re.compile(r"\b(?P<num>\d+)\s*(?P<unit>seconds?|minutes?|hours?|days?)\s*(later|from\s+now)\b", re.IGNORECASE),
]
EN_ABSOLUTE_TIME_PATTERN = re.compile(
    r"\b(?:(?P<day>today|tonight|tomorrow|day\s+after\s+tomorrow)\s*)?"
    r"(?:at\s*)?"
    r"(?P<hour>\d{1,2})"
    r"(?::(?P<minute>\d{1,2}))?\s*(?P<ampm>am|pm)?\b",
    re.IGNORECASE,
)

KO_DURATION_PATTERN = re.compile(r"(?P<num>\d+)\s*(?P<unit>초|분|시간|일)\s*(뒤|후)")
KO_ABSOLUTE_TIME_PATTERN = re.compile(
    r"(?:(?P<day>오늘|내일|모레)\s*)?"
    r"(?:(?P<period>오전|오후|아침|저녁|밤)\s*)?"
    r"(?P<hour>\d{1,2})\s*시(?:\s*(?P<minute>\d{1,2})\s*분?)?"
)

JA_DURATION_PATTERN = re.compile(r"(?P<num>\d+)\s*(?P<unit>秒|分|時間|日)\s*(後|あと)")
JA_ABSOLUTE_TIME_PATTERN = re.compile(
    r"(?:(?P<day>今日|きょう|今夜|明日|あした|明後日)\s*)?"
    r"(?:(?P<period>午前|午後|朝|夜|夕方)\s*)?"
    r"(?P<hour>\d{1,2})\s*時(?:\s*(?P<minute>\d{1,2})\s*分?)?"
)


class TextSpan(BaseModel):
    text: str
    start: int = Field(ge=0)
    end: int = Field(ge=0)


class Entity(BaseModel):
    type: str
    value: str
    normalized: Any | None = None
    start: int | None = Field(default=None, ge=0)
    end: int | None = Field(default=None, ge=0)


class MatchRules(BaseModel):
    keywords_any: list[str] = Field(default_factory=list)
    keywords_all: list[str] = Field(default_factory=list)
    negative_keywords: list[str] = Field(default_factory=list)
    regex_any: list[str] = Field(default_factory=list)
    regex_all: list[str] = Field(default_factory=list)
    entity_types_any: list[str] = Field(default_factory=list)
    entity_types_all: list[str] = Field(default_factory=list)
    examples: list[str] = Field(default_factory=list)
    min_confidence: float | None = Field(default=None, ge=0.0, le=1.0)


class SlotBinding(BaseModel):
    name: str
    required: bool = False
    from_entity_types: list[str] = Field(default_factory=list)
    use_normalized_entity: bool = True
    regex: str | None = None
    regex_group: int = 1
    from_time_key: str | None = None
    time_kind: Literal["duration", "time_point"] | None = None
    default: Any | None = None


class IntentSpec(BaseModel):
    id: str
    name: str | None = None
    priority: int = 0
    hint_score: float | None = Field(default=None, ge=0.0, le=1.0)
    match: MatchRules = Field(default_factory=MatchRules)
    slots: list[SlotBinding] = Field(default_factory=list)


class FilterOptions(BaseModel):
    allow_multi_intent: bool = True
    max_intents: int = Field(default=8, ge=1, le=32)
    max_intents_per_segment: int = Field(default=2, ge=1, le=8)
    min_confidence: float = Field(default=0.35, ge=0.0, le=1.0)
    enable_time_parser: bool = True
    return_debug_candidates: bool = False
    return_debug_entities: bool = False
    emit_system_intent_when_empty: bool = True


class IntentFilterRequest(BaseModel):
    request_id: str | None = None
    command: str = Field(..., min_length=1, description="本轮命令文本")
    intent_catalog: list[IntentSpec] = Field(min_length=1)
    options: FilterOptions = Field(default_factory=FilterOptions)


class Evidence(BaseModel):
    type: str
    value: str
    score: float = Field(ge=0.0, le=1.0)


class SelectedIntent(BaseModel):
    intent_id: str
    intent_name: str
    confidence: float = Field(ge=0.0, le=1.0)
    status: Literal["ready", "need_clarification", "rejected", "system"]
    segment_index: int = Field(ge=0)
    span: TextSpan
    parameters: dict[str, Any] = Field(default_factory=dict)
    normalized: dict[str, Any] = Field(default_factory=dict)
    missing_parameters: list[str] = Field(default_factory=list)
    evidence: list[Evidence] = Field(default_factory=list)


class CandidateIntent(BaseModel):
    intent_id: str
    segment_index: int
    confidence: float = Field(ge=0.0, le=1.0)


class RoutingDecision(BaseModel):
    action: Literal["execute_intents", "fallback_reasoning", "no_action"]
    trigger_intent_id: str
    reason: str


class IntentFilterResponse(BaseModel):
    request_id: str
    intents: list[SelectedIntent]
    decision: RoutingDecision
    candidates: list[CandidateIntent] | None = None
    meta: dict[str, Any]


class Segment(BaseModel):
    index: int
    start: int
    end: int
    text: str


def _clamp(value: float, low: float = 0.0, high: float = 1.0) -> float:
    return max(low, min(high, value))


def _safe_zoneinfo(tz_name: str) -> ZoneInfo:
    try:
        return ZoneInfo(tz_name)
    except Exception:
        logger.warning("invalid timezone=%s fallback=Asia/Shanghai", tz_name)
        return ZoneInfo("Asia/Shanghai")


def _resolve_now(now: str | None, tz_name: str) -> datetime:
    tz = _safe_zoneinfo(tz_name)
    if not now:
        return datetime.now(tz)

    normalized = now.replace("Z", "+00:00")
    try:
        parsed = datetime.fromisoformat(normalized)
    except ValueError:
        logger.warning("invalid now=%s fallback=system_clock", now)
        return datetime.now(tz)

    if parsed.tzinfo is None:
        return parsed.replace(tzinfo=tz)
    return parsed.astimezone(tz)


def _to_nfkc(text: str) -> str:
    return unicodedata.normalize("NFKC", text)


def _detect_locale(text: str) -> str:
    s = _to_nfkc(text)
    if re.search(r"[\uac00-\ud7af]", s):
        return "ko-KR"
    if re.search(r"[\u3040-\u30ff]", s):
        return "ja-JP"

    latin = re.findall(r"[A-Za-z]", s)
    if latin and len(latin) / max(len(s), 1) >= 0.25:
        return "en-US"

    if any(ch in TRADITIONAL_HINT_CHARS for ch in s):
        return "zh-TW"

    return "zh-CN"


def _profile(locale: str) -> dict[str, Any]:
    if locale in SUPPORTED_LOCALES:
        return LANGUAGE_PROFILES[locale]
    return LANGUAGE_PROFILES[DEFAULT_LOCALE] if DEFAULT_LOCALE in SUPPORTED_LOCALES else LANGUAGE_PROFILES["zh-CN"]


def _normalize_command_text(text: str, locale: str) -> str:
    profile = _profile(locale)
    normalized = _to_nfkc(text).strip()
    if locale == "en-US":
        normalized = re.sub(r"\s+", " ", normalized)
    else:
        normalized = re.sub(r"\s+", "", normalized)

    if profile["polite_prefixes"]:
        prefix_pattern = r"^(?:" + "|".join(re.escape(item) for item in profile["polite_prefixes"]) + r")\s*"
        normalized = re.sub(prefix_pattern, "", normalized, flags=re.IGNORECASE)

    return normalized or _to_nfkc(text).strip()


def _iter_keyword_matches(text: str, keyword: str, locale: str):
    if locale == "en-US":
        pattern = re.compile(r"\b" + re.escape(keyword) + r"\b", re.IGNORECASE)
        return pattern.finditer(text)
    return re.finditer(re.escape(keyword), text)


def _extract_keyword_entities(text: str, entity_type: str, aliases: dict[str, str], locale: str) -> list[Entity]:
    entities: list[Entity] = []
    if not text:
        return entities

    occupied = [False] * len(text)
    for keyword in sorted(aliases.keys(), key=lambda item: (-len(item), item)):
        for match in _iter_keyword_matches(text, keyword, locale):
            start = match.start()
            end = match.end()
            if start >= end:
                continue
            if any(occupied[idx] for idx in range(start, end)):
                continue

            raw_value = text[start:end]
            entities.append(
                Entity(
                    type=entity_type,
                    value=raw_value,
                    normalized=aliases[keyword],
                    start=start,
                    end=end,
                )
            )
            for idx in range(start, end):
                occupied[idx] = True
    return entities


def _dedupe_entities(entities: list[Entity]) -> list[Entity]:
    unique: list[Entity] = []
    seen: set[tuple[str, int | None, int | None, str, str]] = set()
    for entity in entities:
        key = (entity.type, entity.start, entity.end, str(entity.normalized), entity.value)
        if key in seen:
            continue
        seen.add(key)
        unique.append(entity)
    return unique


def _extract_entities_from_text(text: str, locale: str) -> list[Entity]:
    profile = _profile(locale)
    entities: list[Entity] = []
    entities.extend(_extract_keyword_entities(text, "action", profile["action_aliases"], locale))
    entities.extend(_extract_keyword_entities(text, "device", profile["device_aliases"], locale))
    entities.extend(_extract_keyword_entities(text, "room", profile["room_aliases"], locale))

    entities = _dedupe_entities(entities)
    entities.sort(
        key=lambda item: (
            item.start if item.start is not None else 10**9,
            -((item.end - item.start) if item.start is not None and item.end is not None else 0),
            item.type,
        )
    )
    return entities


def _build_command_context(command: str) -> tuple[str, str, str, datetime, list[Entity]]:
    detected_locale = _detect_locale(command)
    locale = detected_locale if detected_locale in SUPPORTED_LOCALES else DEFAULT_LOCALE
    command_text = _normalize_command_text(command, locale)
    timezone = DEFAULT_TIMEZONE
    now = _resolve_now(None, timezone)
    entities = _extract_entities_from_text(command_text, locale)
    return command_text, timezone, locale, now, entities


def _has_overlap(entity: Entity, segment: Segment) -> bool:
    if entity.start is None or entity.end is None:
        return True
    return not (entity.end <= segment.start or entity.start >= segment.end)


def _split_segments(text: str, locale: str) -> list[Segment]:
    if locale in {"zh-CN", "zh-TW"}:
        pattern = ZH_CONNECTOR_PATTERN
    elif locale == "en-US":
        pattern = EN_CONNECTOR_PATTERN
    elif locale == "ko-KR":
        pattern = KO_CONNECTOR_PATTERN
    else:
        pattern = JA_CONNECTOR_PATTERN

    segments: list[Segment] = []
    start = 0
    idx = 0
    for match in pattern.finditer(text):
        end = match.start()
        part = text[start:end].strip()
        if part:
            left = text.find(part, start, end + 1)
            segments.append(Segment(index=idx, start=left, end=left + len(part), text=part))
            idx += 1
        start = match.end()

    tail = text[start:].strip()
    if tail:
        left = text.find(tail, start)
        segments.append(Segment(index=idx, start=left, end=left + len(tail), text=tail))

    if not segments:
        stripped = text.strip()
        segments.append(Segment(index=0, start=0, end=len(stripped), text=stripped))

    return segments


def _chinese_to_int(token: str) -> int | None:
    token = token.strip()
    if not token:
        return None
    if token.isdigit():
        return int(token)

    nums = {"零": 0, "一": 1, "二": 2, "兩": 2, "两": 2, "三": 3, "四": 4, "五": 5, "六": 6, "七": 7, "八": 8, "九": 9}
    units = {"十": 10, "百": 100, "千": 1000}

    total = 0
    current = 0
    for ch in token:
        if ch in nums:
            current = nums[ch]
        elif ch in units:
            unit = units[ch]
            if current == 0:
                current = 1
            total += current * unit
            current = 0
        else:
            return None

    total += current
    return total if total > 0 else None


def _unit_to_seconds(unit: str, num: int, locale: str) -> int | None:
    unit_lower = unit.lower()
    if locale in {"zh-CN", "zh-TW"}:
        if unit.startswith("秒"):
            return num
        if unit.startswith("分"):
            return num * 60
        if unit.startswith("小时") or unit.startswith("小時") or unit.startswith("個小時") or unit.startswith("钟头") or unit.startswith("鐘頭"):
            return num * 3600
        if unit.startswith("天"):
            return num * 86400
    elif locale == "en-US":
        if unit_lower.startswith("second"):
            return num
        if unit_lower.startswith("minute"):
            return num * 60
        if unit_lower.startswith("hour"):
            return num * 3600
        if unit_lower.startswith("day"):
            return num * 86400
    elif locale == "ko-KR":
        if unit == "초":
            return num
        if unit == "분":
            return num * 60
        if unit == "시간":
            return num * 3600
        if unit == "일":
            return num * 86400
    elif locale == "ja-JP":
        if unit == "秒":
            return num
        if unit == "分":
            return num * 60
        if unit == "時間":
            return num * 3600
        if unit == "日":
            return num * 86400
    return None


def _parse_duration_signals(text: str, now: datetime, locale: str) -> list[dict[str, Any]]:
    results: list[dict[str, Any]] = []

    if locale in {"zh-CN", "zh-TW"}:
        patterns = [ZH_DURATION_PATTERN]
    elif locale == "en-US":
        patterns = EN_DURATION_PATTERNS
    elif locale == "ko-KR":
        patterns = [KO_DURATION_PATTERN]
    else:
        patterns = [JA_DURATION_PATTERN]

    for pattern in patterns:
        for match in pattern.finditer(text):
            num_token = match.group("num")
            num = _chinese_to_int(num_token) if locale in {"zh-CN", "zh-TW"} else int(num_token)
            unit = match.group("unit")
            seconds = _unit_to_seconds(unit, num, locale)
            if seconds is None:
                continue

            signal = {
                "kind": "duration",
                "raw": match.group(0),
                "duration_seconds": seconds,
                "confidence": 0.95,
                "trigger_at": (now + timedelta(seconds=seconds)).isoformat(),
            }
            results.append(signal)

    return results


def _adjust_hour_for_period(hour: int, period: str | None, locale: str) -> int:
    if not period:
        return hour

    p = period.lower()
    if locale in {"zh-CN", "zh-TW"}:
        if p in {"下午", "晚上", "傍晚", "明晚", "今晚"} and hour < 12:
            return hour + 12
        if p == "中午" and hour < 11:
            return hour + 12
        if p == "凌晨" and hour == 12:
            return 0
        return hour

    if locale == "en-US":
        # handled by am/pm in parser
        return hour

    if locale == "ko-KR":
        if p in {"오후", "저녁", "밤"} and hour < 12:
            return hour + 12
        return hour

    if locale == "ja-JP":
        if p in {"午後", "夜", "夕方"} and hour < 12:
            return hour + 12
        return hour

    return hour


def _parse_absolute_signals(text: str, now: datetime, locale: str) -> list[dict[str, Any]]:
    results: list[dict[str, Any]] = []

    if locale in {"zh-CN", "zh-TW"}:
        pattern = ZH_ABSOLUTE_TIME_PATTERN
        day_offset_map = {
            "今天": 0,
            "今日": 0,
            "今晚": 0,
            "今早": 0,
            "今晨": 0,
            "明天": 1,
            "明早": 1,
            "明晨": 1,
            "明晚": 1,
            "后天": 2,
            "後天": 2,
        }
    elif locale == "en-US":
        pattern = EN_ABSOLUTE_TIME_PATTERN
        day_offset_map = {
            "today": 0,
            "tonight": 0,
            "tomorrow": 1,
            "day after tomorrow": 2,
        }
    elif locale == "ko-KR":
        pattern = KO_ABSOLUTE_TIME_PATTERN
        day_offset_map = {"오늘": 0, "내일": 1, "모레": 2}
    else:
        pattern = JA_ABSOLUTE_TIME_PATTERN
        day_offset_map = {"今日": 0, "きょう": 0, "今夜": 0, "明日": 1, "あした": 1, "明後日": 2}

    for match in pattern.finditer(text):
        raw = match.group(0)
        if not raw:
            continue

        day_word = match.groupdict().get("day")
        period = match.groupdict().get("period")
        hour_token = match.groupdict().get("hour")
        minute_token = match.groupdict().get("minute")

        if not hour_token:
            continue

        if locale in {"zh-CN", "zh-TW"}:
            hour_val = _chinese_to_int(hour_token)
            minute_val = _chinese_to_int(minute_token) if minute_token else 0
            half = match.groupdict().get("half")
            if half:
                minute_val = 30
        else:
            hour_val = int(hour_token)
            minute_val = int(minute_token) if minute_token else 0

        if hour_val is None or hour_val > 24 or minute_val is None or minute_val > 59:
            continue

        if locale == "en-US":
            ampm = (match.groupdict().get("ampm") or "").lower()
            if ampm == "pm" and hour_val < 12:
                hour_val += 12
            if ampm == "am" and hour_val == 12:
                hour_val = 0
        else:
            hour_val = _adjust_hour_for_period(hour_val, period, locale)

        if hour_val > 23:
            continue

        day_key = day_word.lower() if locale == "en-US" and day_word else day_word
        day_offset = day_offset_map.get(day_key, 0)
        target = now.replace(hour=hour_val, minute=minute_val, second=0, microsecond=0) + timedelta(days=day_offset)

        if day_word is None and target <= now:
            target = target + timedelta(days=1)

        results.append(
            {
                "kind": "time_point",
                "raw": raw,
                "trigger_at": target.isoformat(),
                "timezone": str(now.tzinfo),
                "confidence": 0.90,
            }
        )

    return results


def _collect_time_signals(text: str, now: datetime, locale: str) -> list[dict[str, Any]]:
    return _parse_duration_signals(text, now, locale) + _parse_absolute_signals(text, now, locale)


def _attach_time_entities(command_text: str, entities: list[Entity], time_signals: list[dict[str, Any]]) -> list[Entity]:
    scan_pos: dict[str, int] = {}
    for signal in time_signals:
        raw = str(signal.get("raw", ""))
        start = None
        end = None
        if raw:
            cursor = scan_pos.get(raw, 0)
            found = command_text.find(raw, cursor)
            if found < 0 and cursor > 0:
                found = command_text.find(raw)
            if found >= 0:
                start = found
                end = found + len(raw)
                scan_pos[raw] = end

        entities.append(
            Entity(
                type=f"time_{signal.get('kind', 'unknown')}",
                value=raw,
                normalized=signal,
                start=start,
                end=end,
            )
        )
    return _dedupe_entities(entities)


def _as_text(value: Any) -> str:
    if isinstance(value, str):
        return value
    return "" if value is None else str(value)


def _intent_score(intent: IntentSpec, segment: Segment, entities: list[Entity], locale: str) -> tuple[float, list[Evidence]]:
    text = segment.text
    evidence: list[Evidence] = []
    rules = intent.match

    segment_text_for_match = text.lower() if locale == "en-US" else text

    for kw in rules.negative_keywords:
        token = kw.lower() if locale == "en-US" else kw
        if token and token in segment_text_for_match:
            return 0.0, [Evidence(type="negative_keyword", value=kw, score=1.0)]

    score = 0.0

    if rules.keywords_any:
        hits = []
        for kw in rules.keywords_any:
            target = kw.lower() if locale == "en-US" else kw
            if target and target in segment_text_for_match:
                hits.append(kw)
        if hits:
            hit_score = len(hits) / len(rules.keywords_any)
            score += 0.38 * hit_score
            for kw in hits:
                evidence.append(Evidence(type="keyword_any", value=kw, score=_clamp(hit_score)))

    if rules.keywords_all:
        all_hit = True
        for kw in rules.keywords_all:
            target = kw.lower() if locale == "en-US" else kw
            if target and target not in segment_text_for_match:
                all_hit = False
                break
        if all_hit:
            score += 0.25
            for kw in rules.keywords_all:
                if kw:
                    evidence.append(Evidence(type="keyword_all", value=kw, score=1.0))
        else:
            return 0.0, evidence

    if rules.regex_any:
        any_hit = False
        for pattern in rules.regex_any:
            flags = re.IGNORECASE if locale == "en-US" else 0
            if pattern and re.search(pattern, text, flags=flags):
                any_hit = True
                evidence.append(Evidence(type="regex_any", value=pattern, score=1.0))
        if any_hit:
            score += 0.22

    if rules.regex_all:
        all_hit = True
        for pattern in rules.regex_all:
            flags = re.IGNORECASE if locale == "en-US" else 0
            if pattern and not re.search(pattern, text, flags=flags):
                all_hit = False
                break
        if all_hit:
            score += 0.12
            for pattern in rules.regex_all:
                if pattern:
                    evidence.append(Evidence(type="regex_all", value=pattern, score=1.0))
        else:
            return 0.0, evidence

    segment_entities = [entity for entity in entities if _has_overlap(entity, segment)]
    segment_entity_types = {entity.type for entity in segment_entities}

    if rules.entity_types_any:
        hits = [tp for tp in rules.entity_types_any if tp in segment_entity_types]
        if hits:
            hit_score = len(hits) / len(rules.entity_types_any)
            score += 0.14 * hit_score
            for tp in hits:
                evidence.append(Evidence(type="entity_any", value=tp, score=_clamp(hit_score)))

    if rules.entity_types_all:
        if all(tp in segment_entity_types for tp in rules.entity_types_all):
            score += 0.10
            for tp in rules.entity_types_all:
                evidence.append(Evidence(type="entity_all", value=tp, score=1.0))
        else:
            return 0.0, evidence

    if rules.examples:
        best = 0.0
        best_example = ""
        for sample in rules.examples:
            if not sample:
                continue
            ratio = fuzz.partial_ratio(text, sample) / 100.0
            if ratio > best:
                best = ratio
                best_example = sample
        if best > 0:
            score += 0.20 * best
            evidence.append(Evidence(type="example_similarity", value=best_example, score=_clamp(best)))

    if intent.hint_score is not None:
        score += 0.16 * _clamp(intent.hint_score)
        evidence.append(
            Evidence(type="upstream_hint_score", value=f"{intent.hint_score:.3f}", score=_clamp(intent.hint_score))
        )

    if intent.priority:
        priority_score = min(100, max(0, intent.priority)) / 100.0
        score += 0.03 * priority_score

    return _clamp(score), evidence


def _fill_parameters(
    intent: IntentSpec,
    segment: Segment,
    entities: list[Entity],
    time_signals: list[dict[str, Any]],
    now: datetime,
    locale: str,
) -> tuple[dict[str, Any], dict[str, Any], list[str]]:
    params: dict[str, Any] = {}
    normalized: dict[str, Any] = {}
    missing: list[str] = []

    segment_entities = [entity for entity in entities if _has_overlap(entity, segment)]

    for slot in intent.slots:
        value: Any | None = None

        if slot.from_time_key:
            for signal in time_signals:
                if slot.time_kind and signal.get("kind") != slot.time_kind:
                    continue
                if slot.from_time_key in signal:
                    value = signal.get(slot.from_time_key)
                    break

        if value is None and slot.from_entity_types:
            for entity in segment_entities:
                if entity.type in slot.from_entity_types:
                    value = entity.normalized if slot.use_normalized_entity and entity.normalized is not None else entity.value
                    break

        if value is None and slot.regex:
            flags = re.IGNORECASE if locale == "en-US" else 0
            match = re.search(slot.regex, segment.text, flags=flags)
            if match:
                try:
                    value = match.group(slot.regex_group)
                except IndexError:
                    value = match.group(0)

        if value is None and slot.default is not None:
            value = slot.default

        if value is None and slot.required:
            missing.append(slot.name)
            continue

        if value is not None:
            params[slot.name] = value

    if "duration_seconds" in params and "trigger_at" not in params:
        try:
            seconds = int(params["duration_seconds"])
            normalized["duration_seconds"] = seconds
            normalized["trigger_at"] = (now + timedelta(seconds=seconds)).isoformat()
        except Exception:
            pass

    if "trigger_at" in params:
        normalized["trigger_at"] = _as_text(params["trigger_at"])

    return params, normalized, missing


def _select_candidates(
    request: IntentFilterRequest,
    segments: list[Segment],
    entities: list[Entity],
    time_signals: list[dict[str, Any]],
    now: datetime,
    locale: str,
) -> tuple[list[SelectedIntent], list[CandidateIntent]]:
    options = request.options
    all_candidates: list[tuple[int, IntentSpec, Segment, float, list[Evidence]]] = []

    for segment in segments:
        for intent in request.intent_catalog:
            score, evidence = _intent_score(intent, segment, entities, locale)
            threshold = intent.match.min_confidence if intent.match.min_confidence is not None else options.min_confidence
            if score >= threshold:
                all_candidates.append((segment.index, intent, segment, score, evidence))

    all_candidates.sort(key=lambda item: (item[0], -item[3], -item[1].priority, item[1].id))

    selected_rows: list[tuple[int, IntentSpec, Segment, float, list[Evidence]]] = []
    debug_candidates: list[CandidateIntent] = []
    per_segment_counter: dict[int, int] = {}

    for seg_index, intent, segment, score, evidence in all_candidates:
        debug_candidates.append(CandidateIntent(intent_id=intent.id, segment_index=seg_index, confidence=round(score, 4)))

        if not options.allow_multi_intent and selected_rows:
            continue

        count = per_segment_counter.get(seg_index, 0)
        if count >= options.max_intents_per_segment:
            continue

        selected_rows.append((seg_index, intent, segment, score, evidence))
        per_segment_counter[seg_index] = count + 1

        if len(selected_rows) >= options.max_intents:
            break

    selected: list[SelectedIntent] = []
    for seg_index, intent, segment, score, evidence in selected_rows:
        params, normalized, missing = _fill_parameters(intent, segment, entities, time_signals, now, locale)
        status: Literal["ready", "need_clarification", "rejected", "system"] = "ready"
        if missing:
            status = "need_clarification"

        selected.append(
            SelectedIntent(
                intent_id=intent.id,
                intent_name=intent.name or intent.id,
                confidence=round(score, 4),
                status=status,
                segment_index=seg_index,
                span=TextSpan(text=segment.text, start=segment.start, end=segment.end),
                parameters=params,
                normalized=normalized,
                missing_parameters=missing,
                evidence=evidence,
            )
        )

    return selected, debug_candidates


def _is_no_action_utterance(command_text: str, entities: list[Entity], time_signals: list[dict[str, Any]], locale: str) -> tuple[bool, str]:
    profile = _profile(locale)

    question_pattern = profile.get("question_pattern")
    if question_pattern and re.search(question_pattern, command_text, flags=re.IGNORECASE):
        return False, "question_or_discussion"

    has_time = len(time_signals) > 0
    has_device_or_room = any(entity.type in {"device", "room"} for entity in entities)
    has_action = any(entity.type == "action" for entity in entities)

    if has_time or has_device_or_room:
        return False, "has_task_signal"

    for pattern in profile.get("no_action_patterns", []):
        if re.search(pattern, command_text, flags=re.IGNORECASE):
            return True, "matched_no_action_pattern"

    if has_action:
        return False, "action_without_target"

    if locale == "en-US":
        word_count = len([w for w in command_text.split(" ") if w])
        if word_count <= 4:
            return True, "short_emotion_utterance"
    else:
        if len(command_text) <= 8:
            return True, "short_emotion_utterance"

    return False, "insufficient_signal_for_no_action"


def _build_system_intent(
    intent_id: str,
    intent_name: str,
    reason: str,
    command_text: str,
    confidence: float,
) -> SelectedIntent:
    return SelectedIntent(
        intent_id=intent_id,
        intent_name=intent_name,
        confidence=_clamp(confidence),
        status="system",
        segment_index=0,
        span=TextSpan(text=command_text, start=0, end=len(command_text)),
        parameters={"reason": reason},
        normalized={"system_intent": True},
        missing_parameters=[],
        evidence=[Evidence(type="system", value=reason, score=1.0)],
    )


app = FastAPI(title="intent-filter-service", version="0.4.0")


@app.get("/healthz")
def healthz() -> dict[str, Any]:
    return {
        "ok": True,
        "engine": "mvp-rule-filter",
        "version": "0.4.0",
        "locales": sorted(SUPPORTED_LOCALES),
    }


@app.post("/v1/intents/filter", response_model=IntentFilterResponse)
def filter_intents(payload: IntentFilterRequest) -> IntentFilterResponse:
    start = time.perf_counter()

    request_id = payload.request_id or f"ifr-{uuid.uuid4().hex}"
    command_text, timezone, locale, now, entities = _build_command_context(payload.command)

    time_signals: list[dict[str, Any]] = []
    if payload.options.enable_time_parser:
        time_signals = _collect_time_signals(command_text, now, locale)
        entities = _attach_time_entities(command_text, entities, time_signals)

    segments = _split_segments(command_text, locale)
    intents, candidates = _select_candidates(payload, segments, entities, time_signals, now, locale)

    decision = RoutingDecision(
        action="execute_intents",
        trigger_intent_id=intents[0].intent_id if intents else SYSTEM_INTENT_FALLBACK,
        reason="matched_catalog_intents" if intents else "no_catalog_intent_matched",
    )

    if not intents:
        decision = RoutingDecision(
            action="fallback_reasoning",
            trigger_intent_id=SYSTEM_INTENT_FALLBACK,
            reason="no_catalog_intent_matched",
        )

    if not intents and payload.options.emit_system_intent_when_empty:
        is_no_action, reason = _is_no_action_utterance(command_text, entities, time_signals, locale)
        if is_no_action:
            intents = [
                _build_system_intent(
                    intent_id=SYSTEM_INTENT_NO_ACTION,
                    intent_name="无需处理",
                    reason=reason,
                    command_text=command_text,
                    confidence=0.99,
                )
            ]
            decision = RoutingDecision(action="no_action", trigger_intent_id=SYSTEM_INTENT_NO_ACTION, reason=reason)
        else:
            intents = [
                _build_system_intent(
                    intent_id=SYSTEM_INTENT_FALLBACK,
                    intent_name="触发高级推理",
                    reason=reason,
                    command_text=command_text,
                    confidence=0.9,
                )
            ]
            decision = RoutingDecision(
                action="fallback_reasoning",
                trigger_intent_id=SYSTEM_INTENT_FALLBACK,
                reason=reason,
            )

    latency_ms = (time.perf_counter() - start) * 1000.0
    meta: dict[str, Any] = {
        "latency_ms": round(latency_ms, 3),
        "segment_count": len(segments),
        "catalog_size": len(payload.intent_catalog),
        "time_signals": len(time_signals),
        "timezone": timezone,
        "locale": locale,
        "now": now.isoformat(),
    }
    if payload.options.return_debug_entities:
        meta["extracted_entities"] = [entity.model_dump() for entity in entities]

    return IntentFilterResponse(
        request_id=request_id,
        intents=intents,
        decision=decision,
        candidates=candidates if payload.options.return_debug_candidates else None,
        meta=meta,
    )
