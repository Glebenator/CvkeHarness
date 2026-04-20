package cmd

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/coolcake/cvkeharness/agent"
	"github.com/coolcake/cvkeharness/config"
	"github.com/coolcake/cvkeharness/core"
	"github.com/coolcake/cvkeharness/internal/cli"
	"github.com/coolcake/cvkeharness/internal/log"
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

	reader := bufio.NewReader(os.Stdin)
	session, sessionID, err := startChatSession(ctx, a, store, cfg)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}
	defer func() {
		finishChatSession(ctx, store, sessionID, "process_exit")
	}()

	ui.RenderBanner(session.Selection())

	for {
		fmt.Print(ui.Prompt())
		line, readErr := reader.ReadString('\n')
		if readErr != nil {
			finishChatSession(ctx, store, sessionID, "eof")
			sessionID = 0
			fmt.Println()
			return
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
			finishChatSession(ctx, store, sessionID, "cleared")
			sessionID = 0
			session, sessionID, err = startChatSession(ctx, a, store, cfg)
			if err != nil {
				fmt.Printf("Error: %v\n", err)
				return
			}
			ui.RenderBanner(session.Selection())
			continue
		case chatSlashExit:
			finishChatSession(ctx, store, sessionID, "user_exit")
			sessionID = 0
			ui.PrintInfo("Session ended", []string{"Chat ended. Run `cvkeharness chat` to start again."})
			return
		}

		ui.PrintUser(line)
		ui.StartThinking()
		result, turnErr := session.Turn(ctx, line)
		ui.StopThinking()
		persistChatTurn(ctx, store, sessionID, line, result)
		if store.Available() && result.Phase.Provider != "" {
			if err := store.RecordChatPhaseStats(ctx, result.TaskClass, result.Phase, result.Tools); err != nil {
				logger.Warn("failed to record chat stats", "error", err)
			}
		}

		if result.Output != "" {
			ui.PrintAssistant(result.Output, result.Phase, len(result.Tools))
		}
		if turnErr != nil {
			ui.PrintError("Assistant error", []string{turnErr.Error()})
		}
		if result.CurationError != nil {
			logger.Warn("chat curation failed", "error", result.CurationError)
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
