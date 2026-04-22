package cmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/coolcake/cvkeharness/agent"
	"github.com/coolcake/cvkeharness/config"
	"github.com/coolcake/cvkeharness/core"
	"github.com/coolcake/cvkeharness/internal/cli"
	"github.com/coolcake/cvkeharness/internal/log"
	"github.com/coolcake/cvkeharness/internal/termui"
	"github.com/coolcake/cvkeharness/memory"
	"github.com/coolcake/cvkeharness/router"
	"github.com/coolcake/cvkeharness/state"
	"github.com/coolcake/cvkeharness/tools"
	"github.com/spf13/cobra"
)

type chatSlashAction string

const (
	chatSlashNone  chatSlashAction = ""
	chatSlashHelp  chatSlashAction = "help"
	chatSlashClear chatSlashAction = "clear"
	chatSlashExit  chatSlashAction = "exit"
)

type chatInputResult struct {
	line string
	err  error
}

type chatTurnOutcome struct {
	result agent.ChatTurnResult
	err    error
}

type chatSessionState struct {
	session    *agent.ChatConversation
	sessionID  int64
	stats      *chatSessionStats
	summaryOut bool
}

func (s *chatSessionState) close(ctx context.Context, store *state.Store, ui *cli.ChatSurface, exitReason string) {
	if s == nil || s.summaryOut {
		return
	}
	finishChatSession(ctx, store, s.sessionID, exitReason)
	s.sessionID = 0
	if ui != nil && s.stats != nil {
		ui.PrintSessionSummary(s.stats.summary(exitReason))
	}
	s.summaryOut = true
}

type chatSessionStats struct {
	startedAt        time.Time
	fallbackModel    string
	modelCounts      map[string]int
	promptTokens     int
	completionTokens int
	totalTokens      int
	cachedTokens     int
	cachedKnown      bool
	toolCalls        int
	successfulTools  int
	failedTools      int
	turnCount        int
}

func newChatSessionStats(selection core.RoutingSelection) *chatSessionStats {
	fallbackModel := selection.Requested.Model
	if provider := strings.TrimSpace(selection.Requested.Provider); provider != "" && strings.TrimSpace(fallbackModel) != "" {
		fallbackModel = provider + "/" + fallbackModel
	}
	return &chatSessionStats{
		startedAt:     time.Now().UTC(),
		fallbackModel: fallbackModel,
		modelCounts:   make(map[string]int),
	}
}

func (s *chatSessionStats) recordTurn(result agent.ChatTurnResult) {
	if s == nil {
		return
	}

	s.turnCount++
	s.promptTokens += result.Phase.PromptTokens
	s.completionTokens += result.Phase.CompletionTokens
	s.totalTokens += result.Phase.TotalTokens
	if result.Phase.CachedTokensKnown {
		s.cachedKnown = true
		s.cachedTokens += result.Phase.CachedTokens
	}

	model := chatModelLabel(result.Phase)
	if model != "" {
		s.modelCounts[model]++
	}

	s.toolCalls += len(result.Tools)
	for _, tool := range result.Tools {
		if tool.Success {
			s.successfulTools++
			continue
		}
		s.failedTools++
	}
}

func (s *chatSessionStats) summary(exitReason string) cli.SessionSummary {
	if s == nil {
		return cli.SessionSummary{ExitReason: humanizeChatExitReason(exitReason)}
	}

	modelsUsed := summarizeModelCounts(s.modelCounts)
	if len(modelsUsed) == 0 && strings.TrimSpace(s.fallbackModel) != "" {
		modelsUsed = []string{s.fallbackModel + " (pinned)"}
	}

	return cli.SessionSummary{
		Duration:          time.Since(s.startedAt),
		TurnCount:         s.turnCount,
		ExitReason:        humanizeChatExitReason(exitReason),
		ModelsUsed:        modelsUsed,
		PromptTokens:      s.promptTokens,
		CompletionTokens:  s.completionTokens,
		TotalTokens:       s.totalTokens,
		CachedTokens:      s.cachedTokens,
		CachedTokensKnown: s.cachedKnown,
		ToolCalls:         s.toolCalls,
		SuccessfulTools:   s.successfulTools,
		FailedTools:       s.failedTools,
	}
}

var chatCmd = &cobra.Command{
	Use:   "chat",
	Short: "Start an interactive chat session with the LLM",
	Run: func(cmd *cobra.Command, args []string) {
		runChat()
	},
}

func init() {
	rootCmd.AddCommand(chatCmd)
}

func runChat() {
	cfg, err := config.LoadConfig()
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	ui := cli.NewChatSurface(os.Stdout)
	log.InitWithWriter(cfg.LogLevel, "text", ui)
	ctx := context.Background()
	logger := log.FromContext(ctx)

	p, err := resolveProvider(cfg, "")
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}

	store := state.Open(cfg.StateDBPath)
	if store.Err() != nil {
		fmt.Printf("Warning: state DB unavailable, continuing without persisted chat history (%v)\n", store.Err())
	}
	defer store.Close()

	mem := memory.NewManager(cfg.MemoryDir, store, cfg.MemoryMaxSnippets)
	if err := mem.EnsureFiles(); err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}
	if err := mem.Reindex(ctx); err != nil {
		logger.Warn("failed to reindex memory metadata", "error", err)
	}

	registry := tools.NewDefaultRegistryWithStoreAndMemory(cfg.AllowedCommands, store, mem, p, cfg.SafetyMode, cfg.SafetyModel, cfg.PrimaryModel())
	routingCfg := routingConfigFromConfig(cfg, store)
	r := router.New(routingCfg, store, func(ctx context.Context, selection core.RoutingSelection) (bool, error) {
		if selection.Recommendation == nil {
			return false, nil
		}
		return promptModelApproval(*selection.Recommendation, selection.RecommendationReason)
	})

	a := agent.New(agent.Options{
		Provider:         p,
		ProviderName:     cfg.Provider,
		ProviderResolver: providerResolver{cfg: cfg},
		ToolRegistry:     registry,
		EventObserver:    ui,
		DefaultModel:     cfg.PrimaryModel(),
		MaxIterations:    cfg.MaxIterations,
		MaxTokens:        cfg.MaxTokens,
		RoutingConfig:    routingCfg,
		Router:           r,
		MemoryRetriever:  mem,
		MemoryCurator:    mem,
		RunRecorder:      store,
	})

	signals := make(chan os.Signal, 2)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)

	reader := bufio.NewReader(os.Stdin)
	session, sessionID, err := startChatSession(ctx, a, store, cfg)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}
	current := &chatSessionState{
		session:   session,
		sessionID: sessionID,
		stats:     newChatSessionStats(session.Selection()),
	}
	defer func() {
		current.close(ctx, store, ui, "process_exit")
	}()

	ui.RenderBanner(current.session.Selection())
	notifyOnPrompt := false

	for {
		if notifyOnPrompt {
			termui.NotifyInputRequested(os.Stdout, "CvkeHarness", "Assistant is waiting for your input.")
			notifyOnPrompt = false
		}
		fmt.Print(ui.Prompt())
		inputCh := make(chan chatInputResult, 1)
		go func() {
			line, readErr := reader.ReadString('\n')
			inputCh <- chatInputResult{line: line, err: readErr}
		}()

		var line string
		select {
		case sig := <-signals:
			fmt.Println()
			current.close(ctx, store, ui, signalExitReason(sig))
			return
		case input := <-inputCh:
			if input.err != nil {
				current.close(ctx, store, ui, "eof")
				fmt.Println()
				return
			}
			line = input.line
		}

		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		switch parseChatSlashAction(line) {
		case chatSlashHelp:
			ui.PrintHelp()
			continue
		case chatSlashClear:
			current.close(ctx, store, ui, "cleared")
			session, sessionID, err = startChatSession(ctx, a, store, cfg)
			if err != nil {
				fmt.Printf("Error: %v\n", err)
				return
			}
			current = &chatSessionState{
				session:   session,
				sessionID: sessionID,
				stats:     newChatSessionStats(session.Selection()),
			}
			ui.RenderBanner(current.session.Selection())
			continue
		case chatSlashExit:
			current.close(ctx, store, ui, "user_exit")
			return
		}

		ui.PrintUser(line)
		ui.StartThinking()
		turnCtx, cancelTurn := context.WithCancel(ctx)
		outcomeCh := make(chan chatTurnOutcome, 1)
		go func() {
			result, turnErr := current.session.Turn(turnCtx, line)
			outcomeCh <- chatTurnOutcome{result: result, err: turnErr}
		}()

		outcome, exitReason, interrupted := waitForChatTurn(signals, ui, cancelTurn, outcomeCh)
		ui.StopThinking()
		recordChatTurn(ctx, store, current, line, outcome.result)

		if outcome.result.Output != "" {
			ui.PrintAssistant(outcome.result.Output, outcome.result.Phase, len(outcome.result.Tools))
		}
		if outcome.err != nil && !errors.Is(outcome.err, context.Canceled) {
			ui.PrintError("Assistant error", []string{outcome.err.Error()})
		}
		if outcome.result.CurationError != nil {
			logger.Warn("chat curation failed", "error", outcome.result.CurationError)
		}
		if interrupted {
			current.close(ctx, store, ui, exitReason)
			return
		}
		notifyOnPrompt = true
	}
}

func recordChatTurn(ctx context.Context, store *state.Store, current *chatSessionState, userInput string, result agent.ChatTurnResult) {
	if current == nil || current.stats == nil {
		return
	}
	if !hasChatTurnActivity(result) {
		return
	}

	logger := log.FromContext(ctx)
	current.stats.recordTurn(result)
	persistChatTurn(ctx, store, current.sessionID, userInput, result)
	if store != nil && store.Available() && result.Phase.Provider != "" {
		if err := store.RecordChatPhaseStats(ctx, result.TaskClass, result.Phase, result.Tools); err != nil {
			logger.Warn("failed to record chat stats", "error", err)
		}
	}
}

func waitForChatTurn(signals <-chan os.Signal, ui *cli.ChatSurface, cancelTurn context.CancelFunc, outcomeCh <-chan chatTurnOutcome) (chatTurnOutcome, string, bool) {
	select {
	case outcome := <-outcomeCh:
		cancelTurn()
		return outcome, "", false
	case sig := <-signals:
		cancelTurn()
		if ui != nil {
			ui.PrintInfo("Interrupt", []string{"Stopping the current turn and closing the session..."})
		}
		select {
		case outcome := <-outcomeCh:
			return outcome, signalExitReason(sig), true
		case sig := <-signals:
			return chatTurnOutcome{}, signalExitReason(sig), true
		}
	}
}

func startChatSession(ctx context.Context, a *agent.Agent, store *state.Store, cfg *config.Config) (*agent.ChatConversation, int64, error) {
	session, selection, err := a.StartChat(ctx)
	if err != nil {
		return nil, 0, err
	}

	var sessionID int64
	if store != nil && store.Available() {
		sessionID, err = store.StartChatSession(ctx, state.ChatSession{
			StartedAt:      time.Now().UTC(),
			Provider:       selection.Requested.Provider,
			PinnedModel:    selection.Requested.Model,
			RoutingEnabled: cfg.RoutingEnabled,
		})
		if err != nil {
			return nil, 0, err
		}
	}
	return session, sessionID, nil
}

func finishChatSession(ctx context.Context, store *state.Store, sessionID int64, exitReason string) {
	if store == nil || !store.Available() || sessionID == 0 {
		return
	}
	if err := store.FinishChatSession(ctx, sessionID, time.Now().UTC(), exitReason); err != nil {
		log.FromContext(ctx).Warn("failed to finish chat session", "error", err)
	}
}

func persistChatTurn(ctx context.Context, store *state.Store, sessionID int64, userInput string, result agent.ChatTurnResult) {
	if store == nil || !store.Available() || sessionID == 0 {
		return
	}

	turn := state.ChatTurn{
		SessionID:        sessionID,
		UserInput:        userInput,
		TaskClass:        result.TaskClass,
		RequestedModel:   result.Phase.RequestedModel,
		ActualModel:      result.Phase.ActualModel,
		Success:          result.Phase.Success,
		ErrorMessage:     errorString(result.ExecutionErr),
		LatencyMs:        result.Phase.LatencyMs,
		PromptTokens:     result.Phase.PromptTokens,
		CompletionTokens: result.Phase.CompletionTokens,
		TotalTokens:      result.Phase.TotalTokens,
		CreatedAt:        time.Now().UTC(),
	}
	messages := agent.TranscriptToStateMessages(sessionID, 0, 0, turn.CreatedAt, result.Transcript)
	if _, err := store.AppendChatTurn(ctx, sessionID, turn, messages); err != nil {
		log.FromContext(ctx).Warn("failed to persist chat turn", "error", err)
	}
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func hasChatTurnActivity(result agent.ChatTurnResult) bool {
	if strings.TrimSpace(result.Output) != "" {
		return true
	}
	if len(result.Transcript) > 0 || len(result.Tools) > 0 {
		return true
	}
	if result.Phase.Provider != "" || result.Phase.RequestedModel != "" || result.Phase.ActualModel != "" {
		return true
	}
	if result.Phase.LatencyMs > 0 || result.Phase.TotalTokens > 0 || result.Phase.CachedTokensKnown {
		return true
	}
	return false
}

func chatModelLabel(phase state.PhaseRecord) string {
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

func humanizeChatExitReason(exitReason string) string {
	switch strings.TrimSpace(exitReason) {
	case "user_exit":
		return "Exited by user"
	case "cleared":
		return "Started a new session"
	case "eof":
		return "Input closed"
	case "terminated":
		return "Terminated"
	case "process_exit":
		return "Process exited"
	default:
		return "Interrupted"
	}
}

func parseChatSlashAction(line string) chatSlashAction {
	switch strings.TrimSpace(strings.ToLower(line)) {
	case "/help":
		return chatSlashHelp
	case "/clear":
		return chatSlashClear
	case "/exit":
		return chatSlashExit
	default:
		return chatSlashNone
	}
}
