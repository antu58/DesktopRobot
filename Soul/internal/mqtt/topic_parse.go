package mqtt

import (
	"fmt"
	"strings"
)

// expected: {prefix}/terminal/{terminalId}/{kind}/...
func ParseTerminalID(topic, prefix string) (string, error) {
	parts := strings.Split(topic, "/")
	prefixParts := strings.Split(prefix, "/")
	if len(parts) < len(prefixParts)+3 {
		return "", fmt.Errorf("invalid topic: %s", topic)
	}
	for i, p := range prefixParts {
		if parts[i] != p {
			return "", fmt.Errorf("topic prefix mismatch: %s", topic)
		}
	}
	if parts[len(prefixParts)] != "terminal" {
		return "", fmt.Errorf("invalid topic pattern: %s", topic)
	}
	return parts[len(prefixParts)+1], nil
}

func ParseRequestID(topic string) string {
	parts := strings.Split(topic, "/")
	if len(parts) == 0 {
		return ""
	}
	return parts[len(parts)-1]
}
