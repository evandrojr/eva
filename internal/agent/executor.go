package agent

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"time"

	"github.com/peterh/liner"
)

var stdinReader *bufio.Reader

var globalReader *bufio.Reader

func init() {
	globalReader = bufio.NewReader(os.Stdin)
}

const GatewayURL = "http://localhost:1313/v1/chat/completions"

type Config struct {
	Model       string
	Session     bool
	SessionPath string
	Yes        bool
}

type Message struct {
	Role      string      `json:"role"`
	Content   string      `json:"content,omitempty"`
	ToolCalls []ToolCall  `json:"tool_calls,omitempty"`
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
			"name":        "websearch",
			"description": "Search the web for information, locations, how to get there, travel tips, facts, or any question",
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

	systemPrompt := fmt.Sprintf(`You are EVA, an AI agent that executes commands in a terminal, manages files, and searches the web.

## Tool Usage - REQUIRED
When user asks to RUN a command or do a task:
- Call the "execute" tool with commands array
- Example: {"type": "bash", "command": "ls -la"}
- Example: {"type": "read_file", "path": "file.go"}
- Example: {"type": "create_file", "path": "file.go", "content": "..."}
- Example: {"type": "edit_file", "path": "file.go", "old": "old", "new": "new"}

When user asks for INFORMATION (locations, how to get there, travel tips, facts, etc):
- Use web search to find the information first
- Then provide a clear answer with the results
- DO NOT try to run the question as a bash command

## Context
- Current directory: %s
- User: %s
- Shell: %s`, pwd, usr.Username, shell)

	fmt.Println("\033[36mEVA Terminal Mode\033[0m")
	fmt.Println("Type \033[33m/exit\033[0m or \033[33mCtrl+D\033[0m to quit")
	fmt.Println()

	stdinReader := bufio.NewReader(os.Stdin)

	for {
		fmt.Print("\033[32meva>\033[0m ")
		input, _ := stdinReader.ReadString('\n')
		input = strings.NewReplacer("\r\n", "", "\r", "").Replace(input)
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
		}

		resp, err := a.sendRequest(reqBody)
		if err != nil {
			fmt.Printf("\033[31mError: %v\033[0m\n", err)
			continue
		}

		a.handleResponse(resp, false, true)
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

	systemPrompt := fmt.Sprintf(`You are EVA, an AI agent that executes commands in a terminal and manages files.

## STRICT REQUIREMENT
You MUST use the "execute" tool for EVERY action. Never just describe what you would do.
Always call the execute tool with the commands array.

## Tool Usage - REQUIRED
When user asks to run a command, read a file, create a file, edit a file, or any task:
- Call the "execute" tool with the commands array
- Example: {"type": "bash", "command": "ls -la"}
- Example: {"type": "read_file", "path": "file.go"}
- Example: {"type": "create_file", "path": "file.go", "content": "..."}
- Example: {"type": "edit_file", "path": "file.go", "old": "old", "new": "new"}
- Example: {"type": "update_kanban", "task": "task", "status": "todo"}

## Context
- Current directory: %s
- User: %s
- Shell: %s`, pwd, usr.Username, shell)

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
		input = strings.NewReplacer("\r\n", "", "\r", "").Replace(input)
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
		}

		resp, err := a.sendRequest(reqBody)
		if err != nil {
			fmt.Printf("\033[31mError: %v\033[0m\n", err)
			continue
		}

		a.messages = append(a.messages, Message{Role: "user", Content: input})

		if err := a.handleResponse(resp, true, a.cfg.Yes); err != nil {
			fmt.Printf("\033[31mError: %v\033[0m\n", err)
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

	systemPrompt := fmt.Sprintf(`You are EVA, an AI agent that executes commands in a terminal, manages files, and searches the web.

## Tool Usage - REQUIRED
When user asks to RUN a command or do a task:
- Call the "execute" tool with commands array
- Example: {"type": "bash", "command": "ls -la"}
- Example: {"type": "read_file", "path": "file.go"}
- Example: {"type": "create_file", "path": "file.go", "content": "..."}
- Example: {"type": "edit_file", "path": "file.go", "old": "old", "new": "new"}

When user asks for INFORMATION (locations, how to get there, travel tips, facts, etc):
- Use web search to find the information first
- Then provide a clear answer with the results
- DO NOT try to run the question as a bash command

## Context
- Current directory: %s
- User: %s
- Shell: %s

## Task
%s`, pwd, usr.Username, shell, task)

	reqBody := map[string]any{
		"model": a.cfg.Model,
		"messages": []map[string]any{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": task},
		},
		"tools":      tools,
	}

	resp, err := a.sendRequest(reqBody)
	if err != nil {
		return fmt.Errorf("gateway request failed: %w", err)
	}

	return a.handleResponse(resp, true, a.cfg.Yes)
}

func (a *Agent) sendRequest(reqBody map[string]any) ([]byte, error) {
	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequest("POST", GatewayURL, bytes.NewReader(jsonData))
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

func (a *Agent) handleResponse(data []byte, interactive, autoConfirm bool) error {
	var resp map[string]any
	if err := json.Unmarshal(data, &resp); err != nil {
		return fmt.Errorf("parse error: %w", err)
	}

	choices, ok := resp["choices"].([]any)
	if !ok || len(choices) == 0 {
		return fmt.Errorf("no response")
	}

	choice := choices[0].(map[string]any)
	msg, ok := choice["message"].(map[string]any)
	if !ok {
		return fmt.Errorf("no message")
	}

	tc, hasTC := msg["tool_calls"]
	if hasTC {
		toolCalls := tc.([]any)
		if len(toolCalls) == 0 {
			return fmt.Errorf("no tool calls")
		}

		tc0 := toolCalls[0].(map[string]any)
		fn := tc0["function"].(map[string]any)
		
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

		for _, cmd := range commands {
			if err := a.executeCommand(cmd, a.cfg.Yes); err != nil {
				a.writeOutput("\033[31mError: %v\033[0m\n", err)
			}
		}

		if interactive {
			toolCall := ToolCall{ID: tc0["id"].(string), Type: "function", Function: ToolFunction{Name: fn["name"].(string)}}
			if argsStr, ok := fn["arguments"].(string); ok {
				toolCall.Function.Arguments = argsStr
			}
			a.messages = append(a.messages, Message{Role: "assistant", ToolCalls: []ToolCall{toolCall}})
		}
		return nil
	}

	if content, ok := msg["content"].(string); ok && content != "" {
		a.writeOutput("\033[36m%s\033[0m\n", content)
		if interactive {
			a.messages = append(a.messages, Message{Role: "assistant", Content: content})
		}
	}

	return nil
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
}

func (a *Agent) executeCommand(cmd Command, autoConfirm bool) error {
	switch cmd.Type {
	case "bash":
		if !autoConfirm {
			a.writeOutput("\033[33mExecute '%s'? [y/N]\033[0m ", cmd.Command)
			reader := bufio.NewReader(os.Stdin)
			resp, _ := reader.ReadString('\n')
			resp = strings.NewReplacer("\r\n", "", "\r", "", "\n", "").Replace(resp)
			resp = strings.TrimSpace(strings.ToLower(resp))
			if resp != "y" && resp != "yes" {
				a.writeOutput("\033[31mCancelled\033[0m\n")
				return nil
			}
		}
		return a.execBash(cmd.Command)
	case "read_file":
		return a.readFile(cmd.Path)
	case "create_file":
		return a.createFile(cmd.Path, cmd.Content)
	case "edit_file":
		return a.editFile(cmd.Path, cmd.Old, cmd.New)
	case "update_kanban":
		return a.updateKanban(cmd.Task, cmd.Status)
	default:
		return fmt.Errorf("unknown command type: %s", cmd.Type)
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