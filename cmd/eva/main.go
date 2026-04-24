// EVA CLI
package eva

import (
	"flag"
	"fmt"
	"os"

	"github.com/eva/agent/internal/agent"
)

type Config struct {
	Execute     string
	ExecuteFile string
	Model       string
	Interactive bool
	Session    bool
	Yes        bool
	Verbose    bool
	Help       bool
}

func parseArgs(args []string) (Config, error) {
	cfg := Config{}

	// Check for help flags before parsing
	for _, arg := range args[1:] {
		if arg == "-h" || arg == "--help" {
			cfg.Help = true
			return cfg, nil
		}
	}

	fs := flag.NewFlagSet("eva", flag.ContinueOnError)
	fs.SetOutput(os.Stdout)

	fs.StringVar(&cfg.Execute, "e", "", "Execute task as string")
	fs.StringVar(&cfg.ExecuteFile, "ef", "", "Execute task from file")
	fs.StringVar(&cfg.Model, "m", "", "Model to use")
	fs.BoolVar(&cfg.Verbose, "v", false, "Verbose mode")
	fs.BoolVar(&cfg.Interactive, "i", false, "Interactive REPL mode")
	fs.BoolVar(&cfg.Session, "session", false, "Enable session file for interactive mode")
	fs.BoolVar(&cfg.Yes, "y", false, "Skip confirmation prompt")

	if err := fs.Parse(args[1:]); err != nil {
		if err == flag.ErrHelp {
			cfg.Help = true
			return cfg, nil
		}
		return cfg, err
	}

	// Check for -h or --help in args
	for _, arg := range args[1:] {
		if arg == "-h" || arg == "--help" {
			cfg.Help = true
			break
		}
	}

	return cfg, nil
}

func Run(args []string) error {
	cfg, err := parseArgs(args)
	if err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}

	if cfg.Help {
		printHelp()
		return nil
	}

	if cfg.Execute == "" && cfg.ExecuteFile == "" && !cfg.Interactive {
		printHelp()
		return nil
	}

	if cfg.Execute != "" && cfg.ExecuteFile != "" {
		return fmt.Errorf("error: -e and -ef are mutually exclusive")
	}

	ag := agent.New(agent.Config{
		Model:   cfg.Model,
		Session: cfg.Session,
		Yes:     cfg.Yes,
	})

	if cfg.Interactive {
		return ag.Interactive()
	}

	task := cfg.Execute

	if cfg.ExecuteFile != "" {
		data, err := os.ReadFile(cfg.ExecuteFile)
		if err != nil {
			return fmt.Errorf("error reading file: %w", err)
		}
		task = string(data)
	}

	if err := ag.Execute(task, cfg.Interactive); err != nil {
		return err
	}

	return nil
}

func printHelp() {
	fmt.Println(`EVA - AI Agent for Development Tasks

Usage:
  eva -e "command"              Execute task from string
  eva -ef path/file.txt         Execute task from file
  eva -i                        Interactive REPL mode
  eva -e "command" -v         Verbose mode
  eva -e "command" -m model    Use specific model

Options:
  -e string     Execute task as string (mutually exclusive with -ef)
  -ef string    Execute task from file path (mutually exclusive with -e)
  -i            Interactive REPL mode (type /exit or Ctrl+D to quit)
  -session      Enable session file for interactive mode
  -y            Skip confirmation prompt (auto-confirm)
  -m string     Model to use (default: from gateway)
  -v            Verbose mode
  -h            Show this help

Examples:
  eva -e "create a web API in Go"
  eva -ef task.txt
  eva -i -session
  eva -e "list files" -y
  eva> add authentication
  eva> /exit`)
}