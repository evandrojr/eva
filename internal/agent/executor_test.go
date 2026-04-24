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
			err := execBash(tt.cmd)
			if (err != nil) != tt.wantErr {
				t.Errorf("execBash() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestCreateFile(t *testing.T) {
	tmp := t.TempDir()
	path := tmp + "/test.txt"
	content := "hello world"

	err := createFile(path, content)
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
	tmp := t.TempDir()
	path := tmp + "/test.txt"
	oldContent := "hello world"
	newContent := "hello go"

	os.WriteFile(path, []byte(oldContent), 0644)

	err := editFile(path, oldContent, newContent)
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

func BenchmarkExecBash(b *testing.B) {
	for i := 0; i < b.N; i++ {
		execBash("echo test")
	}
}