package main

import (
	"fmt"
	"os"
)

// cmdHooks handles the `tetora hooks` subcommand.
func cmdHooks(args []string) {
	if len(args) == 0 {
		printHooksUsage()
		return
	}

	cfg := loadConfig("")

	switch args[0] {
	case "install":
		if err := installHooks(cfg.ListenAddr); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		// Also generate MCP bridge config.
		if err := generateMCPBridgeConfig(cfg); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to generate MCP bridge config: %v\n", err)
		} else {
			homeDir, _ := os.UserHomeDir()
			fmt.Printf("MCP bridge config: %s/.tetora/mcp/bridge.json\n", homeDir)
		}

	case "remove", "uninstall":
		if err := removeHooks(); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

	case "status":
		if err := showHooksStatus(); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

	default:
		fmt.Fprintf(os.Stderr, "Unknown hooks command: %s\n", args[0])
		printHooksUsage()
		os.Exit(1)
	}
}

func printHooksUsage() {
	fmt.Println(`Usage: tetora hooks <command>

Commands:
  install    Install Tetora hooks in Claude Code settings
  remove     Remove Tetora hooks from Claude Code settings
  status     Show current hook configuration`)
}
