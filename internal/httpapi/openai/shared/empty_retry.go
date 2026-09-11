package shared

import (
	"os"
	"strconv"
	"strings"
)

const EmptyOutputRetrySuffix = "Previous reply had no visible output. Please regenerate the visible final answer or tool call now."

// DefaultEmptyOutputRetryMaxAttempts is used when
// DS2API_EMPTY_OUTPUT_RETRY_MAX_ATTEMPTS is unset or unusable. Upstream
// rate-limits tend to surface as a thinking-only response, and a single
// same-account retry rarely outlives them, so the default allows a few rounds.
const DefaultEmptyOutputRetryMaxAttempts = 3

func EmptyOutputRetryEnabled() bool {
	return true
}

func EmptyOutputRetryMaxAttempts() int {
	raw := strings.TrimSpace(os.Getenv("DS2API_EMPTY_OUTPUT_RETRY_MAX_ATTEMPTS"))
	if raw == "" {
		return DefaultEmptyOutputRetryMaxAttempts
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil || parsed <= 0 {
		return DefaultEmptyOutputRetryMaxAttempts
	}
	return parsed
}

func ClonePayloadWithEmptyOutputRetryPrompt(payload map[string]any) map[string]any {
	return ClonePayloadForEmptyOutputRetry(payload, 0)
}

// ClonePayloadForEmptyOutputRetry creates a retry payload with the suffix
// appended and, if parentMessageID > 0, sets parent_message_id so the
// retry is submitted as a proper follow-up turn in the same DeepSeek
// session rather than a disconnected root message.
func ClonePayloadForEmptyOutputRetry(payload map[string]any, parentMessageID int) map[string]any {
	clone := make(map[string]any, len(payload))
	for k, v := range payload {
		clone[k] = v
	}
	original, _ := payload["prompt"].(string)
	clone["prompt"] = AppendEmptyOutputRetrySuffix(original)
	if parentMessageID > 0 {
		clone["parent_message_id"] = parentMessageID
	}
	return clone
}

func AppendEmptyOutputRetrySuffix(prompt string) string {
	prompt = strings.TrimRight(prompt, "\r\n\t ")
	if prompt == "" {
		return EmptyOutputRetrySuffix
	}
	return prompt + "\n\n" + EmptyOutputRetrySuffix
}

func UsagePromptWithEmptyOutputRetry(originalPrompt string, retryAttempts int) string {
	if retryAttempts <= 0 {
		return originalPrompt
	}
	parts := make([]string, 0, retryAttempts+1)
	parts = append(parts, originalPrompt)
	next := originalPrompt
	for i := 0; i < retryAttempts; i++ {
		next = AppendEmptyOutputRetrySuffix(next)
		parts = append(parts, next)
	}
	return strings.Join(parts, "\n")
}
