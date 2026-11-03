package hook

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/nonchan7720/depgraph/pkg/domain/rules"
)

// StateFile はプロジェクト内の状態ファイル（.gitignore 対象）。
const StateFile = ".claude/.depgraph-hook-state.json"

// maxSessions を超えたら古いセッションから捨てる（ファイルの肥大化防止）。
const maxSessions = 20

// State は session_id ごとに「最後に注入した時点の Findings」を持つ。
type State struct {
	Sessions map[string]SessionState `json:"sessions"`
}

// SessionState は 1 セッション分の前回状態。
type SessionState struct {
	Findings  []rules.Finding `json:"findings"`
	UpdatedAt time.Time       `json:"updatedAt"`
}

// StatePath は root の状態ファイルのパス。
func StatePath(root string) string { return filepath.Join(root, filepath.FromSlash(StateFile)) }

// Put は session の状態を置き換え、maxSessions を超えた古いセッションを捨てる。
// Data は差分判定に不要で大きくなり得るので保存しない。
func (s *State) Put(session string, findings []rules.Finding, now time.Time) {
	if s.Sessions == nil {
		s.Sessions = map[string]SessionState{}
	}
	fs := make([]rules.Finding, 0, len(findings))
	for _, f := range findings {
		f.Data = nil
		fs = append(fs, f)
	}
	s.Sessions[session] = SessionState{Findings: fs, UpdatedAt: now}
	if len(s.Sessions) <= maxSessions {
		return
	}
	ids := make([]string, 0, len(s.Sessions))
	for id := range s.Sessions {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return s.Sessions[ids[i]].UpdatedAt.Before(s.Sessions[ids[j]].UpdatedAt) })
	for _, id := range ids[:len(ids)-maxSessions] {
		delete(s.Sessions, id)
	}
}

// LoadState は状態ファイルを読む。無い・壊れている場合は空の状態。
func LoadState(path string) State {
	var st State
	b, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(b, &st) != nil {
		return State{}
	}
	return st
}

// SaveState は一時ファイル経由で原子的に書き込む。
func SaveState(path string, st State) error {
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".depgraph-hook-state-*.tmp")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
