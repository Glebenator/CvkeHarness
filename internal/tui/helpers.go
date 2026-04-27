package tui

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// ── status icons ────────────────────────────────────────────────────

func statusIcon(success bool) string {
	if success {
		return styleSuccess.Render("●")
	}
	return styleError.Render("●")
}

func enabledIcon(enabled bool) string {
	if enabled {
		return styleSuccess.Render("●")
	}
	return styleMuted.Render("○")
}

// ── time formatting ─────────────────────────────────────────────────

func timeAgo(t time.Time) string {
	if t.IsZero() {
		return styleMuted.Render("—")
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		m := int(d.Minutes())
		if m == 1 {
			return "1m ago"
		}
		return fmt.Sprintf("%dm ago", m)
	case d < 24*time.Hour:
		h := int(d.Hours())
		if h == 1 {
			return "1h ago"
		}
		return fmt.Sprintf("%dh ago", h)
	case d < 7*24*time.Hour:
		days := int(d.Hours() / 24)
		if days == 1 {
			return "1d ago"
		}
		return fmt.Sprintf("%dd ago", days)
	default:
		return t.Format("Jan 2")
	}
}

func fmtDuration(d time.Duration) string {
	if d <= 0 {
		return "—"
	}
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm%ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
}

func fmtDurationMs(ms int64) string {
	if ms <= 0 {
		return "—"
	}
	return fmtDuration(time.Duration(ms) * time.Millisecond)
}

func fmtTime(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	return t.Local().Format("Jan 2 15:04")
}

// ── string helpers ──────────────────────────────────────────────────

func truncate(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	runes := []rune(s)
	if max <= 1 {
		return string(runes[:1])
	}
	return string(runes[:max-1]) + "…"
}

func padRight(s string, width int) string {
	n := utf8.RuneCountInString(s)
	if n >= width {
		return truncate(s, width)
	}
	return s + strings.Repeat(" ", width-n)
}

// wrapText breaks a string into lines that fit within maxWidth runes.
func wrapText(s string, maxWidth int) []string {
	if maxWidth <= 0 {
		return []string{s}
	}
	words := strings.Fields(s)
	if len(words) == 0 {
		return nil
	}
	var lines []string
	current := words[0]
	for _, word := range words[1:] {
		if utf8.RuneCountInString(current)+1+utf8.RuneCountInString(word) > maxWidth {
			lines = append(lines, current)
			current = word
		} else {
			current += " " + word
		}
	}
	lines = append(lines, current)
	return lines
}

func formatTokens(n int) string {
	if n <= 0 {
		return "—"
	}
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	return fmt.Sprintf("%.1fk", float64(n)/1000)
}

func successRate(runs, successes int) string {
	if runs <= 0 {
		return "—"
	}
	return fmt.Sprintf("%.0f%%", float64(successes)/float64(runs)*100)
}

// ── layout helpers ──────────────────────────────────────────────────

func renderKeyValue(label, value string) string {
	return styleDetailLabel.Render(label) + " " + styleDetailValue.Render(value)
}

func renderKeyHint(key, desc string) string {
	return styleKeyHelpKey.Render(key) + " " + styleKeyHelp.Render(desc)
}

func horizontalRule(width int) string {
	if width <= 0 {
		return ""
	}
	return styleSubtle.Render(strings.Repeat("─", width))
}

// ── list windowing ──────────────────────────────────────────────────
// Keeps the cursor visible by computing a scroll window.

func listWindow(cursor, total, viewportHeight int) (start, end int) {
	if total <= viewportHeight {
		return 0, total
	}
	half := viewportHeight / 2
	start = cursor - half
	if start < 0 {
		start = 0
	}
	end = start + viewportHeight
	if end > total {
		end = total
		start = end - viewportHeight
		if start < 0 {
			start = 0
		}
	}
	return start, end
}

// scrollHint returns a string like "↑ 3 more" / "↓ 5 more" or empty if
// the list is fully visible.
func scrollHints(start, end, total int) string {
	var parts []string
	if start > 0 {
		parts = append(parts, styleSubtle.Render(fmt.Sprintf("↑ %d more", start)))
	}
	if end < total {
		parts = append(parts, styleSubtle.Render(fmt.Sprintf("↓ %d more", total-end)))
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, styleMuted.Render("  "))
}

// positionIndicator renders a compact "3/15" cursor indicator.
func positionIndicator(cursor, total int) string {
	if total == 0 {
		return ""
	}
	return styleMuted.Render(fmt.Sprintf("%d/%d", cursor+1, total))
}

// ── arithmetic ──────────────────────────────────────────────────────

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
