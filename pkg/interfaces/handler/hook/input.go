// Package hook は Claude Code の hook（SessionStart / UserPromptSubmit）として
// depgraph の解析結果をプロンプトの文脈へ注入する。解析は行わず、Neo4j に入って
// いる結果を読むだけ。どんな失敗でもプロンプトを止めないことを最優先にする。
package hook

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
)

// Event は depgraph hook が扱う hook イベント。
type Event string

const (
	// SessionStart はセッション開始時（startup / resume / clear / compact）。
	SessionStart Event = "SessionStart"
	// Prompt はプロンプト送信時（UserPromptSubmit）。
	Prompt Event = "UserPromptSubmit"
	// PostEdit runs after Claude edits a file (PostToolUse on Edit / Write /
	// MultiEdit / NotebookEdit): problems of that file are fed back at once.
	PostEdit Event = "PostToolUse"
)

// ParseEvent は CLI 引数（session-start / prompt）を Event に変換する。
func ParseEvent(arg string) (Event, error) {
	switch arg {
	case "session-start":
		return SessionStart, nil
	case "prompt":
		return Prompt, nil
	case "post-edit":
		return PostEdit, nil
	}
	return "", fmt.Errorf("unknown hook event %q (want session-start, prompt or post-edit)", arg)
}

// Input は Claude Code が stdin に渡す hook 入力のうち使うもの。
type Input struct {
	SessionID     string    `json:"session_id"`
	Cwd           string    `json:"cwd"`
	HookEventName string    `json:"hook_event_name"`
	Prompt        string    `json:"prompt"`
	Source        string    `json:"source"`     // SessionStart: startup / resume / clear / compact
	ToolName      string    `json:"tool_name"`  // PostToolUse
	ToolInput     ToolInput `json:"tool_input"` // PostToolUse
}

// ToolInput is the part of a file-editing tool's input that names the file.
type ToolInput struct {
	FilePath     string `json:"file_path"`     // Edit / Write / MultiEdit
	NotebookPath string `json:"notebook_path"` // NotebookEdit
}

// EditedPath is the file the tool edited ("" for other tools).
func (in Input) EditedPath() string {
	if in.ToolInput.FilePath != "" {
		return in.ToolInput.FilePath
	}
	return in.ToolInput.NotebookPath
}

// ParseInput は stdin の JSON を読む。空入力はゼロ値（手動実行時）。
func ParseInput(r io.Reader) (Input, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return Input{}, err
	}
	var in Input
	if strings.TrimSpace(string(b)) == "" {
		return in, nil
	}
	if err := json.Unmarshal(b, &in); err != nil {
		return Input{}, fmt.Errorf("hook input: %w", err)
	}
	return in, nil
}

// ResolveRoot は --root、$CLAUDE_PROJECT_DIR、stdin の cwd の順に最初の非空を
// 絶対パスにして返す。どれも無ければ ""。
func ResolveRoot(flagRoot, projectDir, cwd string) string {
	for _, p := range []string{flagRoot, projectDir, cwd} {
		if p == "" {
			continue
		}
		if abs, err := filepath.Abs(p); err == nil {
			return abs
		}
		return filepath.Clean(p)
	}
	return ""
}
