import unittest

from fastapi.testclient import TestClient

from app import app


class IntentFilterTestCase(unittest.TestCase):
    def setUp(self) -> None:
        self.client = TestClient(app)

    def _catalog(self) -> list[dict]:
        return [
            {
                "id": "light_off",
                "name": "关灯/turn off light",
                "priority": 90,
                "match": {
                    "keywords_any": ["关", "关闭", "灯", "turn off", "light", "꺼", "불", "消して", "ライト"],
                    "min_confidence": 0.30,
                },
                "slots": [
                    {"name": "action", "required": True, "from_entity_types": ["action"]},
                    {"name": "device", "required": True, "from_entity_types": ["device"]},
                    {"name": "room", "from_entity_types": ["room"]},
                ],
            },
            {
                "id": "reminder_create",
                "name": "提醒/reminder",
                "priority": 80,
                "match": {
                    "keywords_any": ["提醒", "remind", "알려", "리마인드", "リマインド", "知らせて"],
                    "min_confidence": 0.30,
                },
                "slots": [
                    {"name": "duration_seconds", "required": True, "from_time_key": "duration_seconds", "time_kind": "duration"},
                    {"name": "trigger_at", "required": True, "from_time_key": "trigger_at", "time_kind": "duration"},
                ],
            },
        ]

    def test_multi_intent_filter_zh(self) -> None:
        payload = {
            "command": "帮我关闭卧室的灯并且提醒我10分钟后取快递",
            "intent_catalog": self._catalog(),
            "options": {
                "allow_multi_intent": True,
                "max_intents_per_segment": 1,
                "return_debug_entities": True,
            },
        }
        response = self.client.post("/v1/intents/filter", json=payload)
        self.assertEqual(response.status_code, 200)

        body = response.json()
        self.assertEqual(body["decision"]["action"], "execute_intents")
        self.assertEqual(body["meta"]["locale"], "zh-CN")
        self.assertEqual(len(body["intents"]), 2)

        first, second = body["intents"]
        self.assertEqual(first["intent_id"], "light_off")
        self.assertEqual(second["intent_id"], "reminder_create")
        self.assertEqual(second["parameters"]["duration_seconds"], 600)

    def test_locale_zh_tw(self) -> None:
        payload = {
            "command": "請幫我關閉臥室的燈",
            "intent_catalog": self._catalog(),
        }
        response = self.client.post("/v1/intents/filter", json=payload)
        self.assertEqual(response.status_code, 200)
        body = response.json()
        self.assertEqual(body["meta"]["locale"], "zh-TW")
        self.assertEqual(body["decision"]["action"], "execute_intents")

    def test_locale_en(self) -> None:
        payload = {
            "command": "Please turn off the bedroom light and remind me in 10 minutes",
            "intent_catalog": self._catalog(),
            "options": {"allow_multi_intent": True, "max_intents_per_segment": 1},
        }
        response = self.client.post("/v1/intents/filter", json=payload)
        self.assertEqual(response.status_code, 200)
        body = response.json()
        self.assertEqual(body["meta"]["locale"], "en-US")
        self.assertEqual(body["decision"]["action"], "execute_intents")

    def test_locale_ko(self) -> None:
        payload = {
            "command": "침실 불 꺼줘 그리고 10분 뒤에 알려줘",
            "intent_catalog": self._catalog(),
        }
        response = self.client.post("/v1/intents/filter", json=payload)
        self.assertEqual(response.status_code, 200)
        body = response.json()
        self.assertEqual(body["meta"]["locale"], "ko-KR")
        self.assertEqual(body["decision"]["action"], "execute_intents")

    def test_locale_ja(self) -> None:
        payload = {
            "command": "寝室のライトを消して、10分後に知らせて",
            "intent_catalog": self._catalog(),
        }
        response = self.client.post("/v1/intents/filter", json=payload)
        self.assertEqual(response.status_code, 200)
        body = response.json()
        self.assertEqual(body["meta"]["locale"], "ja-JP")
        self.assertEqual(body["decision"]["action"], "execute_intents")

    def test_no_action_system_intent(self) -> None:
        payload = {
            "command": "吓我一跳",
            "intent_catalog": self._catalog(),
            "options": {"return_debug_entities": True},
        }
        response = self.client.post("/v1/intents/filter", json=payload)
        self.assertEqual(response.status_code, 200)

        body = response.json()
        self.assertEqual(body["decision"]["action"], "no_action")
        self.assertEqual(body["decision"]["trigger_intent_id"], "sys.no_action")
        self.assertEqual(body["intents"][0]["intent_id"], "sys.no_action")
        self.assertEqual(body["intents"][0]["status"], "system")

    def test_fallback_reasoning_system_intent(self) -> None:
        payload = {
            "command": "这个事情你怎么看",
            "intent_catalog": self._catalog(),
        }
        response = self.client.post("/v1/intents/filter", json=payload)
        self.assertEqual(response.status_code, 200)

        body = response.json()
        self.assertEqual(body["decision"]["action"], "fallback_reasoning")
        self.assertEqual(body["decision"]["trigger_intent_id"], "sys.fallback_reasoning")
        self.assertEqual(body["intents"][0]["intent_id"], "sys.fallback_reasoning")
        self.assertEqual(body["intents"][0]["status"], "system")


if __name__ == "__main__":
    unittest.main()
