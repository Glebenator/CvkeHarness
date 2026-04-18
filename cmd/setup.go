package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"syscall"

	"github.com/coolcake/cvkeharness/config"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var setupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Interactive onboarding wizard to configure the agent",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("Welcome to CvkeHarness Setup!")
		fmt.Println("Let's configure your AI DevOps agent.")
		fmt.Println("----------------------------------------")

		cfg := config.DefaultConfig()

		reader := bufio.NewReader(os.Stdin)

		// 1. Provider
		fmt.Printf("Provider [%s]: ", cfg.Provider)
		provider, _ := reader.ReadString('\n')
		provider = strings.TrimSpace(provider)
		if provider != "" {
			cfg.Provider = provider
		}

		if cfg.Provider != "openrouter" && cfg.Provider != "lmstudio" {
			fmt.Printf("Warning: Currently only 'openrouter' and 'lmstudio' are fully supported out of the box.\n")
		}

		// 2. Base URL (if LM Studio)
		if cfg.Provider == "lmstudio" {
			fmt.Printf("Base URL [%s]: ", "http://localhost:1234/v1")
			baseURL, _ := reader.ReadString('\n')
			baseURL = strings.TrimSpace(baseURL)
			if baseURL != "" {
				cfg.BaseURL = baseURL
			} else {
				cfg.BaseURL = "http://localhost:1234/v1"
			}
		}

		// 3. API Key (Masked - Only required for openrouter)
		if cfg.Provider == "openrouter" {
			fmt.Print("API Key: ")
			bytePassword, err := term.ReadPassword(int(syscall.Stdin))
			fmt.Println()
			if err == nil {
				key := strings.TrimSpace(string(bytePassword))
				if key != "" {
					cfg.APIKey = key
				}
			}

			if cfg.APIKey == "" {
				fmt.Println("Error: API Key is required for OpenRouter.")
				os.Exit(1)
			}
		}

		if cfg.Provider == "lmstudio" && cfg.Model == "anthropic/claude-3.5-sonnet" {
			// Change sensible default if they picked LM Studio but hadn't set a model yet
			cfg.Model = "local-model"
		}
		fmt.Printf("Model [%s]: ", cfg.Model)
		model, _ := reader.ReadString('\n')
		model = strings.TrimSpace(model)
		if model != "" {
			cfg.Model = model
		}

		// Save Configuration
		fmt.Println("----------------------------------------")
		fmt.Println("Saving configuration to ~/.cvkeharness/config.yaml...")

		if err := cfg.Save(); err != nil {
			fmt.Printf("Failed to save configuration: %v\n", err)
			os.Exit(1)
		}

		fmt.Println("Setup complete! You can now run:")
		fmt.Println(`  cvkeharness run "list all docker containers and tell me which ones are running"`)
	},
}

func init() {
	rootCmd.AddCommand(setupCmd)
}
