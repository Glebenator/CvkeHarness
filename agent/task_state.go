package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/coolcake/cvkeharness/state"
	"github.com/coolcake/cvkeharness/tools"
)

type scheduledJobContextKey struct{}

// WithScheduledJobID associates a run with the scheduler job that initiated it.
func WithScheduledJobID(ctx context.Context, id string) context.Context {
	if strings.TrimSpace(id) == "" {
		return ctx
	}
	return context.WithValue(ctx, scheduledJobContextKey{}, strings.TrimSpace(id))
}

func scheduledJobIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(scheduledJobContextKey{}).(string)
	return strings.TrimSpace(id)
}

type blockedTaskError struct {
	workID  string
	reason  string
	request tools.ShellApprovalRequest
}

func (e blockedTaskError) Error() string {
	if strings.TrimSpace(e.reason) != "" {
		return e.reason
	}
	return "task blocked waiting for user approval"
}

func (e blockedTaskError) TaskState() state.TaskState {
	return state.TaskStateBlockedWaitingUser
}

func (e blockedTaskError) WorkID() string {
	return e.workID
}

func taskStateForError(err error) state.TaskState {
	switch {
	case err == nil:
		return state.TaskStateCompleted
	case isBlockedTaskError(err):
		return state.TaskStateBlockedWaitingUser
	default:
		var incomplete incompleteTaskError
		if errors.As(err, &incomplete) {
			return state.TaskStateIncomplete
		}
		return state.TaskStateFailed
	}
}

func isBlockedTaskError(err error) bool {
	var blocked interface {
		TaskState() state.TaskState
	}
	return errors.As(err, &blocked) && blocked.TaskState() == state.TaskStateBlockedWaitingUser
}

func newRunCorrelationID() (string, error) {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate run correlation id: %w", err)
	}
	return "run_" + hex.EncodeToString(raw[:]), nil
}
