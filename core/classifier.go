package core

import "strings"

// ClassifyTask returns a coarse routing class from the user task text.
func ClassifyTask(task string) TaskClass {
	lower := strings.ToLower(task)

	switch {
	case containsAny(lower, "summarize", "summary", "report", "recap", "explain what happened"):
		return TaskClassSummarization
	case containsAny(lower, "debug", "fix", "failure", "failing", "broken", "traceback", "error", "panic", "incident", "regression"):
		return TaskClassDebugging
	case containsAny(lower, "restart", "deploy", "delete", "remove", "secret", "credential", "approve", "policy", "permission", "network", "probe", "scan", "sudo"):
		return TaskClassPolicySensitive
	case containsAny(lower, "migrate", "rollout", "plan", "design", "architecture", "end-to-end", "refactor", "multi-step"):
		return TaskClassLongHorizon
	case containsAny(lower, "shell", "bash", "command", "journalctl", "systemctl", "ps ", "df ", "uptime", "netstat"):
		return TaskClassShellHeavy
	case containsAny(lower, "inspect", "show", "list", "status", "check", "diagnose", "what is", "look at"):
		return TaskClassInspection
	default:
		return TaskClassGeneral
	}
}

func containsAny(s string, terms ...string) bool {
	for _, term := range terms {
		if strings.Contains(s, term) {
			return true
		}
	}
	return false
}
