package systemcron

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

const (
	metadataPrefix = "# cvkeharness:id="
)

// Runner abstracts crontab for tests.
type Runner interface {
	List(ctx context.Context) (string, error)
	Install(ctx context.Context, content string) error
}

// CommandRunner uses the current user's crontab.
type CommandRunner struct{}

func (CommandRunner) List(ctx context.Context) (string, error) {
	cmd := exec.CommandContext(ctx, "crontab", "-l")
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := stderr.String()
		if strings.Contains(strings.ToLower(msg), "no crontab") {
			return "", nil
		}
		return "", fmt.Errorf("crontab -l failed: %s", strings.TrimSpace(msg))
	}
	return out.String(), nil
}

func (CommandRunner) Install(ctx context.Context, content string) error {
	cmd := exec.CommandContext(ctx, "crontab", "-")
	cmd.Stdin = strings.NewReader(content)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("crontab install failed: %s", strings.TrimSpace(stderr.String()))
	}
	return nil
}

// Client manages the current user's crontab.
type Client struct {
	runner Runner
}

// New creates a crontab client.
func New(runner Runner) *Client {
	if runner == nil {
		runner = CommandRunner{}
	}
	return &Client{runner: runner}
}

// Entry is a parsed cron command line.
type Entry struct {
	ID       string
	Line     int
	Schedule string
	Command  string
	Raw      string
	Disabled bool
	Managed  bool
	Hash     string
}

// Mutation describes a crontab write.
type Mutation struct {
	Action     string
	Target     string
	Schedule   string
	Command    string
	Name       string
	OldContent string
	NewContent string
	Entry      Entry
}

// List returns parsed cron command entries and raw content.
func (c *Client) List(ctx context.Context) ([]Entry, string, error) {
	content, err := c.runner.List(ctx)
	if err != nil {
		return nil, "", err
	}
	return Parse(content), content, nil
}

// Add prepares a new entry.
func (c *Client) Add(ctx context.Context, schedule, command, name string) (Mutation, error) {
	if err := ValidateEntry(schedule, command); err != nil {
		return Mutation{}, err
	}
	_, content, err := c.List(ctx)
	if err != nil {
		return Mutation{}, err
	}
	id, err := newCronID()
	if err != nil {
		return Mutation{}, err
	}
	var b strings.Builder
	b.WriteString(strings.TrimRight(content, "\n"))
	if b.Len() > 0 {
		b.WriteString("\n")
	}
	if strings.TrimSpace(name) != "" {
		b.WriteString("# ")
		b.WriteString(strings.TrimSpace(name))
		b.WriteString("\n")
	}
	b.WriteString(metadataPrefix)
	b.WriteString(id)
	b.WriteString("\n")
	raw := strings.TrimSpace(schedule) + " " + strings.TrimSpace(command)
	b.WriteString(raw)
	b.WriteString("\n")
	return Mutation{
		Action:     "add",
		Target:     id,
		Schedule:   strings.TrimSpace(schedule),
		Command:    strings.TrimSpace(command),
		Name:       strings.TrimSpace(name),
		OldContent: content,
		NewContent: b.String(),
		Entry: Entry{
			ID:       id,
			Schedule: strings.TrimSpace(schedule),
			Command:  strings.TrimSpace(command),
			Raw:      raw,
			Managed:  true,
			Hash:     hashLine(raw),
		},
	}, nil
}

// Update prepares replacement of a targeted entry.
func (c *Client) Update(ctx context.Context, target, schedule, command string) (Mutation, error) {
	if err := ValidateEntry(schedule, command); err != nil {
		return Mutation{}, err
	}
	return c.rewrite(ctx, "update", target, func(_ Entry) (string, error) {
		return strings.TrimSpace(schedule) + " " + strings.TrimSpace(command), nil
	})
}

// Remove prepares deletion of a targeted entry.
func (c *Client) Remove(ctx context.Context, target string) (Mutation, error) {
	return c.rewrite(ctx, "remove", target, func(Entry) (string, error) {
		return "", nil
	})
}

// SetEnabled prepares enable/disable of a targeted entry.
func (c *Client) SetEnabled(ctx context.Context, target string, enabled bool) (Mutation, error) {
	action := "enable"
	if !enabled {
		action = "disable"
	}
	return c.rewrite(ctx, action, target, func(entry Entry) (string, error) {
		raw := strings.TrimSpace(entry.Raw)
		if enabled {
			return strings.TrimSpace(strings.TrimPrefix(raw, "#")), nil
		}
		if strings.HasPrefix(raw, "#") {
			return raw, nil
		}
		return "# " + raw, nil
	})
}

// Apply installs a prepared mutation.
func (c *Client) Apply(ctx context.Context, mutation Mutation) error {
	return c.runner.Install(ctx, mutation.NewContent)
}

func (c *Client) rewrite(ctx context.Context, action, target string, replace func(Entry) (string, error)) (Mutation, error) {
	entries, content, err := c.List(ctx)
	if err != nil {
		return Mutation{}, err
	}
	lines := splitLines(content)
	entry, ok := findEntry(entries, target)
	if !ok {
		return Mutation{}, fmt.Errorf("cron entry %q not found", target)
	}
	nextRaw, err := replace(entry)
	if err != nil {
		return Mutation{}, err
	}
	if nextRaw == "" {
		start := entry.Line
		if entry.Managed && entry.Line > 0 && strings.HasPrefix(strings.TrimSpace(lines[entry.Line-1]), metadataPrefix) {
			start = entry.Line - 1
		}
		lines = append(lines[:start], lines[entry.Line+1:]...)
	} else {
		lines[entry.Line] = nextRaw
	}
	newContent := strings.Join(lines, "\n")
	if strings.TrimSpace(newContent) != "" {
		newContent = strings.TrimRight(newContent, "\n") + "\n"
	}
	entry.Raw = nextRaw
	entry.Hash = hashLine(nextRaw)
	return Mutation{
		Action:     action,
		Target:     target,
		OldContent: content,
		NewContent: newContent,
		Entry:      entry,
	}, nil
}

// Parse extracts cron command lines from full crontab content.
func Parse(content string) []Entry {
	lines := splitLines(content)
	var out []Entry
	pendingID := ""
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, metadataPrefix) {
			pendingID = strings.TrimSpace(strings.TrimPrefix(trimmed, metadataPrefix))
			continue
		}
		if trimmed == "" || isEnvLine(trimmed) {
			pendingID = ""
			continue
		}
		disabled := false
		candidate := trimmed
		if strings.HasPrefix(candidate, "#") {
			disabled = true
			candidate = strings.TrimSpace(strings.TrimPrefix(candidate, "#"))
			if !looksLikeCron(candidate) {
				continue
			}
		} else if strings.HasPrefix(candidate, "#") || !looksLikeCron(candidate) {
			pendingID = ""
			continue
		}
		schedule, command := splitCronLine(candidate)
		id := pendingID
		pendingID = ""
		out = append(out, Entry{
			ID:       id,
			Line:     i,
			Schedule: schedule,
			Command:  command,
			Raw:      line,
			Disabled: disabled,
			Managed:  id != "",
			Hash:     hashLine(line),
		})
	}
	return out
}

// ValidateEntry validates a cron schedule and command without executing it.
func ValidateEntry(schedule, command string) error {
	if strings.ContainsAny(schedule, "\n\r\x00") || strings.ContainsAny(command, "\n\r\x00") {
		return fmt.Errorf("cron schedule and command must be single-line text")
	}
	if !looksLikeCron(strings.TrimSpace(schedule) + " " + strings.TrimSpace(command)) {
		return fmt.Errorf("cron schedule must have five valid fields")
	}
	if strings.TrimSpace(command) == "" {
		return fmt.Errorf("cron command cannot be empty")
	}
	return nil
}

// Diff returns a compact before/after crontab diff.
func Diff(oldContent, newContent string) string {
	if oldContent == newContent {
		return "(no changes)"
	}
	return "--- current crontab\n+++ proposed crontab\n" + prefixedLines("-", oldContent) + prefixedLines("+", newContent)
}

func looksLikeCron(line string) bool {
	parts := strings.Fields(line)
	if len(parts) < 6 {
		return false
	}
	ranges := [][2]int{{0, 59}, {0, 23}, {1, 31}, {1, 12}, {0, 7}}
	for i := 0; i < 5; i++ {
		if !validCronField(parts[i], ranges[i][0], ranges[i][1]) {
			return false
		}
	}
	return true
}

func validCronField(raw string, minValue, maxValue int) bool {
	if raw == "*" {
		return true
	}
	for _, part := range strings.Split(raw, ",") {
		if part == "" {
			return false
		}
		base := part
		if strings.Contains(part, "/") {
			pieces := strings.Split(part, "/")
			if len(pieces) != 2 {
				return false
			}
			base = pieces[0]
			step, err := strconv.Atoi(pieces[1])
			if err != nil || step <= 0 {
				return false
			}
		}
		if base == "*" {
			continue
		}
		if strings.Contains(base, "-") {
			pieces := strings.Split(base, "-")
			if len(pieces) != 2 {
				return false
			}
			start, err1 := strconv.Atoi(pieces[0])
			end, err2 := strconv.Atoi(pieces[1])
			if err1 != nil || err2 != nil || start > end || start < minValue || end > maxValue {
				return false
			}
			continue
		}
		n, err := strconv.Atoi(base)
		if err != nil || n < minValue || n > maxValue {
			return false
		}
	}
	return true
}

func splitCronLine(line string) (string, string) {
	parts := strings.Fields(line)
	return strings.Join(parts[:5], " "), strings.Join(parts[5:], " ")
}

func findEntry(entries []Entry, target string) (Entry, bool) {
	for _, entry := range entries {
		if entry.ID != "" && entry.ID == target {
			return entry, true
		}
		if entry.Hash == target {
			return entry, true
		}
		if strconv.Itoa(entry.Line+1) == target || strconv.Itoa(entry.Line) == target {
			return entry, true
		}
	}
	return Entry{}, false
}

func isEnvLine(line string) bool {
	if strings.HasPrefix(line, "#") {
		return false
	}
	idx := strings.IndexByte(line, '=')
	if idx <= 0 {
		return false
	}
	key := line[:idx]
	return !strings.ContainsAny(key, " \t")
}

func splitLines(content string) []string {
	content = strings.TrimRight(content, "\n")
	if content == "" {
		return nil
	}
	return strings.Split(content, "\n")
}

func hashLine(line string) string {
	sum := sha256.Sum256([]byte(line))
	return hex.EncodeToString(sum[:])[:16]
}

func newCronID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "cron_" + hex.EncodeToString(b[:]), nil
}

func prefixedLines(prefix, text string) string {
	if text == "" {
		return prefix + "\n"
	}
	var b strings.Builder
	for _, line := range splitLines(text) {
		b.WriteString(prefix)
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}
