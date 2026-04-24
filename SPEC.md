# SPEC - EVA Agent

## 1. Project Overview

**Project Name:** EVA (Execution & Voice Assistant)

**Type:** CLI Agent Tool written in Go

**Core Functionality:** A terminal-based AI agent that reads task descriptions from files or command-line arguments, uses the AI-gatiator gateway to process tasks, generates sub-agents to solve problems, creates requirements documents and kanban boards to track project evolution.

**Target Users:** Developers who want to delegate complex tasks to an AI agent that can execute shell commands and track progress.

---

## 2. Technical Architecture

### 2.1 Gateway Integration
- Uses `/home/j/AI-gatiator` as LLM gateway (HTTP API on port 1313)
- Provider priority: opencode → openrouter → groq → google → ollama (fallback)
- API endpoint: `http://localhost:1313/v1/chat/completions`

### 2.2 System Flow
```
User Input (CLI)
    ↓
Task Parser
    ↓
AI-gatiator Gateway (LLM)
    ↓
Response Parser (JSON + Commands)
    ↓
Execute Commands (Shell)
    ↓
Generate Artifacts (requirements.md, kanban.md)
    ↓
Output Result
```

### 2.3 JSON Response Format
```json
{
  "response": "Human-readable response",
  "commands": [
    {"type": "bash", "command": "ls -la"},
    {"type": "create_file", "path": "requirements.md", "content": "..."},
    {"type": "update_kanban", "task": "Implement X", "status": "in_progress"}
  ],
  "artifacts": {
    "requirements": "path/to/requirements.md",
    "kanban": "path/to/kanban.md"
  }
}
```

---

## 3. UI/UX Specification

### 3.1 CLI Interface

**Command-line Options:**
| Flag | Description | Required |
|------|-------------|----------|
| `-e` | Execute command/task as string | No (mutual exclusive with `-ef`) |
| `-ef` | Execute command from file path | No (mutual exclusive with `-e`) |
| `-m` | Model to use (default: from gateway) | No |
| `-v` | Verbose mode | No |
| `-h` | Show help | No |

**Usage Examples:**
```bash
eva -e "Create a web API in Go that reads from PostgreSQL"
eva -ef task.txt
eva -e "Add authentication to the API" -v
```

### 3.2 Terminal Output
- Color-coded output using ANSI codes
- Success: Green (`\033[32m`)
- Error: Red (`\033[31m`)
- Info: Cyan (`\033[36m`)
- Warning: Yellow (`\033[33m`)

### 3.3 Interactive Mode (future)
- REPL-style input for continuous commands

---

## 4. Functional Specification

### 4.1 Core Features

#### F1: CLI Parser
- Parse `-e` flag as task string
- Parse `-ef` flag as file path, read content
- Validate mutual exclusivity
- Validate required arguments

#### F2: Task Processor
- Send task to LLM via AI-gatiator
- Build system prompt with:
  - Agent capabilities (shell access, file creation, kanban management)
  - Output format requirements (JSON)
  - Context (current directory, environment)

#### F3: Command Executor
- Execute `bash` commands from response
- Capture stdout/stderr
- Report results back to LLM for context

#### F4: Artifact Generator

**Requirements File** (`requirements.md`):
```markdown
# Project: [Name]

## Overview
[Description]

## Functional Requirements
- [ ] FR1: [Requirement]

## Non-Functional Requirements
- Performance: [Metrics]
- Security: [Requirements]

## Technical Stack
- Language: [Go]
- Database: [PostgreSQL]
- etc.
```

**Kanban File** (`kanban.md`):
```markdown
# Kanban - [Project]

## To Do
- [ ] Task 1

## In Progress
- [ ] Task 2

## Done
- [x] Task 3
```

#### F5: Kanban Tracker
- Read/update kanban.md file
- Support status transitions: todo → in_progress → done
- Track task evolution over time

### 4.2 System Prompt Template

```
You are EVA, an AI agent that can execute commands in a terminal and create files.

## Capabilities
1. Execute bash/zsh commands
2. Create and update files
3. Generate requirements documents
4. Manage kanban boards

## Output Format (STRICT JSON)
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
- Current directory: {pwd}
- User: {user}
- Shell: {shell}

## Task
{task}
```

---

## 5. File Structure

```
eva/
├── cmd/eva/
│   └── main.go        # Entry point
├── internal/
│   ├── cli/
│   │   └── parser.go # CLI argument parsing
│   ├── agent/
│   │   ├── task.go    # Task processing
│   │   └── executor.go # Command execution
│   ├── gateway/
│   │   └── client.go # AI-gatiator integration
│   └── artifacts/
│       ├── requirements.go # Requirements generator
│       └── kanban.go        # Kanban manager
├── go.mod
└── main.go (wrapper)
```

---

## 6. Acceptance Criteria

### AC1: CLI Parsing
- [ ] `eva -e "test"` executes task from string
- [ ] `eva -ef file.txt` executes task from file
- [ ] `eva` without arguments shows help
- [ ] `eva -e "x" -ef "y"` shows error (mutual exclusion)

### AC2: Gateway Communication
- [ ] Request sent to AI-gatiator on port 1313
- [ ] JSON response parsed correctly
- [ ] Fallback to secondary provider on failure

### AC3: Command Execution
- [ ] Bash commands executed successfully
- [ ] Output captured and displayed
- [ ] Errors reported properly

### AC4: Artifact Generation
- [ ] requirements.md created with correct format
- [ ] kanban.md created with correct format
- [ ] Both files updated on subsequent runs

### AC5: Kanban Tracking
- [ ] Tasks move between todo/in_progress/done
- [ ] Status persisted to file

### AC6: Edge Cases
- [ ] Empty task shows helpful message
- [ ] Gateway unreachable shows error
- [ ] Invalid JSON from LLM handled gracefully
- [ ] File permission errors handled

---

## 7. Implementation Priority

1. **Phase 1:** CLI parser + basic gateway call
2. **Phase 2:** Command execution
3. **Phase 3:** Artifact generation (requirements.md, kanban.md)
4. **Phase 4:** Kanban tracking
5. **Phase 5:** Interactive mode (future)