package agent

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"time"
)

const GatewayURL = "http://localhost:1313/v1/chat/completions"

type Config struct {
	Verbose    bool
	Model      string
	Session    bool
	SessionPath string
}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type Agent struct {
	cfg      Config
	messages []Message
}

func New(cfg Config) *Agent {
	a := &Agent{cfg: cfg, messages: []Message{}}
	if cfg.Session {
		a.loadSession()
	}
	return a
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

func (a *Agent) messagesAsMap() []map[string]string {
	var result []map[string]string
	for _, m := range a.messages {
		result = append(result, map[string]string{"role": m.Role, "content": m.Content})
	}
	return result
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

func (a *Agent) Interactive() error {
	if a.cfg.Session {
		defer a.saveSession()
	}

	reader := bufio.NewReader(os.Stdin)
	fmt.Println("\033[36mEVA Interactive Mode\033[0m")
	fmt.Println("Type \033[33m/exit\033[0m or \033[33mCtrl+D\033[0m to quit")
	fmt.Println()

	pwd, _ := os.Getwd()
	usr, _ := user.Current()
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/bash"
	}

	systemPrompt := fmt.Sprintf(`You are EVA, an AI agent that can execute commands in a terminal and create files.

## Capabilities
1. Execute bash/zsh commands
2. Create and update files
3. Generate requirements documents
4. Manage kanban boards

## Output Format (STRICT JSON - no extra text)
{
  "response": "Explanation of actions taken",
  "commands": [
    {"type": "bash", "command": "command to execute"},
    {"type": "create_file", "path": "file.md", "content": "..."},
    {"type": "update_kanban", "task": "task name", "status": "todo|in_progress|done"}
  ],
  "artifacts": {
    "requirements": "requirements.md",
    "kanban": "kanban.md"
  }
}

## Context
- Current directory: %s
- User: %s
- Shell: %s`, pwd, usr.Username, shell)

	for {
		fmt.Print("\033[32meva>\033[0m ")
		line, err := reader.ReadString('\n')
		if err != nil {
			break
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if line == "/exit" || line == "/quit" {
			fmt.Println("\033[33mGoodbye!\033[0m")
			break
		}

		a.messages = append(a.messages, Message{Role: "system", Content: systemPrompt})
		a.messages = append(a.messages, Message{Role: "user", Content: line})

		reqBody := map[string]interface{}{
			"model": a.cfg.Model,
			"messages": a.messages,
			"stream": false,
		}

		if a.cfg.Verbose {
			log.Printf("Sending request to gateway...")
		}

		resp, err := a.sendRequest(reqBody)
		if err != nil {
			fmt.Printf("\033[31mError: %v\033[0m\n", err)
			continue
		}

		var result LLMResponse
		if err := json.Unmarshal(resp, &result); err != nil {
			fmt.Printf("\033[31mParse error: %v\033[0m\n", err)
			continue
		}

		if len(result.Choices) == 0 {
			fmt.Println("\033[31mNo response from LLM\033[0m")
			continue
		}

		content := result.Choices[0].Message.Content
		content = strings.TrimPrefix(content, "```json")
		content = strings.TrimPrefix(content, "```")
		content = strings.TrimSuffix(content, "```")
		content = strings.TrimSpace(content)

		jsonStart := strings.Index(content, "{")
		jsonEnd := strings.LastIndex(content, "}")
		if jsonStart >= 0 && jsonEnd > jsonStart {
			content = content[jsonStart : jsonEnd+1]
		}

		var parsed AgentResponse
		if err := json.Unmarshal([]byte(content), &parsed); err != nil {
			fmt.Printf("\033[31mFailed to parse: %v\033[0m\n%s\n", err, content)
			continue
		}

		fmt.Printf("\033[36m%s\033[0m\n\n", parsed.Response)

		executor := NewExecutor(a.cfg.Verbose)
		for _, cmd := range parsed.Commands {
			if err := executor.Execute(cmd); err != nil {
				fmt.Printf("\033[31mError: %v\033[0m\n", err)
			}
		}

		if parsed.Artifacts.Kanban != "" {
			a.ensureKanban(parsed.Artifacts.Kanban)
		}

		a.messages = append(a.messages, Message{Role: "assistant", Content: content})
	}

	return nil
}

func (a *Agent) Execute(task string) error {
	if a.cfg.Verbose {
		log.Printf("Executing task: %s", task)
	}

	pwd, _ := os.Getwd()
	usr, _ := user.Current()
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/bash"
	}

	systemPrompt := fmt.Sprintf(`You are EVA, an AI agent that can execute commands in a terminal and create files.

## Capabilities
1. Execute bash/zsh commands
2. Create and update files
3. Generate requirements documents
4. Manage kanban boards

## Output Format (STRICT JSON - no extra text)
{
  "response": "Explanation of actions taken",
  "commands": [
    {"type": "bash", "command": "command to execute"},
    {"type": "create_file", "path": "file.md", "content": "..."},
    {"type": "update_kanban", "task": "task name", "status": "todo|in_progress|done"}
  ],
  "artifacts": {
    "requirements": "requirements.md",
    "kanban": "kanban.md"
  }
}

## Context
- Current directory: %s
- User: %s
- Shell: %s

## Task
%s`, pwd, usr.Username, shell, task)

	reqBody := map[string]interface{}{
		"model": a.cfg.Model,
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": task},
		},
		"stream": false,
	}

	if a.cfg.Verbose {
		log.Printf("Sending request to gateway...")
	}

	resp, err := a.sendRequest(reqBody)
	if err != nil {
		return fmt.Errorf("gateway request failed: %w", err)
	}

	var result LLMResponse
	if err := json.Unmarshal(resp, &result); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}

	if len(result.Choices) == 0 {
		return fmt.Errorf("no response from LLM")
	}

	content := result.Choices[0].Message.Content

	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)

	jsonStart := strings.Index(content, "{")
	jsonEnd := strings.LastIndex(content, "}")
	if jsonStart >= 0 && jsonEnd > jsonStart {
		content = content[jsonStart : jsonEnd+1]
	}

	if a.cfg.Verbose {
		log.Printf("Raw response: %s", content)
	}

	var parsed AgentResponse
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		fmt.Printf("Response: %s\n", content)
		return fmt.Errorf("failed to parse agent response: %w", err)
	}

	fmt.Printf("\033[36m%s\033[0m\n\n", parsed.Response)

	executor := NewExecutor(a.cfg.Verbose)
	for _, cmd := range parsed.Commands {
		if err := executor.Execute(cmd); err != nil {
			fmt.Printf("\033[31mError: %v\033[0m\n", err)
		}
	}

	if parsed.Artifacts.Kanban != "" {
		a.ensureKanban(parsed.Artifacts.Kanban)
	}

	return nil
}

func (a *Agent) Run(task string) (*AgentResponse, error) {
	pwd, _ := os.Getwd()
	usr, _ := user.Current()
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/bash"
	}

	systemPrompt := fmt.Sprintf(`You are EVA, an AI agent that can execute commands in a terminal and create files.

## Capabilities
1. Execute bash/zsh commands
2. Create and update files
3. Generate requirements documents
4. Manage kanban boards

## Output Format (STRICT JSON - no extra text)
{
  "response": "Explanation of actions taken",
  "commands": [
    {"type": "bash", "command": "command to execute"},
    {"type": "create_file", "path": "file.md", "content": "..."},
    {"type": "update_kanban", "task": "task name", "status": "todo|in_progress|done"}
  ],
  "artifacts": {
    "requirements": "requirements.md",
    "kanban": "kanban.md"
  }
}

## Context
- Current directory: %s
- User: %s
- Shell: %s`, pwd, usr.Username, shell)

	reqBody := map[string]interface{}{
		"model": a.cfg.Model,
		"messages": append(a.messagesAsMap(), 
			map[string]string{"role": "system", "content": systemPrompt},
			map[string]string{"role": "user", "content": task},
		),
		"stream": false,
	}

	resp, err := a.sendRequest(reqBody)
	if err != nil {
		return nil, fmt.Errorf("gateway request failed: %w", err)
	}

	var result LLMResponse
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if len(result.Choices) == 0 {
		return nil, fmt.Errorf("no response from LLM")
	}

	content := result.Choices[0].Message.Content

	parsed, err := a.parseResponse(content)
	if err != nil {
		return nil, err
	}

	fmt.Printf("\033[36m%s\033[0m\n\n", parsed.Response)

	executor := NewExecutor(a.cfg.Verbose)
	for _, cmd := range parsed.Commands {
		if err := executor.Execute(cmd); err != nil {
			fmt.Printf("\033[31mError: %v\033[0m\n", err)
		}
	}

	if parsed.Artifacts.Kanban != "" {
		a.ensureKanban(parsed.Artifacts.Kanban)
	}

	a.messages = append(a.messages, Message{Role: "user", Content: task})
	a.messages = append(a.messages, Message{Role: "assistant", Content: content})

	return parsed, nil
}
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)

	jsonStart := strings.Index(content, "{")
	jsonEnd := strings.LastIndex(content, "}")
	if jsonStart >= 0 && jsonEnd > jsonStart {
		content = content[jsonStart : jsonEnd+1]
	}

	var parsed AgentResponse
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		fmt.Printf("Response: %s\n", content)
		return nil, fmt.Errorf("failed to parse agent response: %w", err)
	}

	return &parsed, nil
}

	executor := NewExecutor(a.cfg.Verbose)
	for _, cmd := range parsed.Commands {
		if err := executor.Execute(cmd); err != nil {
			fmt.Printf("\033[31mError: %v\033[0m\n", err)
		}
	}

	if parsed.Artifacts.Kanban != "" {
		a.ensureKanban(parsed.Artifacts.Kanban)
	}

	a.messages = append(a.messages, Message{Role: "user", Content: task})
	a.messages = append(a.messages, Message{Role: "assistant", Content: content})

	return parsed, nil
}

func (a *Agent) sendRequest(reqBody map[string]interface{}) ([]byte, error) {
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

func (a *Agent) ensureKanban(path string) error {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return err
	}

	dir := filepath.Dir(absPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	if _, err := os.Stat(absPath); os.IsNotExist(err) {
		content := `# Kanban

## To Do

## In Progress

## Done
`
		if err := os.WriteFile(absPath, []byte(content), 0644); err != nil {
			return err
		}
		fmt.Printf("\033[32mCreated: %s\033[0m\n", absPath)
	}

	return nil
}

type LLMResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

type AgentResponse struct {
	Response  string                     `json:"response"`
	Commands  []AgentCommand             `json:"commands"`
	Artifacts ArtifactFiles             `json:"artifacts"`
}

type AgentCommand struct {
	Type    string `json:"type"`
	Command string `json:"command"`
	Path    string `json:"path"`
	Content string `json:"content"`
	Task    string `json:"task"`
	Status  string `json:"status"`
}

type ArtifactFiles struct {
	Requirements string `json:"requirements"`
	Kanban        string `json:"kanban"`
}

type Executor struct {
	verbose bool
}

func NewExecutor(verbose bool) *Executor {
	return &Executor{verbose: verbose}
}

func (e *Executor) Execute(cmd AgentCommand) error {
	switch cmd.Type {
	case "bash":
		return e.execBash(cmd.Command)
	case "create_file":
		return e.createFile(cmd.Path, cmd.Content)
	case "update_kanban":
		return e.updateKanban(cmd.Task, cmd.Status)
	default:
		return fmt.Errorf("unknown command type: %s", cmd.Type)
	}
}

func (e *Executor) execBash(cmd string) error {
	if e.verbose {
		fmt.Printf("\033[33m$ %s\033[0m\n", cmd)
	}

	parts := strings.Fields(cmd)
	if len(parts) == 0 {
		return nil
	}

	execCmd := exec.Command("bash", "-c", cmd)
	execCmd.Stdout = os.Stdout
	execCmd.Stderr = os.Stderr
	execCmd.Dir, _ = os.Getwd()

	if err := execCmd.Run(); err != nil {
		return err
	}

	return nil
}

func (e *Executor) createFile(path, content string) error {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return err
	}

	dir := filepath.Dir(absPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	if err := os.WriteFile(absPath, []byte(content), 0644); err != nil {
		return err
	}

	fmt.Printf("\033[32mCreated: %s\033[0m\n", absPath)
	return nil
}

func (e *Executor) updateKanban(task, status string) error {
	kanbanPath := "kanban.md"

	data, err := os.ReadFile(kanbanPath)
	if err != nil {
		content := fmt.Sprintf(`# Kanban

## To Do
- [ ] %s

## In Progress

## Done
`, task)
		return os.WriteFile(kanbanPath, []byte(content), 0644)
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

		inserted := false
		for i, line := range newLines {
			if strings.HasPrefix(strings.TrimSpace(line), section) {
				checked := " "
				if status == "done" {
					checked = "x"
				}
				for j := i + 1; j < len(newLines); j++ {
					if strings.HasPrefix(strings.TrimSpace(newLines[j]), "## ") {
						newLines = append(newLines[:j], append([]string{fmt.Sprintf("- [%s] %s", checked, task)}, newLines[j:]...)...)
						inserted = true
						break
					}
				}
				if !inserted {
					newLines = append(newLines, fmt.Sprintf("- [%s] %s", checked, task))
				}
				break
			}
		}
	}

	return os.WriteFile(kanbanPath, []byte(strings.Join(newLines, "\n")), 0644)
}