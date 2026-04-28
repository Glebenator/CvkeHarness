package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/coolcake/cvkeharness/internal/termui"
	"github.com/coolcake/cvkeharness/state"
	"github.com/coolcake/cvkeharness/systemcron"
)

// SystemCronManageTool manages the current user's crontab.
type SystemCronManageTool struct {
	store  *state.Store
	client *systemcron.Client
	in     io.Reader
	out    io.Writer
}

// NewSystemCronManageTool creates a current-user crontab management tool.
func NewSystemCronManageTool(store *state.Store) *SystemCronManageTool {
	return &SystemCronManageTool{
		store:  store,
		client: systemcron.New(nil),
		in:     os.Stdin,
		out:    os.Stdout,
	}
}

func (t *SystemCronManageTool) Name() string { return "system_cron_manage" }

func (t *SystemCronManageTool) Description() string {
	return "Manages the current user's OS crontab. Use only when the user explicitly asks for system, OS, user, or crontab-level scheduling. Writes arbitrary cron commands but always requires user confirmation."
}

func (t *SystemCronManageTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"action": {"type": "string", "enum": ["list", "show", "add", "update", "remove", "enable", "disable", "dry_run"]},
			"target": {"type": "string", "description": "Managed id, line number, or hash"},
			"schedule": {"type": "string", "description": "Five-field cron schedule"},
			"command": {"type": "string", "description": "Command string to place in crontab; not executed by CvkeHarness"},
			"name": {"type": "string"}
		},
		"required": ["action"]
	}`)
}

func (t *SystemCronManageTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var req struct {
		Action   string `json:"action"`
		Target   string `json:"target"`
		Schedule string `json:"schedule"`
		Command  string `json:"command"`
		Name     string `json:"name"`
	}
	if err := json.Unmarshal(args, &req); err != nil {
		return "", err
	}
	switch req.Action {
	case "list":
		entries, _, err := t.client.List(ctx)
		if err != nil {
			return "", err
		}
		return marshalJSON(entries), nil
	case "show":
		entries, content, err := t.client.List(ctx)
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(req.Target) == "" {
			return content, nil
		}
		for _, entry := range entries {
			if entry.ID == req.Target || entry.Hash == req.Target || fmt.Sprint(entry.Line+1) == req.Target || fmt.Sprint(entry.Line) == req.Target {
				return marshalJSON(entry), nil
			}
		}
		return "", fmt.Errorf("cron entry %q not found", req.Target)
	case "dry_run":
		mutation, err := t.prepare(ctx, req)
		if err != nil {
			return "", err
		}
		return systemcron.Diff(mutation.OldContent, mutation.NewContent), nil
	case "add", "update", "remove", "enable", "disable":
		mutation, err := t.prepare(ctx, req)
		if err != nil {
			t.audit(ctx, mutation, false, err)
			return "", err
		}
		diff := systemcron.Diff(mutation.OldContent, mutation.NewContent)
		fmt.Fprintln(t.out, diff)
		if !confirm(t.in, t.out, "Apply this crontab change?") {
			err := fmt.Errorf("system crontab change was not approved")
			t.audit(ctx, mutation, false, err)
			return "", err
		}
		err = t.client.Apply(ctx, mutation)
		t.audit(ctx, mutation, err == nil, err)
		if err != nil {
			return "", err
		}
		return marshalJSON(map[string]any{"ok": true, "action": mutation.Action, "target": mutation.Target}), nil
	default:
		return "", fmt.Errorf("unknown action %q", req.Action)
	}
}

func (t *SystemCronManageTool) prepare(ctx context.Context, req struct {
	Action   string `json:"action"`
	Target   string `json:"target"`
	Schedule string `json:"schedule"`
	Command  string `json:"command"`
	Name     string `json:"name"`
}) (systemcron.Mutation, error) {
	switch req.Action {
	case "add", "dry_run":
		return t.client.Add(ctx, req.Schedule, req.Command, req.Name)
	case "update":
		return t.client.Update(ctx, req.Target, req.Schedule, req.Command)
	case "remove":
		return t.client.Remove(ctx, req.Target)
	case "enable":
		return t.client.SetEnabled(ctx, req.Target, true)
	case "disable":
		return t.client.SetEnabled(ctx, req.Target, false)
	default:
		return systemcron.Mutation{}, fmt.Errorf("cannot prepare action %q", req.Action)
	}
}

func (t *SystemCronManageTool) audit(ctx context.Context, mutation systemcron.Mutation, success bool, err error) {
	if t.store == nil || !t.store.Available() {
		return
	}
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	_ = t.store.RecordSystemCronAudit(ctx, state.SystemCronAudit{
		Action:         mutation.Action,
		Target:         mutation.Target,
		OldSnippet:     mutation.OldContent,
		NewSnippet:     mutation.NewContent,
		Success:        success,
		ErrorMessage:   msg,
		InitiatingTool: t.Name(),
		CreatedAt:      time.Now().UTC(),
	})
}

func confirm(in io.Reader, out io.Writer, prompt string) bool {
	idx, err := termui.Select(termui.SelectOptions{
		Title: prompt,
		Details: []string{
			"Review the diff above before applying the change.",
		},
		Choices: []termui.Choice{
			{Label: "Cancel", Description: "Leave the current crontab unchanged"},
			{Label: "Apply change", Description: "Write the reviewed crontab update"},
		},
		InitialIndex: 0,
		In:           in,
		Out:          out,
	})
	if err != nil {
		return false
	}
	return idx == 1
}
