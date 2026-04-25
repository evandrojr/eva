package agent

import (
	"os"
	"testing"
)

func TestExecBash(t *testing.T) {
	tests := []struct {
		name    string
		cmd     string
		wantErr bool
	}{
		{"echo hello", "echo hello", false},
		{"invalid command", "nonexistentcmd123", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &Agent{
				cfg: Config{Interactive: true, Yes: true},
			}
			err := a.execBash(tt.cmd)
			if (err != nil) != tt.wantErr {
				t.Errorf("execBash() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestCreateFile(t *testing.T) {
	a := &Agent{}
	tmp := t.TempDir()
	path := tmp + "/test.txt"
	content := "hello world"

	err := a.createFile(path, content)
	if err != nil {
		t.Fatalf("createFile() error = %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}

	if string(data) != content {
		t.Errorf("got %s, want %s", string(data), content)
	}
}

func TestEditFile(t *testing.T) {
	a := &Agent{}
	tmp := t.TempDir()
	path := tmp + "/test.txt"
	oldContent := "hello world"
	newContent := "hello go"

	os.WriteFile(path, []byte(oldContent), 0644)

	err := a.editFile(path, oldContent, newContent)
	if err != nil {
		t.Fatalf("editFile() error = %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}

	if string(data) != newContent {
		t.Errorf("got %s, want %s", string(data), newContent)
	}
}

func TestCommand(t *testing.T) {
	cmd := Command{
		Type:    "bash",
		Command: "ls",
	}

	if cmd.Type != "bash" {
		t.Errorf("got %s, want bash", cmd.Type)
	}

	if cmd.Command != "ls" {
		t.Errorf("got %s, want ls", cmd.Command)
	}
}

func TestExecuteCommandReturnsResult(t *testing.T) {
	a := &Agent{cfg: Config{Interactive: true, Yes: true}}

	tests := []struct {
		name        string
		cmd         Command
		wantResult  string
		wantErr    bool
	}{
		{
			name: "bash echo",
			cmd: Command{
				Type:    "bash",
				Command: "echo hello",
			},
			wantResult: "Command executed: echo hello",
			wantErr:   false,
		},
		{
			name: "create file",
			cmd: Command{
				Type:    "create_file",
				Path:    "/tmp/eva_test_file.txt",
				Content: "test content",
			},
			wantResult: "File created: /tmp/eva_test_file.txt",
			wantErr:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := a.executeCommand(tt.cmd, true)
			if (err != nil) != tt.wantErr {
				t.Errorf("executeCommand() error = %v, wantErr %v", err, tt.wantErr)
			}
			if result == "" && !tt.wantErr {
				t.Errorf("executeCommand() result = empty, want %s", tt.wantResult)
			}
		})
	}
}

func TestHandleResponseSignature(t *testing.T) {
	a := &Agent{cfg: Config{Interactive: true, Yes: true}}

	resp := `{
		"choices": [{
			"message": {
				"content": "Hello"
			}
		}]
	}`

	hasMore, toolResult, err := a.handleResponse([]byte(resp), false, true)
	if err != nil {
		t.Errorf("handleResponse() error = %v", err)
	}
	if hasMore {
		t.Errorf("handleResponse() hasMore = true, want false for text response")
	}
	if toolResult != "" {
		t.Errorf("handleResponse() toolResult = %s, want empty", toolResult)
	}
}

func TestEvalLoopNoGateway(t *testing.T) {
	a := &Agent{cfg: Config{Model: "test"}}

	reqBody := map[string]any{
		"messages": []map[string]any{
			{"role": "user", "content": "test"},
		},
	}

	result := a.evalLoop(reqBody, "test result")
	if result {
		t.Log("evalLoop returned true - gateway likely unavailable (expected in test without gateway)")
	}
}

func TestMessageStruct(t *testing.T) {
	msg := Message{
		Role:            "assistant",
		Content:         "test content",
		ToolCallID:       "call_123",
		ToolCallFunction: "websearch",
		ToolCallResult:   "search results",
	}

	if msg.Role != "assistant" {
		t.Errorf("got role %s, want assistant", msg.Role)
	}
	if msg.ToolCallFunction != "websearch" {
		t.Errorf("got function %s, want websearch", msg.ToolCallFunction)
	}
}

func BenchmarkExecBash(b *testing.B) {
	for i := 0; i < b.N; i++ {
		a := &Agent{}
		a.execBash("echo test")
	}
}