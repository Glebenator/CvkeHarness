package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/coolcake/cvkeharness/scheduler"
	"github.com/coolcake/cvkeharness/state"
)

// ScheduleManageTool lets the agent manage CvkeHarness internal schedules.
type ScheduleManageTool struct {
	store     *state.Store
	runNowCmd func(ctx context.Context, id string) (string, error)
}

// NewScheduleManageTool creates a schedule management tool.
func NewScheduleManageTool(store *state.Store) *ScheduleManageTool {
	return &ScheduleManageTool{store: store, runNowCmd: runScheduleJobCommand}
}

func (t *ScheduleManageTool) Name() string { return "schedule_manage" }

func (t *ScheduleManageTool) Description() string {
	return "Manages CvkeHarness internal scheduled jobs. Use this by default for recurring agent tasks, reminders, and health checks. Use system_cron_manage only when the user explicitly asks for OS/user/system crontab."
}

func (t *ScheduleManageTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"action": {"type": "string", "enum": ["list", "add", "update", "remove", "run_now", "pause", "resume", "runs"]},
			"id": {"type": "string"},
			"name": {"type": "string"},
			"schedule_kind": {"type": "string", "enum": ["at", "every", "cron"]},
			"schedule_spec": {"type": "string", "description": "RFC3339 for at, Go duration such as 5m for every, or five-field cron expression"},
			"prompt": {"type": "string"},
			"include_disabled": {"type": "boolean"},
			"limit": {"type": "number"}
		},
		"required": ["action"]
	}`)
}

func (t *ScheduleManageTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	if t.store == nil || !t.store.Available() {
		return "", fmt.Errorf("state database unavailable")
	}
	var req struct {
		Action          string `json:"action"`
		ID              string `json:"id"`
		Name            string `json:"name"`
		ScheduleKind    string `json:"schedule_kind"`
		ScheduleSpec    string `json:"schedule_spec"`
		Prompt          string `json:"prompt"`
		IncludeDisabled bool   `json:"include_disabled"`
		Limit           int    `json:"limit"`
	}
	if err := json.Unmarshal(args, &req); err != nil {
		return "", err
	}
	svc := scheduler.New(t.store)
	switch req.Action {
	case "list":
		jobs, err := t.store.ListScheduledJobs(ctx, req.IncludeDisabled)
		if err != nil {
			return "", err
		}
		data, _ := json.MarshalIndent(jobs, "", "  ")
		return string(data), nil
	case "add":
		job, err := svc.Create(ctx, req.Name, req.ScheduleKind, req.ScheduleSpec, req.Prompt)
		if err != nil {
			return "", err
		}
		return marshalJSON(job), nil
	case "update":
		if strings.TrimSpace(req.ID) == "" {
			return "", fmt.Errorf("id required")
		}
		job, err := svc.Update(ctx, req.ID, req.Name, req.ScheduleKind, req.ScheduleSpec, req.Prompt)
		if err != nil {
			return "", err
		}
		return marshalJSON(job), nil
	case "remove":
		if strings.TrimSpace(req.ID) == "" {
			return "", fmt.Errorf("id required")
		}
		return `{"removed":true}`, t.store.DeleteScheduledJob(ctx, req.ID)
	case "pause":
		job, err := svc.SetEnabled(ctx, req.ID, false)
		if err != nil {
			return "", err
		}
		return marshalJSON(job), nil
	case "resume":
		job, err := svc.SetEnabled(ctx, req.ID, true)
		if err != nil {
			return "", err
		}
		return marshalJSON(job), nil
	case "runs":
		if strings.TrimSpace(req.ID) == "" {
			return "", fmt.Errorf("id required")
		}
		runs, err := t.store.ListScheduledJobRuns(ctx, req.ID, req.Limit)
		if err != nil {
			return "", err
		}
		return marshalJSON(runs), nil
	case "run_now":
		if strings.TrimSpace(req.ID) == "" {
			return "", fmt.Errorf("id required")
		}
		return t.runNowCmd(ctx, req.ID)
	default:
		return "", fmt.Errorf("unknown action %q", req.Action)
	}
}

func marshalJSON(v any) string {
	data, _ := json.MarshalIndent(v, "", "  ")
	return string(data)
}

func runScheduleJobCommand(ctx context.Context, id string) (string, error) {
	runCtx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(runCtx, os.Args[0], "jobs", "run", id)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("scheduled job run failed: %w", err)
	}
	return string(out), nil
}
