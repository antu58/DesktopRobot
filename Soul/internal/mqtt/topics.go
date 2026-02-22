package mqtt

import "fmt"

func TopicTerminalSkills(prefix string) string {
	return fmt.Sprintf("%s/terminal/+/skills", prefix)
}

func TopicTerminalOnline(prefix string) string {
	return fmt.Sprintf("%s/terminal/+/online", prefix)
}

func TopicTerminalHeartbeat(prefix string) string {
	return fmt.Sprintf("%s/terminal/+/heartbeat", prefix)
}

func TopicTerminalResult(prefix string) string {
	return fmt.Sprintf("%s/terminal/+/result/+", prefix)
}

func TopicTerminalIntentCatalog(prefix string) string {
	return fmt.Sprintf("%s/terminal/+/intent_catalog", prefix)
}

func TopicInvoke(prefix, terminalID, requestID string) string {
	return fmt.Sprintf("%s/terminal/%s/invoke/%s", prefix, terminalID, requestID)
}

func TopicResult(prefix, terminalID, requestID string) string {
	return fmt.Sprintf("%s/terminal/%s/result/%s", prefix, terminalID, requestID)
}

func TopicSkills(prefix, terminalID string) string {
	return fmt.Sprintf("%s/terminal/%s/skills", prefix, terminalID)
}

func TopicOnline(prefix, terminalID string) string {
	return fmt.Sprintf("%s/terminal/%s/online", prefix, terminalID)
}

func TopicHeartbeat(prefix, terminalID string) string {
	return fmt.Sprintf("%s/terminal/%s/heartbeat", prefix, terminalID)
}

func TopicStatus(prefix, terminalID string) string {
	return fmt.Sprintf("%s/terminal/%s/status", prefix, terminalID)
}

func TopicIntentCatalog(prefix, terminalID string) string {
	return fmt.Sprintf("%s/terminal/%s/intent_catalog", prefix, terminalID)
}

func TopicEmotionUpdate(prefix, terminalID string) string {
	return fmt.Sprintf("%s/terminal/%s/emotion_update", prefix, terminalID)
}

func TopicIntentAction(prefix, terminalID string) string {
	return fmt.Sprintf("%s/terminal/%s/intent_action", prefix, terminalID)
}
