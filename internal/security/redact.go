package security

import (
	"regexp"
	"strings"
)

const Redacted = "[REDACTED]"

var sensitiveKey = regexp.MustCompile(`(?i)(authorization|password|passwd|secret|token|private.?key|kubeconfig|credential|access.?key|session|cookie)`)

func RedactMap(value map[string]any) map[string]any {
	return redactMap(value)
}

func redactMap(value map[string]any) map[string]any {
	result := make(map[string]any, len(value))
	for key, item := range value {
		if sensitiveKey.MatchString(key) {
			result[key] = Redacted
			continue
		}
		result[key] = redactValue(item)
	}
	return result
}

func redactValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return redactMap(typed)
	case []any:
		result := make([]any, len(typed))
		for index := range typed {
			result[index] = redactValue(typed[index])
		}
		return result
	case string:
		upper := strings.ToUpper(typed)
		if strings.Contains(upper, "BEGIN PRIVATE KEY") || strings.HasPrefix(upper, "BEARER ") {
			return Redacted
		}
		return typed
	default:
		return value
	}
}
