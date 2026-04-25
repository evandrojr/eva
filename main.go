// EVA CLI with Web UI
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/eva/agent/internal/agent"
)

type Config struct {
	Execute     string
	ExecuteFile string
	Model       string
	Interactive bool
	Terminal    bool
	Web         bool
	Lan         bool
	Port        string
	Install     bool
	Session     bool
	Yes         bool
	Setup       bool
	Help        bool
	PermInternet bool
	Verbose     bool
}

func parseArgs(args []string) (Config, error) {
	cfg := Config{Port: "11313", PermInternet: true}

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
	fs.BoolVar(&cfg.Interactive, "i", false, "Interactive REPL mode")
	fs.BoolVar(&cfg.Terminal, "basic_term", false, "Terminal compatibility mode (SSH/WSL)")
	fs.BoolVar(&cfg.Web, "web", false, "Web UI mode")
	fs.StringVar(&cfg.Port, "port", "11313", "Web UI port")
	fs.BoolVar(&cfg.Lan, "lan", false, "Serve on all interfaces")
fs.BoolVar(&cfg.Install, "install", false, "Install as systemd service")
	fs.BoolVar(&cfg.Session, "session", false, "Enable session file")
	fs.BoolVar(&cfg.Yes, "y", false, "Skip confirmation prompt")
	fs.BoolVar(&cfg.PermInternet, "internet", true, "Allow internet access (web search)")
	fs.BoolVar(&cfg.Verbose, "verbose", false, "Enable verbose output")
	fs.BoolVar(&cfg.Help, "h", false, "Show this help")
	fs.BoolVar(&cfg.Setup, "setup", false, "Setup API keys")

	if err := fs.Parse(args[1:]); err != nil {
		if err == flag.ErrHelp {
			cfg.Help = true
			return cfg, nil
		}
		return cfg, err
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

	if cfg.Help || cfg.Setup {
		printHelp()
		if cfg.Setup {
			fmt.Println("\n--- Setup ---")
			return setupAPIKeys()
		}
		return nil
	}

	if cfg.Execute == "" && cfg.ExecuteFile == "" && !cfg.Interactive && !cfg.Terminal && !cfg.Web && !cfg.Install {
		printHelp()
		return nil
	}

	if cfg.Web {
		return runWeb(cfg.Port, cfg.Lan)
	}

	if cfg.Install {
		return installService(cfg.Port)
	}

	ag := agent.New(agent.Config{
		Model:   cfg.Model,
		Session: cfg.Session,
		Yes:     cfg.Yes,
		PermInternet: cfg.PermInternet,
		Verbose:     cfg.Verbose,
		FirecrawlKey: os.Getenv("FIRECRAWL_API_KEY"),
	})

	if cfg.Interactive || cfg.Terminal {
		if cfg.Terminal {
			return ag.Terminal()
		}
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

	return ag.Execute(task, false)
}

func runWeb(port string, lan bool) error {
	exe, _ := os.Executable()
	webDir := filepath.Dir(exe) + "/web"
	http.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir(webDir+"/static"))))

	http.HandleFunc("/config", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		home, _ := os.UserHomeDir()
		data, err := os.ReadFile(filepath.Join(home, ".eva", "config.json"))
		if err != nil {
			json.NewEncoder(w).Encode(map[string]any{})
			return
		}
		w.Write(data)
	})

	http.HandleFunc("/config/save", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			if err := r.ParseForm(); err != nil {
				http.Error(w, "bad request", 400)
				return
			}
			home, _ := os.UserHomeDir()
			os.MkdirAll(filepath.Join(home, ".eva"), 0755)
			cfg := map[string]any{
				"firecrawl":     r.FormValue("firecrawl"),
				"perm_internet": r.FormValue("perm_internet") == "true",
				"perm_read":     r.FormValue("perm_read") == "true",
				"perm_write":    r.FormValue("perm_write") == "true",
				"perm_delete":   r.FormValue("perm_delete") == "true",
				"perm_exec":     r.FormValue("perm_exec") == "true",
				"perm_root":     r.FormValue("perm_root") == "true",
				"verbose":       r.FormValue("verbose") == "true",
			}
			data, _ := json.Marshal(cfg)
			os.WriteFile(filepath.Join(home, ".eva", "config.json"), data, 0644)
			w.Write([]byte(`{"ok":true}`))
		}
	})

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			http.ServeFile(w, r, webDir+"/index.html")
			return
		}
		if r.Method == "POST" {
			cmd := r.FormValue("command")
			model := r.FormValue("model")
			gateway := r.FormValue("gateway")
			firecrawl := r.FormValue("firecrawl")
			permInternet := r.FormValue("perm_internet") == "true"
			permRead := r.FormValue("perm_read") == "true"
			permWrite := r.FormValue("perm_write") == "true"
			permDelete := r.FormValue("perm_delete") == "true"
			permExec := r.FormValue("perm_exec") == "true"
			permRoot := r.FormValue("perm_root") == "true"
			verbose := r.FormValue("verbose") == "true"
			cfg := agent.Config{Yes: true, PermInternet: true, Verbose: verbose}
			if model != "" {
				cfg.Model = model
			}
			if gateway != "" {
				cfg.Gateway = gateway
			}
			if firecrawl != "" {
				cfg.FirecrawlKey = firecrawl
			}
			cfg.PermInternet = permInternet
			cfg.PermRead = permRead
			cfg.PermWrite = permWrite
			cfg.PermDelete = permDelete
			cfg.PermExec = permExec
			cfg.PermRoot = permRoot
			ag := agent.New(cfg)
			err := ag.Execute(cmd, false)

			var out string
			if err != nil {
				out = err.Error()
			} else {
				out = ag.GetOutput()
			}

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"output": out})
			return
		}
	})

	http.HandleFunc("/logs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		cmd := exec.Command("journalctl", "-u", "eva", "-f", "-n", "50")
		stdout, _ := cmd.StdoutPipe()
		cmd.Start()
		defer cmd.Process.Kill()

		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			fmt.Fprintf(w, "data: %s\n\n", scanner.Text())
			w.(http.Flusher).Flush()
		}
	})

	http.HandleFunc("/gwlogs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		cmd := exec.Command("journalctl", "-u", "aigatiator", "-f", "-n", "50")
		stdout, _ := cmd.StdoutPipe()
		cmd.Start()
		defer cmd.Process.Kill()

		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			fmt.Fprintf(w, "data: %s\n\n", scanner.Text())
			w.(http.Flusher).Flush()
		}
	})

	http.HandleFunc("/files/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			path := r.URL.Query().Get("path")
			if path == "" {
				path = "."
			}
			data, err := os.ReadFile(path)
			if err == nil {
				w.Header().Set("Content-Type", "text/plain; charset=utf-8")
				w.Write(data)
				return
			}
			entries, err := os.ReadDir(path)
			if err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			var files []map[string]any
			for _, e := range entries {
				info, _ := e.Info()
				files = append(files, map[string]any{
					"name":  e.Name(),
					"isDir": e.IsDir(),
					"size":  info.Size(),
				})
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(files)
			return
		}

		if r.Method == "PUT" || r.Method == "POST" {
			path := r.FormValue("path")
			content := r.FormValue("content")
			err := os.WriteFile(path, []byte(content), 0644)
			if err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		}
	})

	host := "localhost"
	if lan {
		host = ""
	}
	log.Printf("EVA Web http://localhost:%s", port)
	return http.ListenAndServe(host+":"+port, nil)
}

func installService(port string) error {
	if runtime.GOOS != "linux" {
		log.Fatal("A instalação como serviço só é suportada no Linux.")
	}

	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("erro ao obter caminho do executável: %w", err)
	}
	execPath, _ = filepath.Abs(execPath)
	workDir := filepath.Dir(execPath)

	username := os.Getenv("SUDO_USER")
	if username == "" {
		username = "root"
	}

	service := fmt.Sprintf(`[Unit]
Description=EVA AI Agent Web
After=network.target

[Service]
Type=simple
User=%s
WorkingDirectory=%s
ExecStart=%s -web -lan -port %s
Restart=always
RestartSec=3

[Install]
WantedBy=multi-user.target
`, username, workDir, execPath, port)

	path := "/etc/systemd/system/eva.service"
	if err := os.WriteFile(path, []byte(service), 0644); err != nil {
		return fmt.Errorf("write service file: %w", err)
	}

	fmt.Printf("Service installed: %s\n", path)
	return nil
}

func printHelp() {
	fmt.Println(`EVA - AI Agent CLI

Usage:
  eva -e "command"     Execute task
  eva -i              Interactive mode
  eva -term           Terminal mode (SSH/WSL)
  eva -web            Web UI
  eva -setup          Setup API keys

Options:
  -e string    Execute task
  -ef file     Execute from file
  -i           Interactive mode
  -term        Terminal mode
  -web         Web UI
  -lan         Serve on LAN (all interfaces)
  -port        Web port (default 11313)
  -install     Install as systemd service
  -y           Skip confirmation
  -h           Help`)
}

func setupAPIKeys() error {
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".eva")
	os.MkdirAll(dir, 0755)
	path := filepath.Join(dir, ".env")

	var existing string
	if data, err := os.ReadFile(path); err == nil {
		existing = string(data)
	}

	fmt.Print("Firecrawl API Key (fc-...): ")
	reader := bufio.NewReader(os.Stdin)
	key, _ := reader.ReadString('\n')
	key = strings.TrimSpace(key)

	if key == "" && existing != "" {
		fmt.Println("Using existing key")
		return nil
	}

	if key == "" {
		return fmt.Errorf("no key provided")
	}

	env := fmt.Sprintf("FIRECRAWL_API_KEY=%s", key)
	if existing != "" && !strings.Contains(existing, "FIRECRAWL_API_KEY") {
		env = existing + "\n" + env
	}

	os.WriteFile(path, []byte(env), 0644)
	fmt.Println("API key saved to", path)
	return nil
}

func main() {
	if err := Run(os.Args); err != nil {
		log.Fatal(err)
	}
}
