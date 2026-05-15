package secrets

import "regexp"

var likelySecretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`sk-or-v1-[A-Za-z0-9_-]{40,}`),
	regexp.MustCompile(`sk-[A-Za-z0-9_-]{20,}`),
	regexp.MustCompile(`tvly-[A-Za-z0-9_-]{16,}`),
	regexp.MustCompile(`(?i)authorization\s*:\s*bearer\s+[A-Za-z0-9._~+/=-]{12,}`),
	regexp.MustCompile(`(?i)\b(api[_-]?key|access[_-]?token|auth[_-]?token|password|secret)\s*[:=]\s*[^\s&]{8,}`),
	regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`),
}

// Contains reports whether text appears to contain an obvious credential.
func Contains(text string) bool {
	for _, pattern := range likelySecretPatterns {
		if pattern.MatchString(text) {
			return true
		}
	}
	return false
}

// Mask redacts obvious credentials from user- or tool-controlled strings.
func Mask(text string) string {
	if text == "" {
		return text
	}
	for _, pattern := range likelySecretPatterns {
		text = pattern.ReplaceAllString(text, "[REDACTED]")
	}
	return text
}
