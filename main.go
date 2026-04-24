// EVA CLI with Web UI
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"

	"github.com/eva/agent/internal/agent"
)

type Config struct {
	Execute     string
	ExecuteFile string
	Model       string
	Interactive bool
	Web         bool
	Lan         bool
	Port        string
	Install     bool
	Session     bool
	Yes         bool
	Help        bool
}

func parseArgs(args []string) (Config, error) {
	cfg := Config{Port: "11313"}

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
	fs.BoolVar(&cfg.Web, "web", false, "Web UI mode")
	fs.StringVar(&cfg.Port, "port", "11313", "Web UI port")
	fs.BoolVar(&cfg.Lan, "lan", false, "Serve on all interfaces")
	fs.BoolVar(&cfg.Install, "install", false, "Install as systemd service")
	fs.BoolVar(&cfg.Session, "session", false, "Enable session file")
	fs.BoolVar(&cfg.Yes, "y", false, "Skip confirmation prompt")

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

	if cfg.Help {
		printHelp()
		return nil
	}

	if cfg.Execute == "" && cfg.ExecuteFile == "" && !cfg.Interactive && !cfg.Web {
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

	return ag.Execute(task, false)
}

func runWeb(port string, lan bool) error {
	tmpl := template.Must(template.New("index").Parse(indexHTML))

http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			tmpl.Execute(w, nil)
			return
		}

		if r.Method == "POST" {
			cmd := r.FormValue("command")
			auto := r.FormValue("auto") == "on"

			ag := agent.New(agent.Config{Yes: true}) // Web mode always auto-confirms
			err := ag.Execute(cmd, false)

			var out string
			if err != nil {
				out = err.Error()
			} else {
				// Get output from session
				out = ag.GetOutput()
			}

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"output": out})
			return
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
					"name": e.Name(),
					"isDir": e.IsDir(),
					"size": info.Size(),
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
	exec, err := os.Executable()
	if err != nil {
		return fmt.Errorf("get executable: %w", err)
	}

	service := fmt.Sprintf(`[Unit]
Description=EVA AI Agent Web
After=network.target

[Service]
Type=simple
ExecStart=%s -web -port %s
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
`, exec, port)

	path := "/etc/systemd/system/eva.service"
	if err := os.WriteFile(path, []byte(service), 0644); err != nil {
		return fmt.Errorf("write service file: %w", err)
	}

	fmt.Printf("Service installed: %s\n", path)
	fmt.Println("Run: sudo systemctl daemon-reload && sudo systemctl enable eva")
	return nil
}

func printHelp() {
	fmt.Println(`EVA - AI Agent CLI

Usage:
  eva -e "command"     Execute task
  eva -i              Interactive mode
  eva -web            Web UI

Options:
  -e string    Execute task
  -ef file     Execute from file
  -i           Interactive mode
  -web         Web UI
  -lan         Serve on LAN (all interfaces)
  -port        Web port (default 11313)
  -install     Install as systemd service
  -y           Skip confirmation
  -h           Help`)
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
		#output, #editor { background: #16213e; padding: 20px; border-radius: 4px; white-space: pre-wrap; margin-top: 20px; min-height: 200px; }
		#output { display: none }
		#editor { display: none; font-family: monospace; width: 100%; color: #fff; }
		.files { display: grid; grid-template-columns: 1fr 1fr; gap: 10px }
		.file { background: #16213e; padding: 10px; border-radius: 4px; cursor: pointer }
		.file:hover { background: #1f2f4f }
		.dir { color: #00d4ff }
		.hidden { display: none }
	</style>
</head>
<body>
	<div class="container">
		<h1>EVA 🤖</h1>
		<div class="tabs">
			<button class="tab active" data-tab="cmd">Command</button>
			<button class="tab" data-tab="files">Files</button>
		</div>
		<div id="cmd-tab">
			<form id="cmd-form">
				<input class="cmd" name="command" placeholder="Type command..." autofocus>
				<button>Run</button>
			</form>
			<label><input type="checkbox" name="auto"> Auto-confirm</label>
			<div id="output"></div>
		</div>
		<div id="files-tab" class="hidden">
			<button id="refresh-btn">Refresh</button>
			<div id="path"></div>
			<div class="files" id="files"></div>
			<textarea id="editor" rows="20"></textarea>
			<button id="save-btn" class="hidden">Save</button>
		</div>
	</div>
	<script>
		let currentPath = '.';
		let currentFile = '';
		document.querySelectorAll('.tab').forEach(t => {
			t.addEventListener('click', () => {
				document.querySelectorAll('.tab').forEach(x => x.classList.remove('active'));
				t.classList.add('active');
				const tab = t.dataset.tab;
				document.getElementById('cmd-tab').classList.toggle('hidden', tab !== 'cmd');
				document.getElementById('files-tab').classList.toggle('hidden', tab !== 'files');
				if (tab === 'files') loadFiles(currentPath);
			});
		});
		document.getElementById('cmd-form').addEventListener('submit', async e => {
			e.preventDefault();
			const res = await fetch('/', { method: 'POST', body: new FormData(e.target) });
			const data = await res.json();
			document.getElementById('output').textContent = data.output;
			document.getElementById('output').style.display = 'block';
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
	</script>
</body>
</html>`

func main() {
	if err := Run(os.Args); err != nil {
		log.Fatal(err)
	}
}