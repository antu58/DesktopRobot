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
