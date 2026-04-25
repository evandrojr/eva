package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/peterh/liner"
)

var stdinReader *bufio.Reader

var globalReader *bufio.Reader

func init() {
	globalReader = bufio.NewReader(os.Stdin)
	loadEnv()
	loadConfig()
}

func loadConfig() {
	home, _ := os.UserHomeDir()
	path := filepath.Join(home, ".eva", "config.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		return
	}
	if key, ok := cfg["firecrawl"].(string); ok && key != "" && os.Getenv("FIRECRAWL_API_KEY") == "" {
		os.Setenv("FIRECRAWL_API_KEY", key)
	}
}

func loadEnv() {
	home, _ := os.UserHomeDir()
	path := filepath.Join(home, ".eva", ".env")
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") || line == "" {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			os.Setenv(parts[0], parts[1])
		}
	}
}

const GatewayURL = "http://localhost:1313/v1/chat/completions"

type Config struct {
	Model       string
	Gateway    string
	FirecrawlKey string
	Session    bool
	SessionPath string
	Yes        bool
	Interactive bool
	PermInternet bool
	PermRead   bool
	PermWrite  bool
	PermDelete bool
	PermExec   bool
	PermRoot   bool
}

type Message struct {
	Role               string      `json:"role"`
	Content            string      `json:"content,omitempty"`
	ToolCalls          []ToolCall  `json:"tool_calls,omitempty"`
	ToolCallID         string     `json:"tool_call_id,omitempty"`
	ToolCallFunction  string     `json:"tool_call_function,omitempty"`
	ToolCallResult    string     `json:"tool_call_result,omitempty"`
}

type ToolCall struct {
	ID       string      `json:"id"`
	Type     string      `json:"type"`
	Function ToolFunction `json:"function"`
}

type ToolFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type Agent struct {
	cfg      Config
	messages []Message
	output   bytes.Buffer
}

func New(cfg Config) *Agent {
	a := &Agent{cfg: cfg, messages: []Message{}, output: bytes.Buffer{}}
	if cfg.Session {
		a.loadSession()
	}
	return a
}

func (a *Agent) GetOutput() string {
	return a.output.String()
}

func (a *Agent) writeOutput(format string, args ...any) {
	s := fmt.Sprintf(format, args...)
	a.output.WriteString(s)
	fmt.Print(s)
}

func (a *Agent) loadSession() {
	path := a.cfg.SessionPath
	if path == "" {
		home, _ := os.UserHomeDir()
		path = filepath.Join(home, ".eva", "session.json")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	json.Unmarshal(data, &a.messages)
}

func (a *Agent) saveSession() {
	path := a.cfg.SessionPath
	if path == "" {
		home, _ := os.UserHomeDir()
		path = filepath.Join(home, ".eva", "session.json")
	}
	os.MkdirAll(filepath.Dir(path), 0755)
	data, _ := json.Marshal(a.messages)
	os.WriteFile(path, data, 0644)
}

func (a *Agent) messagesForRequest() []map[string]any {
	var result []map[string]any
	for _, m := range a.messages {
		msg := map[string]any{"role": m.Role}
		if m.Content != "" {
			msg["content"] = m.Content
		}
		if len(m.ToolCalls) > 0 {
			msg["tool_calls"] = m.ToolCalls
		}
		result = append(result, msg)
	}
	return result
}

var tools = []map[string]any{
	{
		"type": "function",
		"function": map[string]any{
			"name":        "get_time",
			"description": "Get current time for any city. Uses worldtimeapi.org. Example: city=\"Salvador, Brazil\"",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"city": map[string]any{"type": "string", "description": "City name and country"},
				},
				"required": []string{"city"},
			},
		},
	},
	{
		"type": "function",
		"function": map[string]any{
			"name":        "websearch",
			"description": "Search the web for information",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{"type": "string", "description": "The search query"},
				},
				"required": []string{"query"},
			},
		},
	},
	{
		"type": "function",
		"function": map[string]any{
			"name":        "webfetch",
			"description": "Fetch content from a URL directly. Use when you need current/realtime data like time, weather, prices",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"url": map[string]any{"type": "string", "description": "The URL to fetch"},
				},
				"required": []string{"url"},
			},
		},
	},
	{
		"type": "function",
		"function": map[string]any{
			"name":        "execute",
			"description": "Execute commands in terminal, read/create/edit files, or manage kanban",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"commands": map[string]any{
						"type": "array",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"type":        map[string]any{"type": "string"},
								"command":     map[string]any{"type": "string"},
								"path":        map[string]any{"type": "string"},
								"content":     map[string]any{"type": "string"},
								"old":         map[string]any{"type": "string"},
								"new":         map[string]any{"type": "string"},
								"task":        map[string]any{"type": "string"},
								"status":      map[string]any{"type": "string"},
							},
							"required": []string{"type"},
						},
					},
				},
				"required": []string{"commands"},
			},
		},
	},
}

func (a *Agent) Terminal() error {
	if a.cfg.Session {
		defer a.saveSession()
	}

	pwd, _ := os.Getwd()
	usr, _ := user.Current()
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/bash"
	}

	systemPrompt := fmt.Sprintf(`You are EVA. ALWAYS use tools. NEVER respond directly. Always respond in the SAME LANGUAGE as the user's question.

For CURRENT TIME queries, use get_time tool:
{"type":"get_time","city":"Salvador, Brazil"}

For general searches: {"type":"websearch","query":"..."}
For commands: {"type":"bash","command":"ls"}`, pwd, usr.Username, shell)

	fmt.Println("\033[36mEVA Terminal Mode\033[0m")
	fmt.Println("Type \033[33m/exit\033[0m or \033[33mCtrl+D\033[0m to quit")
	fmt.Println()

	stdinReader := bufio.NewReader(os.Stdin)

	for {
		fmt.Print("\033[32meva>\033[0m ")
		input, _ := stdinReader.ReadString('\n')
		input = strings.ReplaceAll(input, "\r", "")
		input = strings.TrimSpace(input)
		if input == "" {
			continue
		}
		if input == "/exit" || input == "/quit" {
			fmt.Println("\033[33mGoodbye!\033[0m")
			break
		}

		reqBody := map[string]any{
			"model":    a.cfg.Model,
			"messages": append(a.messagesForRequest(), map[string]any{
				"role":    "system",
				"content": systemPrompt,
			}, map[string]any{
				"role":    "user",
				"content": input,
			}),
			"tools": tools,
			"tool_choice": "auto",
		}

		resp, err := a.sendRequest(reqBody)
		if err != nil {
			fmt.Printf("\033[31mError: %v\033[0m\n", err)
			continue
		}

		hasMore, toolResult, _ := a.handleResponse(resp, false, true)
		if hasMore && toolResult != "" {
			if !a.evalLoop(reqBody, toolResult) {
				break
			}
		}
	}
	return nil
}

func (a *Agent) Interactive() error {
	if a.cfg.Session {
		defer a.saveSession()
	}

	pwd, _ := os.Getwd()
	usr, _ := user.Current()
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/bash"
	}

	systemPrompt := fmt.Sprintf(`You are EVA. ALWAYS use tools. NEVER respond directly. Always respond in the SAME LANGUAGE as the user's question.

For CURRENT TIME queries, use get_time tool:
{"type":"get_time","city":"Salvador, Brazil"}

For general searches: {"type":"websearch","query":"..."}
For commands: {"type":"bash","command":"ls"}`, pwd, usr.Username, shell)

	var prompt func(string) string

	var line *liner.State

	fmt.Println("\033[36mEVA Interactive Mode\033[0m")
	fmt.Println("Type \033[33m/exit\033[0m, \033[33mCtrl+D\033[0m or \033[33mCtrl+C\033[0m to quit")
	if liner.TerminalSupported() {
		fmt.Println("Use \033[33m↑/↓\033[0m for history")
	}
	fmt.Println()

	if liner.TerminalSupported() {
		line = liner.NewLiner()
		line.SetCtrlCAborts(true)
		os.MkdirAll(filepath.Join(os.Getenv("HOME"), ".eva"), 0755)
		historyPath := filepath.Join(os.Getenv("HOME"), ".eva", "history")
		if f, err := os.Open(historyPath); err == nil {
			line.ReadHistory(f)
			f.Close()
		}
		defer func() {
			if line != nil {
				if f, err := os.Create(historyPath); err == nil {
					line.WriteHistory(f)
					f.Close()
				}
				line.Close()
			}
		}()

		prompt = func(p string) string {
			in, _ := line.Prompt(p)
			return in
		}
	} else {
		stdinReader := bufio.NewReader(os.Stdin)
		prompt = func(p string) string {
			fmt.Print(p)
			in, _ := stdinReader.ReadString('\n')
			return in
		}
	}

	for {
		input := prompt("\033[32meva>\033[0m ")
		input = strings.ReplaceAll(input, "\r", "")
		input = strings.TrimSpace(input)
		if input == "" {
			continue
		}
		if input == "/exit" || input == "/quit" {
			fmt.Println("\033[33mGoodbye!\033[0m")
			break
		}

		reqBody := map[string]any{
			"model":   a.cfg.Model,
			"messages": append(a.messagesForRequest(), map[string]any{
				"role":    "system",
				"content": systemPrompt,
			}, map[string]any{
				"role":    "user",
				"content": input,
			}),
			"tools":   tools,
			"tool_choice": "auto",
		}

		resp, err := a.sendRequest(reqBody)
		if err != nil {
			fmt.Printf("\033[31mError: %v\033[0m\n", err)
			continue
		}

		a.messages = append(a.messages, Message{Role: "user", Content: input})

		if hasMore, toolResult, err := a.handleResponse(resp, true, a.cfg.Yes); err != nil {
			fmt.Printf("\033[31mError: %v\033[0m\n", err)
		} else if hasMore && toolResult != "" {
			if !a.evalLoop(reqBody, toolResult) {
				break
			}
		}
	}

	return nil
}

func (a *Agent) Execute(task string, interactive bool) error {
	a.output.Reset()
	pwd, _ := os.Getwd()
	usr, _ := user.Current()
	shell := os.Getenv("SHELL")
if shell == "" {
		shell = "/bin/bash"
	}

	systemPrompt := fmt.Sprintf(`You are EVA. ALWAYS use tools. NEVER respond directly. Respond in PORTUGUESE when user asks in Portuguese.

For CURRENT TIME queries, use get_time tool:
{"type":"get_time","city":"Salvador, Brazil"}

For general searches: {"type":"websearch","query":"..."}
For commands: {"type":"bash","command":"ls"}`, pwd, usr.Username, shell)

	reqBody := map[string]any{
		"model": a.cfg.Model,
		"messages": []map[string]any{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": task},
		},
		"tools":      tools,
		"tool_choice": "auto",
	}

	maxIterations := 5
	for i := 0; i < maxIterations; i++ {
		resp, err := a.sendRequest(reqBody)
		if err != nil {
			return fmt.Errorf("gateway request failed: %w", err)
		}

		hasMore, toolResult, err := a.handleResponse(resp, true, a.cfg.Yes)
		if err != nil {
			return err
		}

		if !hasMore || toolResult == "" {
			break
		}

		if !a.evalLoop(reqBody, toolResult) {
			break
		}

		if len(a.messages) == 0 {
			break
		}
		lastMsg := a.messages[len(a.messages)-1]
		if len(lastMsg.ToolCalls) == 0 {
			break
		}
	}
	return nil
}

func (a *Agent) sendRequest(reqBody map[string]any) ([]byte, error) {
	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	gatewayURL := GatewayURL
	if a.cfg.Gateway != "" {
		gatewayURL = a.cfg.Gateway
	}

	httpReq, err := http.NewRequest("POST", gatewayURL, bytes.NewReader(jsonData))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("gateway returned %d: %s", resp.StatusCode, string(body))
	}

	return body, nil
}

func (a *Agent) handleResponse(data []byte, interactive, autoConfirm bool) (bool, string, error) {
	var resp map[string]any
	if err := json.Unmarshal(data, &resp); err != nil {
		return false, "", fmt.Errorf("parse error: %w", err)
	}

	choices, ok := resp["choices"].([]any)
	if !ok || len(choices) == 0 {
		return false, "", fmt.Errorf("no response")
	}

	choice := choices[0].(map[string]any)
	msg, ok := choice["message"].(map[string]any)
	if !ok {
		return false, "", fmt.Errorf("no message")
	}

	tc, hasTC := msg["tool_calls"]
	if hasTC {
		toolCalls := tc.([]any)
		if len(toolCalls) == 0 {
			return false, "", fmt.Errorf("no tool calls")
		}

		tc0 := toolCalls[0].(map[string]any)
		fn := tc0["function"].(map[string]any)
		fnName := fn["name"].(string)

		if fnName == "get_time" {
			city := ""
			switch arg := fn["arguments"].(type) {
			case string:
				var args map[string]any
				json.Unmarshal([]byte(arg), &args)
				if c, ok := args["city"].(string); ok {
					city = c
				}
			case map[string]any:
				if c, ok := fn["arguments"].(map[string]any)["city"].(string); ok {
					city = c
				}
			}

			timezoneMap := map[string]string{
				"salvador": "America/Bahia",
				"salvador, brazil": "America/Bahia",
				"salvador, bahia": "America/Bahia",
				"sao paulo": "America/Sao_Paulo",
				"sao paulo, brazil": "America/Sao_Paulo",
				"rio de janeiro": "America/Rio_Branco",
				"new york": "America/New_York",
				"london": "Europe/London",
				"tokyo": "Asia/Tokyo",
			}

			tz := timezoneMap[strings.ToLower(city)]
			if tz == "" {
				tz = "America/Bahia"
			}

			cmd := exec.Command("bash", "-c", `TZ="`+tz+`" date "+%H:%M:%S %A, %B %d, %Y"`)
			output, err := cmd.Output()
			toolResult := ""
			if err == nil {
				toolResult = "Current time: " + strings.TrimSpace(string(output))
			} else {
				toolResult = "Error getting time: " + err.Error()
			}

			a.writeOutput("\033[36m%s\033[0m\n", toolResult)

			toolCallID := tc0["id"].(string)
			argsStr, _ := fn["arguments"].(string)
			a.messages = append(a.messages, Message{
				Role:               "assistant",
				ToolCalls:          []ToolCall{{ID: toolCallID, Type: "function", Function: ToolFunction{Name: "get_time", Arguments: argsStr}}},
				ToolCallID:         toolCallID,
				ToolCallFunction:  "get_time",
				ToolCallResult:    toolResult,
			})
			return true, toolResult, nil
		}

		if fnName == "websearch" {
			if !a.cfg.PermInternet {
				toolResult := "Internet access not allowed"
				a.writeOutput("\033[31m%s\033[0m\n", toolResult)
				if interactive {
					toolCallID := tc0["id"].(string)
					argsStr, _ := fn["arguments"].(string)
					a.messages = append(a.messages, Message{
						Role:               "assistant",
						ToolCalls:          []ToolCall{{ID: toolCallID, Type: "function", Function: ToolFunction{Name: "websearch", Arguments: argsStr}}},
						ToolCallID:         toolCallID,
						ToolCallFunction:  "websearch",
						ToolCallResult:    toolResult,
					})
				}
				return true, toolResult, nil
			}
			query := ""
			switch a := fn["arguments"].(type) {
			case string:
				var args map[string]any
				json.Unmarshal([]byte(a), &args)
				if q, ok := args["query"].(string); ok {
					query = q
				}
			case map[string]any:
				if q, ok := fn["arguments"].(map[string]any)["query"].(string); ok {
					query = q
				}
			}
			results, err := a.doWebSearch(query)
			toolResult := ""
			if err != nil {
				if strings.Contains(err.Error(), "not set") || strings.Contains(err.Error(), "FIRECRAWL") {
					a.writeOutput("\033[33m⚠ Web search unavailable. Set FIRECRAWL_API_KEY for web search.\033[0m\n")
				}
				toolResult = fmt.Sprintf("Error: %v", err)
				a.writeOutput("\033[31mSearch error: %v\033[0m\n", err)
			} else {
				toolResult = results
				a.writeOutput("\033[36m%s\033[0m\n", results)
			}
			toolCallID := tc0["id"].(string)
			argsStr, _ := fn["arguments"].(string)
			a.messages = append(a.messages, Message{
				Role:               "assistant",
				ToolCalls:          []ToolCall{{ID: toolCallID, Type: "function", Function: ToolFunction{Name: "websearch", Arguments: argsStr}}},
				ToolCallID:         toolCallID,
				ToolCallFunction:  "websearch",
				ToolCallResult:    toolResult,
			})
			return true, toolResult, nil
		}
		if fnName == "webfetch" {
			url := ""
			switch arg := fn["arguments"].(type) {
			case string:
				var args map[string]any
				json.Unmarshal([]byte(arg), &args)
				if u, ok := args["url"].(string); ok {
					url = u
				}
			case map[string]any:
				if u, ok := fn["arguments"].(map[string]any)["url"].(string); ok {
					url = u
				}
			}
			content, err := a.doWebFetch(url)
			toolResult := ""
			if err != nil {
				toolResult = fmt.Sprintf("Error: %v", err)
				a.writeOutput("\033[31mFetch error: %v\033[0m\n", err)
			} else {
				toolResult = content
				a.writeOutput("\033[36m%s\033[0m\n", content)
			}
			toolCallID := tc0["id"].(string)
			argsStr, _ := fn["arguments"].(string)
			a.messages = append(a.messages, Message{
				Role:               "assistant",
				ToolCalls:          []ToolCall{{ID: toolCallID, Type: "function", Function: ToolFunction{Name: "webfetch", Arguments: argsStr}}},
				ToolCallID:         toolCallID,
				ToolCallFunction:  "webfetch",
				ToolCallResult:    toolResult,
			})
			return true, toolResult, nil
		}
		var commands []Command
		args := make(map[string]any)
		
		switch a := fn["arguments"].(type) {
		case string:
			json.Unmarshal([]byte(a), &args)
		case map[string]any:
			args = a
		}
		
		if cmds, ok := args["commands"]; ok {
			for _, c := range cmds.([]any) {
				m, ok := c.(map[string]any)
				if !ok {
					continue
				}
				cmd := Command{}
				if v, ok := m["type"].(string); ok {
					cmd.Type = v
				}
				if v, ok := m["command"].(string); ok {
					cmd.Command = v
				}
				if v, ok := m["path"].(string); ok {
					cmd.Path = v
				}
				if v, ok := m["content"].(string); ok {
					cmd.Content = v
				}
				if v, ok := m["old"].(string); ok {
					cmd.Old = v
				}
				if v, ok := m["new"].(string); ok {
					cmd.New = v
				}
				if v, ok := m["task"].(string); ok {
					cmd.Task = v
				}
				if v, ok := m["status"].(string); ok {
					cmd.Status = v
				}
				commands = append(commands, cmd)
			}
		}

		var toolResult string
		for _, cmd := range commands {
			result, err := a.executeCommand(cmd, autoConfirm)
			if err != nil {
				toolResult += fmt.Sprintf("%s error: %v\n", cmd.Type, err)
			} else {
				toolResult += result + "\n"
			}
		}

		if interactive {
			toolCall := ToolCall{ID: tc0["id"].(string), Type: "function", Function: ToolFunction{Name: fn["name"].(string)}}
			if argsStr, ok := fn["arguments"].(string); ok {
				toolCall.Function.Arguments = argsStr
			}
			a.messages = append(a.messages, Message{Role: "assistant", ToolCalls: []ToolCall{toolCall}})
		}
		return true, toolResult, nil
	}

	if content, ok := msg["content"].(string); ok && content != "" {
		a.writeOutput("\033[36m%s\033[0m\n", content)
		if interactive {
			a.messages = append(a.messages, Message{Role: "assistant", Content: content})
		}
	}

	return false, "", nil
}

type Command struct {
	Type    string `json:"type"`
	Command string `json:"command,omitempty"`
	Path    string `json:"path,omitempty"`
	Content string `json:"content,omitempty"`
	Old     string `json:"old,omitempty"`
	New     string `json:"new,omitempty"`
	Task    string `json:"task,omitempty"`
	Status string `json:"status,omitempty"`
	Result string `json:"-"`
}

func (a *Agent) executeCommand(cmd Command, autoConfirm bool) (string, error) {
	switch cmd.Type {
	case "bash":
		if !a.cfg.PermExec {
			return "", fmt.Errorf("shell execution not allowed")
		}
		if strings.Contains(cmd.Command, "sudo") || strings.HasPrefix(strings.TrimSpace(cmd.Command), "sudo ") {
			if !a.cfg.PermRoot {
				rootCmds := []string{"rm -rf", "mkfs", "dd if=", ":(){:|:&}:", "chmod -R 000", "chown -R", "shutdown", "reboot", "init 6", "telinit 6"}
				for _, dc := range rootCmds {
					if strings.Contains(cmd.Command, dc) {
						return "", fmt.Errorf("root/sudo execution not allowed")
					}
				}
			}
		}
		if !autoConfirm {
			if !a.cfg.Interactive {
				return "", fmt.Errorf("enable auto-confirm or run in terminal mode")
			}
			a.writeOutput("\033[33mExecute '%s'? [y/N]\033[0m ", cmd.Command)
			reader := bufio.NewReader(os.Stdin)
			resp, _ := reader.ReadString('\n')
			resp = strings.NewReplacer("\r\n", "", "\r", "", "\n", "").Replace(resp)
			resp = strings.TrimSpace(strings.ToLower(resp))
			if resp != "y" && resp != "yes" {
				a.writeOutput("\033[31mCancelled\033[0m\n")
				return "", nil
			}
		}
		err := a.execBash(cmd.Command)
		return "Command executed: " + cmd.Command, err
	case "read_file":
		if !a.cfg.PermRead {
			return "", fmt.Errorf("file read not allowed")
		}
		err := a.readFile(cmd.Path)
		return "File read: " + cmd.Path, err
	case "create_file":
		if !a.cfg.PermWrite {
			return "", fmt.Errorf("file write not allowed")
		}
		err := a.createFile(cmd.Path, cmd.Content)
		return "File created: " + cmd.Path, err
	case "edit_file":
		if !a.cfg.PermWrite {
			return "", fmt.Errorf("file write not allowed")
		}
		err := a.editFile(cmd.Path, cmd.Old, cmd.New)
		return "File edited: " + cmd.Path, err
	case "delete_file":
		if !a.cfg.PermDelete {
			return "", fmt.Errorf("file delete not allowed")
		}
		err := a.deleteFile(cmd.Path)
		return "File deleted: " + cmd.Path, err
	case "update_kanban":
		if !a.cfg.PermWrite {
			return "", fmt.Errorf("kanban write not allowed")
		}
		err := a.updateKanban(cmd.Task, cmd.Status)
		return "Kanban updated", err
	default:
		return "", fmt.Errorf("unknown command type: %s", cmd.Type)
	}
}

func (a *Agent) execBash(cmd string) error {
	a.writeOutput("\033[33m$ %s\033[0m\n", cmd)
	execCmd := exec.Command("bash", "-c", cmd)
	
	// Create pipes to capture output and still show it
	stdoutPipe, _ := execCmd.StdoutPipe()
	stderrPipe, _ := execCmd.StderrPipe()
	
	if err := execCmd.Start(); err != nil {
		return err
	}

	go func() {
		scanner := bufio.NewScanner(stdoutPipe)
		for scanner.Scan() {
			a.writeOutput("%s\n", scanner.Text())
		}
	}()

	go func() {
		scanner := bufio.NewScanner(stderrPipe)
		for scanner.Scan() {
			a.writeOutput("%s\n", scanner.Text())
		}
	}()

	return execCmd.Wait()
}

func (a *Agent) readFile(path string) error {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(absPath)
	if err != nil {
		return err
	}
	a.writeOutput("\033[36m--- %s ---\n%s\033[0m\n", absPath, string(data))
	return nil
}

func (a *Agent) createFile(path, content string) error {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	dir := filepath.Dir(absPath)
	os.MkdirAll(dir, 0755)
	if err := os.WriteFile(absPath, []byte(content), 0644); err != nil {
		return err
	}
	a.writeOutput("\033[32mCreated: %s\033[0m\n", absPath)
	return nil
}

func (a *Agent) editFile(path, oldStr, newStr string) error {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(absPath)
	if err != nil {
		return err
	}
	content := strings.Replace(string(data), oldStr, newStr, 1)
	if err := os.WriteFile(absPath, []byte(content), 0644); err != nil {
		return err
	}
	a.writeOutput("\033[32mEdited: %s\033[0m\n", absPath)
	return nil
}

func (a *Agent) deleteFile(path string) error {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	info, err := os.Stat(absPath)
	if err != nil {
		return err
	}
	if info.IsDir() {
		err = os.RemoveAll(absPath)
	} else {
		err = os.Remove(absPath)
	}
	if err != nil {
		return err
	}
	a.writeOutput("\033[32mDeleted: %s\033[0m\n", absPath)
	return nil
}

func (a *Agent) updateKanban(task, status string) error {
	kanbanPath := "kanban.md"

	data, err := os.ReadFile(kanbanPath)
	if err != nil {
		content := fmt.Sprintf("# Kanban\n\n## To Do\n- [ ] %s\n\n## In Progress\n\n## Done\n", task)
		err := os.WriteFile(kanbanPath, []byte(content), 0644)
		if err == nil {
			a.writeOutput("\033[32mKanban created and task added\033[0m\n")
		}
		return err
	}

	lines := strings.Split(string(data), "\n")
	var newLines []string
	taskFound := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.Contains(trimmed, task) {
			taskFound = true
			checked := " "
			if status == "done" {
				checked = "x"
			}
			newLines = append(newLines, fmt.Sprintf("- [%s] %s", checked, task))
			continue
		}
		newLines = append(newLines, line)
	}

	if !taskFound {
		section := "## To Do"
		if status == "in_progress" {
			section = "## In Progress"
		} else if status == "done" {
			section = "## Done"
		}

		for i, line := range newLines {
			if strings.HasPrefix(strings.TrimSpace(line), section) {
				checked := " "
				if status == "done" {
					checked = "x"
				}
				for j := i + 1; j < len(newLines); j++ {
					if strings.HasPrefix(strings.TrimSpace(newLines[j]), "## ") {
						newLines = append(newLines[:j], append([]string{fmt.Sprintf("- [%s] %s", checked, task)}, newLines[j:]...)...)
						break
					}
				}
				break
			}
		}
	}

	err = os.WriteFile(kanbanPath, []byte(strings.Join(newLines, "\n")), 0644)
	if err == nil {
		a.writeOutput("\033[32mKanban updated\033[0m\n")
	}
	return err
}

func (a *Agent) doWebSearch(query string) (string, error) {
	apiKey := a.cfg.FirecrawlKey
	if apiKey == "" {
		apiKey = os.Getenv("FIRECRAWL_API_KEY")
	}

	if apiKey != "" {
		searchURL := "https://api.firecrawl.dev/v2/search"
		jsonData, _ := json.Marshal(map[string]any{
			"query": query,
			"limit": 5,
		})

		httpReq, err := http.NewRequest("POST", searchURL, bytes.NewReader(jsonData))
		if err == nil {
			httpReq.Header.Set("Content-Type", "application/json")
			httpReq.Header.Set("Authorization", "Bearer "+apiKey)

			client := &http.Client{Timeout: 30 * time.Second}
			resp, err := client.Do(httpReq)
			if err == nil {
				defer resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					body, _ := io.ReadAll(resp.Body)
					var result map[string]any
					json.Unmarshal(body, &result)

					var output string
					if webData, ok := result["data"].(map[string]any); ok {
						if data, ok := webData["web"].([]any); ok && len(data) > 0 {
							for i, item := range data {
								if i > 4 {
									break
								}
								m := item.(map[string]any)
								title, _ := m["title"].(string)
								url, _ := m["url"].(string)
								desc, _ := m["description"].(string)
								output += fmt.Sprintf("%d. %s\n   %s\n   %s\n\n", i+1, title, url, desc)
							}
						}
					}
					if output != "" {
						return output, nil
					}
				}
			}
		}
	}

	searchURL := "https://www.google.com/search?q=" + strings.ReplaceAll(query, " ", "+")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	allocCtx, _ := chromedp.NewExecAllocator(ctx, chromedp.DefaultExecAllocatorOptions[:]...)
	taskCtx, _ := chromedp.NewContext(allocCtx)

	if err := chromedp.Navigate(searchURL).Do(taskCtx); err != nil {
		return "", fmt.Errorf("chromedp navigate failed: %w", err)
	}

	if err := chromedp.WaitVisible("#search").Do(taskCtx); err != nil {
		return "", fmt.Errorf("chromedp wait failed: %w", err)
	}

	var resultHTML string
	if err := chromedp.OuterHTML("html", &resultHTML).Do(taskCtx); err != nil {
		return "", fmt.Errorf("chromedp outerHTML failed: %w", err)
	}

	var results []string
	re := regexp.MustCompile(`<a href="(https?://[^\s"']+)"[^>]*>([^<]+)</a>`)
	matches := re.FindAllStringSubmatch(resultHTML, -1)
	seen := make(map[string]bool)
	for _, m := range matches {
		if len(m) == 3 {
			url := m[1]
			title := regexp.MustCompile(`<[^>]+>`).ReplaceAllString(m[2], "")
			if !strings.Contains(url, "google") && !strings.Contains(url, "youtube.com/redirect") &&
				!seen[url] && len(results) < 5 && len(url) > 20 {
				seen[url] = true
				results = append(results, fmt.Sprintf("%d. %s\n   %s\n\n", len(results)+1, title, url))
			}
		}
	}

	if len(results) == 0 {
		return "", fmt.Errorf("no results found")
	}

	return strings.Join(results, ""), nil
}

func (a *Agent) doWebFetch(url string) (string, error) {
	if !a.cfg.PermInternet {
		return "", fmt.Errorf("internet access not allowed")
	}

	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("fetch failed: %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 50000))
	content := string(body)

	content = regexp.MustCompile(`<[^>]+>`).ReplaceAllString(content, " ")
	content = regexp.MustCompile(`\s+`).ReplaceAllString(content, " ")
	content = strings.TrimSpace(content)

	if len(content) > 3000 {
		content = content[:3000] + "..."
	}

	return content, nil
}

func (a *Agent) fetchWithBrowser(url string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var browserOpts []chromedp.ExecAllocatorOption
	browserOpts = append(browserOpts,
		chromedp.Headless,
		chromedp.DisableGPU,
		chromedp.NoSandbox,
		chromedp.Flag("disable-web-security", true),
	)

	allocCtx, _ := chromedp.NewExecAllocator(ctx, browserOpts...)
	ctx, cancel = chromedp.NewContext(allocCtx)
	defer cancel()

	var htmlContent string
	var timeText string

	err := chromedp.Run(ctx,
		chromedp.Navigate(url),
		chromedp.Sleep(2*time.Second),
		chromedp.OuterHTML(`html`, &htmlContent, chromedp.ByQuery),
		chromedp.Text(`#ct`, &timeText, chromedp.ByID),
	)

	if err != nil {
		if len(htmlContent) > 0 {
			re := regexp.MustCompile(`(\d{1,2}:\d{2}:\d{2}\s*(?:AM|PM)?)`)
			matches := re.FindStringSubmatch(htmlContent)
			dateRe := regexp.MustCompile(`([A-Z][a-z]{2,}\s+[A-Z][a-z]{2,}\s+\d{1,2},?\s+\d{4})`)
			dateMatch := dateRe.FindString(htmlContent)
			if len(matches) > 1 {
				return fmt.Sprintf("Time: %s, Date: %s", matches[1], dateMatch)
			}
		}
		return fmt.Sprintf("Error: %v", err)
	}

	dateRe := regexp.MustCompile(`([A-Z][a-z]{2,}\s+[A-Z][a-z]{2,}\s+\d{1,2},?\s+\d{4})`)
	dateMatch := dateRe.FindString(htmlContent)

	if timeText != "" {
		return fmt.Sprintf("Time: %s, Date: %s", strings.TrimSpace(timeText), dateMatch)
	}

	return "Time not found"
}

func (a *Agent) evalLoop(reqBody map[string]any, toolResult string) bool {
	a.writeOutput("\033[33m→Evaluating result...\033[0m\n")

	evalPrompt := fmt.Sprintf(`O usuário perguntou a hora. Com base no resultado abaixo, responda na MESMA LINGUA do usuário:

%s

Responda de forma clara e concisa. Se satisfeito, responda "TASK_COMPLETE".`, toolResult)

	var messages []map[string]any
	if msgs, ok := reqBody["messages"].([]map[string]any); ok {
		messages = append(messages, msgs...)
	}
	messages = append(messages, map[string]any{
		"role":    "tool",
		"content": toolResult,
	})
	messages = append(messages, map[string]any{
		"role":    "user",
		"content": evalPrompt,
	})

	evalBody := map[string]any{
		"model":    a.cfg.Model,
		"messages": messages,
		"tools":    tools,
		"tool_choice": "auto",
	}

	resp, err := a.sendRequest(evalBody)
	if err != nil {
		a.writeOutput("\033[31mEval error: %v\033[0m\n", err)
		return false
	}

	var evalResp map[string]any
	if err := json.Unmarshal(resp, &evalResp); err != nil {
		a.writeOutput("\033[31mParse eval error: %v\033[0m\n", err)
		return false
	}

	choices := evalResp["choices"].([]any)
	choice := choices[0].(map[string]any)
	msg := choice["message"].(map[string]any)

	if content, ok := msg["content"].(string); ok {
		if strings.Contains(content, "TASK_COMPLETE") {
			a.writeOutput("\033[32m✓ Task complete\033[0m\n")
			return false
		}
		a.writeOutput("\033[36m%s\033[0m\n", content)
		if !strings.Contains(content, "search") && !strings.Contains(content, "result") {
			return false
		}

		tc, hasTC := msg["tool_calls"]
		if hasTC {
			toolCalls := tc.([]any)
			if len(toolCalls) > 0 {
				tc0 := toolCalls[0].(map[string]any)
				fn := tc0["function"].(map[string]any)
				fnName := fn["name"].(string)

				if fnName == "get_time" {
					city := ""
					switch arg := fn["arguments"].(type) {
					case string:
						var args map[string]any
						json.Unmarshal([]byte(arg), &args)
						if c, ok := args["city"].(string); ok {
							city = c
						}
					}
					cityMap := map[string]string{
						"salvador": "America/Bahia",
						"salvador, brazil": "America/Bahia",
						"salvador, brasil": "America/Bahia",
						"sao paulo": "America/Sao_Paulo",
						"sao paulo, brazil": "America/Sao_Paulo",
						"rio de janeiro": "America/Rio_Branco",
						"new york": "America/New_York",
						"london": "Europe/London",
						"tokyo": "Asia/Tokyo",
					}
					timezone := cityMap[strings.ToLower(city)]
					if timezone == "" {
						timezone = "America/Bahia"
					}
					content, err := a.doWebFetch("https://worldtimeapi.org/api/timezone/" + strings.ReplaceAll(timezone, " ", "_"))
					var newResult string
					if err != nil {
						newResult = fmt.Sprintf("Error: %v", err)
					} else {
						newResult = content
					}
					return a.evalLoop(reqBody, newResult)
				}

				if fnName == "websearch" {
					if !a.cfg.PermInternet {
						toolResult := "Internet access not allowed"
						a.writeOutput("\033[31m%s\033[0m\n", toolResult)
						return true
					}
					query := ""
					switch arg := fn["arguments"].(type) {
					case string:
						var args map[string]any
						json.Unmarshal([]byte(arg), &args)
						if q, ok := args["query"].(string); ok {
							query = q
						}
					}
					results, err := a.doWebSearch(query)
					var newResult string
					if err != nil {
						newResult = fmt.Sprintf("Error: %v", err)
					} else {
						newResult = results
					}
					return a.evalLoop(reqBody, newResult)
				}
				if fnName == "webfetch" {
					url := ""
					switch arg := fn["arguments"].(type) {
					case string:
						var args map[string]any
						json.Unmarshal([]byte(arg), &args)
						if u, ok := args["url"].(string); ok {
							url = u
						}
					}
					content, err := a.doWebFetch(url)
					var newResult string
					if err != nil {
						newResult = fmt.Sprintf("Error: %v", err)
					} else {
						newResult = content
					}
					return a.evalLoop(reqBody, newResult)
				}

				var commands []Command
				switch arg := fn["arguments"].(type) {
				case string:
					var args map[string]any
					json.Unmarshal([]byte(arg), &args)
					if cmds, ok := args["commands"].([]any); ok {
						for _, c := range cmds {
							m, _ := c.(map[string]any)
							cmd := Command{}
							if v, ok := m["type"].(string); ok {
								cmd.Type = v
							}
							if v, ok := m["command"].(string); ok {
								cmd.Command = v
							}
							if v, ok := m["path"].(string); ok {
								cmd.Path = v
							}
							if v, ok := m["content"].(string); ok {
								cmd.Content = v
							}
							if v, ok := m["old"].(string); ok {
								cmd.Old = v
							}
							if v, ok := m["new"].(string); ok {
								cmd.New = v
							}
							commands = append(commands, cmd)
						}
					}
				}

				for _, cmd := range commands {
					_, err := a.executeCommand(cmd, a.cfg.Yes)
					if err != nil {
						a.writeOutput("\033[31mError: %v\033[0m\n", err)
					} else {
						a.writeOutput("\033[32mDone: %s\033[0m\n", cmd.Type)
					}
				}
				return a.evalLoop(reqBody, "Commands executed")
			}
		}
	}

	return true
}