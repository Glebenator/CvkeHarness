package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/coolcake/cvkeharness/agent"
	"github.com/coolcake/cvkeharness/config"
	"github.com/coolcake/cvkeharness/internal/log"
	"github.com/coolcake/cvkeharness/provider"
	"github.com/coolcake/cvkeharness/tools"
	"github.com/spf13/cobra"
)

var runCmd = &cobra.Command{
	Use:   "run [task]",
	Short: "Execute a DevOps task via the LLM agent",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		task := args[0]

		// 1. Load config
		cfg, err := config.LoadConfig()
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}

		// 2. Initialize logger based on config
		log.Init(cfg.LogLevel, "text") // Force text for CLI usage, json better for server
		logger := log.FromContext(context.Background())
		logger.Info("CvkeHarness starting up", "model", cfg.Model)

		// 3. Initialize Provider
		var p provider.Provider
		if cfg.Provider == "openrouter" {
			p = provider.NewOpenRouter(cfg.GetAPIKey("openrouter"))
		} else if cfg.Provider == "lmstudio" {
			p = provider.NewLMStudio(cfg.BaseURL)
		} else {
			fmt.Printf("Error: Unsupported provider '%s'\n", cfg.Provider)
			os.Exit(1)
		}

		// 4. Initialize Tools
		registry := tools.NewRegistry()
		registry.Register(tools.NewDockerListTool())
		registry.Register(tools.NewDockerInspectTool())
		registry.Register(tools.NewDockerRestartTool())
		registry.Register(tools.NewHTTPHealthcheckTool())
		registry.Register(tools.NewTCPHealthcheckTool())
		registry.Register(tools.NewShellTool(cfg.AllowedCommands))

		logger.Info("tools initialized", "count", len(registry.Definitions()))

		// 5. Initialize Agent
		a := agent.New(p, registry, cfg.Model, cfg.MaxIterations, cfg.MaxTokens)

		// 6. Run
		fmt.Printf("\nExecuting task: %s\n", task)
		fmt.Println("----------------------------------------")

		result, err := a.Run(context.Background(), task)
		if err != nil {
			fmt.Printf("\nAgent failed: %v\n", err)
			os.Exit(1)
		}

		fmt.Println("\nResult:")
		fmt.Println(result)
	},
}

func init() {
	rootCmd.AddCommand(runCmd)
}
