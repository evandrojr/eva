// EVA CLI with Web UI
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
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
	tmpl := template.Must(template.New("index").Parse(indexHTML))

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
				"firecrawl":    r.FormValue("firecrawl"),
				"perm_internet": r.FormValue("perm_internet") == "true",
				"perm_read":    r.FormValue("perm_read") == "true",
				"perm_write":   r.FormValue("perm_write") == "true",
				"perm_delete":  r.FormValue("perm_delete") == "true",
				"perm_exec":   r.FormValue("perm_exec") == "true",
				"perm_root":   r.FormValue("perm_root") == "true",
			}
			data, _ := json.Marshal(cfg)
			os.WriteFile(filepath.Join(home, ".eva", "config.json"), data, 0644)
			w.Write([]byte(`{"ok":true}`))
		}
	})

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			tmpl.Execute(w, nil)
			return
		}

		if r.Method == "POST" {
			cmd := r.FormValue("command")
			autoConfirm := r.FormValue("auto") == "on"
			model := r.FormValue("model")
			gateway := r.FormValue("gateway")
			firecrawl := r.FormValue("firecrawl")
			permInternet := r.FormValue("perm_internet") == "true"
			permRead := r.FormValue("perm_read") == "true"
			permWrite := r.FormValue("perm_write") == "true"
			permDelete := r.FormValue("perm_delete") == "true"
			permExec := r.FormValue("perm_exec") == "true"
			permRoot := r.FormValue("perm_root") == "true"
			cfg := agent.Config{Yes: autoConfirm, PermInternet: true}
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

var indexHTML = `
<!DOCTYPE html>
<html>
<head>
        <meta charset="UTF-8">
        <title>EVA</title>
        <style>
                * { box-sizing: border-box }
                body { font-family: system-ui; background: #1a1a2e; color: #eee; padding: 20px }
                .container { max-width: 900px; margin: 0 auto }
                h1 { color: #00d4ff; margin-bottom: 20px }
                .tabs { display: flex; gap: 10px; margin-bottom: 20px }
                .tab { padding: 10px 20px; background: #16213e; border: none; color: #fff; cursor: pointer; border-radius: 4px }
                .tab.active { background: #00d4ff; color: #1a1a2e }
                .cmd { width: 100%; padding: 15px; border-radius: 4px; border: none; margin-bottom: 10px }
                button { padding: 12px 24px; border-radius: 4px; border: none; background: #00d4ff; color: #1a1a2e; cursor: pointer }
                #output, #editor, #logs { background: #16213e; padding: 20px; border-radius: 4px; white-space: pre-wrap; margin-top: 20px; }
                #logs { display: none; height: 300px; overflow-y: auto; font-family: monospace; font-size: 12px; border: 1px solid #333; }
                #editor { display: none; font-family: monospace; width: 100%; color: #fff; }
                .footer { margin-top: 40px; padding-top: 20px; border-top: 1px solid #333; display: flex; align-items: center; gap: 10px; }
                
                /* Switch Style */
                .switch { position: relative; display: inline-block; width: 40px; height: 20px; }
                .switch input { opacity: 0; width: 0; height: 0; }
                .slider { position: absolute; cursor: pointer; top: 0; left: 0; right: 0; bottom: 0; background-color: #333; transition: .4s; border-radius: 20px; }
                .slider:before { position: absolute; content: ""; height: 16px; width: 16px; left: 2px; bottom: 2px; background-color: white; transition: .4s; border-radius: 50%; }
                input:checked + .slider { background-color: #00d4ff; }
                input:checked + .slider:before { transform: translateX(20px); }
                .hidden { display: none }
                .files { display: grid; grid-template-columns: 1fr 1fr; gap: 10px }
                .file { background: #16213e; padding: 10px; border-radius: 4px; cursor: pointer }
                .file:hover { background: #1f2f4f }
                .dir { color: #00d4ff }
                #ai-messages { height: 400px; overflow-y: auto; background: #16213e; padding: 15px; border-radius: 4px; margin-top: 10px; }
                .ai-user { color: #00d4ff; font-weight: bold; margin: 10px 0; }
                .ai-assistant { color: #90EE90; margin: 10px 0; white-space: pre-wrap; }
                .ai-tool-call { color: #ffaa00; font-family: monospace; font-size: 12px; margin: 5px 0; }
                #gateway-logs { font-family: monospace; font-size: 11px; color: #888; margin-top: 20px; border-top: 1px solid #333; padding-top: 10px; }
        </style>
</head>
<body>
        <div class="container">
                <h1>EVA 🤖</h1>
                <div class="tabs">
                        <button class="tab active" data-tab="cmd">Command + AI</button>
                        <button class="tab" data-tab="files">Files</button>
                        <button class="tab" data-tab="settings">Settings</button>
                </div>
                <div id="cmd-tab">
                        <form id="cmd-form">
                                <input class="cmd" name="command" placeholder="Type command..." autofocus>
                                <button>Run</button>
                        </form>
                        <label><input type="checkbox" name="auto"> Auto-confirm</label>
                        <div id="ai-messages"></div>
                </div>
                <div id="files-tab" class="hidden">
                        <button id="refresh-btn">Refresh</button>
                        <div id="path"></div>
                        <div class="files" id="files"></div>
                        <textarea id="editor" rows="20"></textarea>
                        <button id="save-btn" class="hidden">Save</button>
                </div>
                <div id="settings-tab" class="hidden">
                        <div style="margin-bottom: 15px">
                                <label style="display:block;margin-bottom:5px">Model</label>
                                <input class="cmd" id="settings-model" placeholder="e.g. opencode, gpt-4o, claude-3-5-sonnet-20241022">
                        </div>
                        <div style="margin-bottom: 15px">
                                <label style="display:block;margin-bottom:5px">Gateway URL</label>
                                <input class="cmd" id="settings-gateway" placeholder="http://localhost:1313/v1/chat/completions">
                        </div>
                        <div style="margin-bottom: 15px">
                                <label style="display:block;margin-bottom:5px">Firecrawl API Key</label>
                                <input class="cmd" id="settings-firecrawl" placeholder="fc-...">
                        </div>
                        <div style="margin-bottom: 15px">
                                <label style="display:block;margin-bottom:5px">Permissões</label>
                                <div style="display:flex;flex-wrap:wrap;gap:10px">
                                        <label><input type="checkbox" id="perm-internet"> Internet</label>
                                        <label><input type="checkbox" id="perm-read"> Ler Arquivos</label>
                                        <label><input type="checkbox" id="perm-write"> Escrever</label>
                                        <label><input type="checkbox" id="perm-delete"> Apagar</label>
                                        <label><input type="checkbox" id="perm-exec"> Executar Shell</label>
                                        <label><input type="checkbox" id="perm-root"> Root</label>
                                </div>
                        </div>
                        <button id="save-settings-btn">Save Settings</button>
                </div>

                <div id="logs"></div>
                <div id="gateway-logs"></div>

                <div class="footer">
                        <span>Logs EVA</span>
                        <label class="switch">
                                <input type="checkbox" id="log-toggle">
                                <span class="slider"></span>
                        </label>
                        <span style="margin-left: 20px">AI Gateway</span>
                        <label class="switch">
                                <input type="checkbox" id="gwlog-toggle">
                                <span class="slider"></span>
                        </label>
                </div>
        </div>
        <script>
                let currentPath = '.';
                let currentFile = '';
                let logSource = null;

                document.getElementById('log-toggle').addEventListener('change', e => {
                        const logs = document.getElementById('logs');
                        if (e.target.checked) {
                                logs.style.display = 'block';
                                startLogs();
                        } else {
                                logs.style.display = 'none';
                                stopLogs();
                        }
                });

                function startLogs() {
                        if (logSource) return;
                        logSource = new EventSource('/logs');
                        logSource.onmessage = e => {
                                const logs = document.getElementById('logs');
                                logs.textContent += e.data + '\n';
                                logs.scrollTop = logs.scrollHeight;
                        };
                }

                function stopLogs() {
                        if (logSource) {
                                logSource.close();
                                logSource = null;
                        }
                }

                document.querySelectorAll('.tab').forEach(t => {
                        t.addEventListener('click', () => {
                                document.querySelectorAll('.tab').forEach(x => x.classList.remove('active'));
                                t.classList.add('active');
                                const tab = t.dataset.tab;
                                document.getElementById('cmd-tab').classList.toggle('hidden', tab !== 'cmd');
                                document.getElementById('files-tab').classList.toggle('hidden', tab !== 'files');
                                document.getElementById('settings-tab').classList.toggle('hidden', tab !== 'settings');
                                if (tab === 'files') loadFiles(currentPath);
                        });
                });
                let gwLogSource = null;
                document.getElementById('gwlog-toggle').addEventListener('change', e => {
                        const gwLogs = document.getElementById('gateway-logs');
                        if (e.target.checked) {
                                gwLogs.style.display = 'block';
                                startGwLogs();
                        } else {
                                gwLogs.style.display = 'none';
                                stopGwLogs();
                        }
                });
                function startGwLogs() {
                        if (gwLogSource) return;
                        gwLogSource = new EventSource('/gwlogs');
                        gwLogSource.onmessage = e => {
                                const gwLogs = document.getElementById('gateway-logs');
                                gwLogs.textContent += e.data + '\n';
                                gwLogs.scrollTop = gwLogs.scrollHeight;
                        };
                }
                function stopGwLogs() {
                        if (gwLogSource) {
                                gwLogSource.close();
                                gwLogSource = null;
                        }
                }
                document.getElementById('cmd-form').addEventListener('submit', async e => {
                        e.preventDefault();
                        const btn = e.target.querySelector('button');
                        const input = e.target.querySelector('input[name="command"]').value;
                        btn.disabled = true;
                        btn.textContent = 'Running...';
                        const formData = new FormData(e.target);
                        const savedModel = localStorage.getItem('eva_model');
                        const savedGateway = localStorage.getItem('eva_gateway');
                        const savedFirecrawl = localStorage.getItem('eva_firecrawl');
                        if (savedModel) formData.append('model', savedModel);
                        if (savedGateway) formData.append('gateway', savedGateway);
                        if (savedFirecrawl) formData.append('firecrawl', savedFirecrawl);
                        formData.append('perm_internet', localStorage.getItem('eva_perm_internet') || 'false');
                        formData.append('perm_read', localStorage.getItem('eva_perm_read') || 'false');
                        formData.append('perm_write', localStorage.getItem('eva_perm_write') || 'false');
                        formData.append('perm_delete', localStorage.getItem('eva_perm_delete') || 'false');
                        formData.append('perm_exec', localStorage.getItem('eva_perm_exec') || 'false');
                        formData.append('perm_root', localStorage.getItem('eva_perm_root') || 'false');
                        try {
                                const res = await fetch('/', { method: 'POST', body: formData });
                                const data = await res.json();
                                
                                // Add to AI chat in same tab
                                const aiMsgs = document.getElementById('ai-messages');
                                aiMsgs.innerHTML += '<div class="ai-user">User: ' + input + '</div>';
                                aiMsgs.innerHTML += '<div class="ai-assistant">' + data.output.replace(/\x1b\[[0-9;]*m/g, '') + '</div>';
                                aiMsgs.scrollTop = aiMsgs.scrollHeight;
                                e.target.querySelector('input[name="command"]').value = '';
                        } finally {
                                btn.disabled = false;
                                btn.textContent = 'Run';
                        }
                });
                async function loadFiles(path) {
                        currentPath = path;
                        const res = await fetch('/files/?path=' + encodeURIComponent(path));
                        const files = await res.json();
                        document.getElementById('path').textContent = path;
                        const es = document.getElementById('files');
                        es.innerHTML = '';
                        files.forEach(f => {
                                const d = document.createElement('div');
                                d.className = 'file' + (f.isDir ? ' dir' : '');
                                d.textContent = f.name;
                                d.onclick = () => { if (f.isDir) loadFiles(f.name); else openFile(f.name); };
                                es.appendChild(d);
                        });
                        document.getElementById('editor').style.display = 'none';
                        document.getElementById('save-btn').classList.add('hidden');
                }
                async function openFile(name) {
                        currentFile = name;
                        const res = await fetch('/files/?path=' + encodeURIComponent(name));
                        const content = await res.text();
                        document.getElementById('editor').value = content;
                        document.getElementById('editor').style.display = 'block';
                        document.getElementById('save-btn').classList.remove('hidden');
                }
                document.getElementById('save-btn').addEventListener('click', async () => {
                        const content = document.getElementById('editor').value;
                        await fetch('/files/', { method: 'POST', body: 'path=' + currentFile + '&content=' + encodeURIComponent(content) });
                        alert('Saved!');
                });
                document.getElementById('refresh-btn').addEventListener('click', () => loadFiles(currentPath));

                async function loadServerConfig() {
                        try {
                                const res = await fetch('/config');
                                const cfg = await res.json();
                                if (!localStorage.getItem('eva_firecrawl') && cfg.firecrawl) {
                                        document.getElementById('settings-firecrawl').value = cfg.firecrawl;
                                        localStorage.setItem('eva_firecrawl', cfg.firecrawl);
                                }
                                if (!localStorage.getItem('eva_perm_internet') && cfg.perm_internet) {
                                        document.getElementById('perm-internet').checked = cfg.perm_internet;
                                        localStorage.setItem('eva_perm_internet', cfg.perm_internet);
                                }
                                if (!localStorage.getItem('eva_perm_read') && cfg.perm_read) {
                                        document.getElementById('perm-read').checked = cfg.perm_read;
                                        localStorage.setItem('eva_perm_read', cfg.perm_read);
                                }
                                if (!localStorage.getItem('eva_perm_write') && cfg.perm_write) {
                                        document.getElementById('perm-write').checked = cfg.perm_write;
                                        localStorage.setItem('eva_perm_write', cfg.perm_write);
                                }
                                if (!localStorage.getItem('eva_perm_delete') && cfg.perm_delete) {
                                        document.getElementById('perm-delete').checked = cfg.perm_delete;
                                        localStorage.setItem('eva_perm_delete', cfg.perm_delete);
                                }
                                if (!localStorage.getItem('eva_perm_exec') && cfg.perm_exec) {
                                        document.getElementById('perm-exec').checked = cfg.perm_exec;
                                        localStorage.setItem('eva_perm_exec', cfg.perm_exec);
                                }
                                if (!localStorage.getItem('eva_perm_root') && cfg.perm_root) {
                                        document.getElementById('perm-root').checked = cfg.perm_root;
                                        localStorage.setItem('eva_perm_root', cfg.perm_root);
                                }
                        } catch(e) {}
                }
                loadServerConfig();
                
                if (localStorage.getItem('eva_model')) {
                        document.getElementById('settings-model').value = localStorage.getItem('eva_model');
                }
                if (localStorage.getItem('eva_gateway')) {
                        document.getElementById('settings-gateway').value = localStorage.getItem('eva_gateway');
                }
                if (localStorage.getItem('eva_firecrawl')) {
                        document.getElementById('settings-firecrawl').value = localStorage.getItem('eva_firecrawl');
                }
                if (localStorage.getItem('eva_perm_internet') === 'true') document.getElementById('perm-internet').checked = true;
                if (localStorage.getItem('eva_perm_read') === 'true') document.getElementById('perm-read').checked = true;
                if (localStorage.getItem('eva_perm_write') === 'true') document.getElementById('perm-write').checked = true;
                if (localStorage.getItem('eva_perm_delete') === 'true') document.getElementById('perm-delete').checked = true;
                if (localStorage.getItem('eva_perm_exec') === 'true') document.getElementById('perm-exec').checked = true;
                if (localStorage.getItem('eva_perm_root') === 'true') document.getElementById('perm-root').checked = true;
                document.getElementById('save-settings-btn').addEventListener('click', () => {
                        const model = document.getElementById('settings-model').value;
                        const gateway = document.getElementById('settings-gateway').value;
                        const firecrawl = document.getElementById('settings-firecrawl').value;
                        if (model) localStorage.setItem('eva_model', model);
                        if (gateway) localStorage.setItem('eva_gateway', gateway);
                        if (firecrawl) localStorage.setItem('eva_firecrawl', firecrawl);
                        localStorage.setItem('eva_perm_internet', document.getElementById('perm-internet').checked);
                        localStorage.setItem('eva_perm_read', document.getElementById('perm-read').checked);
                        localStorage.setItem('eva_perm_write', document.getElementById('perm-write').checked);
                        localStorage.setItem('eva_perm_delete', document.getElementById('perm-delete').checked);
                        localStorage.setItem('eva_perm_exec', document.getElementById('perm-exec').checked);
                        localStorage.setItem('eva_perm_root', document.getElementById('perm-root').checked);
                        const formData = new FormData();
                        formData.append('firecrawl', document.getElementById('settings-firecrawl').value);
                        formData.append('perm_internet', document.getElementById('perm-internet').checked);
                        formData.append('perm_read', document.getElementById('perm-read').checked);
                        formData.append('perm_write', document.getElementById('perm-write').checked);
                        formData.append('perm_delete', document.getElementById('perm-delete').checked);
                        formData.append('perm_exec', document.getElementById('perm-exec').checked);
                        formData.append('perm_root', document.getElementById('perm-root').checked);
                        fetch('/config/save', { method: 'POST', body: formData });
                        alert('Settings saved!');
                });
        </script>
</body>
</html>`

func main() {
	if err := Run(os.Args); err != nil {
		log.Fatal(err)
	}
}
