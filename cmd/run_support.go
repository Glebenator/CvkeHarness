package cmd

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"syscall"

	"github.com/coolcake/cvkeharness/state"
)

func phaseModelLabel(phase state.PhaseRecord) string {
	model := strings.TrimSpace(phase.ActualModel)
	if model == "" {
		model = strings.TrimSpace(phase.RequestedModel)
	}
	if model == "" {
		return ""
	}
	if provider := strings.TrimSpace(phase.Provider); provider != "" {
		return provider + "/" + model
	}
	return model
}

func summarizeModelCounts(modelCounts map[string]int) []string {
	if len(modelCounts) == 0 {
		return nil
	}

	type modelUsage struct {
		name  string
		count int
	}

	items := make([]modelUsage, 0, len(modelCounts))
	for name, count := range modelCounts {
		items = append(items, modelUsage{name: name, count: count})
	}

	sort.Slice(items, func(i, j int) bool {
		if items[i].count == items[j].count {
			return items[i].name < items[j].name
		}
		return items[i].count > items[j].count
	})

	out := make([]string, 0, len(items))
	for _, item := range items {
		label := item.name
		if item.count > 1 {
			label = fmt.Sprintf("%s x%d", item.name, item.count)
		}
		out = append(out, label)
	}
	return out
}

func signalExitReason(sig os.Signal) string {
	if sig == nil {
		return "interrupt"
	}
	if sig == syscall.SIGTERM {
		return "terminated"
	}
	return "interrupt"
}

func humanizeExitReason(exitReason string) string {
	switch strings.TrimSpace(exitReason) {
	case "terminated":
		return "Terminated"
	case "":
		return "Completed"
	default:
		return "Interrupted"
	}
}

func summarizeRunExit(taskState state.TaskState, signalReason string) string {
	if strings.TrimSpace(signalReason) != "" {
		return humanizeExitReason(signalReason)
	}
	switch taskState {
	case state.TaskStateBlockedWaitingUser:
		return "Approval required"
	case state.TaskStateIncomplete:
		return "Incomplete"
	case state.TaskStateFailed:
		return "Failed"
	default:
		return "Completed"
	}
}
