package hook

import (
	"bytes"
	"context"
	"encoding/json"
	"time"

	"github.com/nonchan7720/depgraph/pkg/domain/rules"
	"github.com/nonchan7720/depgraph/pkg/domain/view"
)

// Snapshot は 1 プロジェクト分の解析結果。
type Snapshot struct {
	Stats    view.Stats
	Findings []rules.Finding
	// Sessions is the session state of the same project.
	Sessions SessionStore
}

// ProjectData は「root のデータが DB にあるか」を判定し、あれば解析結果を返す。
// 読み取りのみ（ルール評価は読み取りクエリ。CLAUDE.md は更新しない）。
// 実装は root からプロジェクト key を決め、そのプロジェクトにスコープして読む（cmd の projectData）。
type ProjectData interface {
	// Load は root のデータが無ければ ok=false を返す（別プロジェクトのデータしか無い等）。
	Load(ctx context.Context, root string) (snap Snapshot, ok bool, err error)
}

// Run は ev に応じて注入する文脈を返す（"" は何も注入しない）。
//
//   - root のデータが無ければ何もしない（別プロジェクトの違反を注入しない）。
//   - SessionStart: 全体の要約を返し、そのセッションの前回状態を現在の Findings にする。
//   - Prompt: 前回状態からの差分だけを返す。前回状態が無ければ要約を返す。
//     session_id が無いと差分を追えないので何もしない。
func Run(ctx context.Context, ev Event, in Input, root string, src ProjectData, now time.Time) (string, error) {
	if root == "" || (ev == Prompt && in.SessionID == "") {
		return "", nil
	}
	removeLegacyState(root)
	snap, ok, err := src.Load(ctx, root)
	if err != nil || !ok {
		return "", err
	}
	lim := DefaultLimits()
	var prev []rules.Finding
	seen := false
	if in.SessionID != "" && snap.Sessions != nil {
		if prev, seen, err = snap.Sessions.Get(ctx, in.SessionID); err != nil {
			seen = false // unknown state: summarise instead of diffing
		}
	}

	var out string
	if ev == Prompt && seen {
		added, removed := rules.Diff(prev, snap.Findings)
		out = RenderDiff(added, removed, snap.Findings, lim)
	} else {
		out = Summarize(snap.Stats, snap.Findings, lim)
	}
	if in.SessionID != "" && snap.Sessions != nil && (out != "" || !seen) {
		if err := snap.Sessions.Put(ctx, in.SessionID, snap.Findings, now); err != nil {
			// 状態が保存できなくても注入はする（次回も同じ差分が出るだけ）。
			return out, err
		}
	}
	return out, nil
}

// Output は Claude Code の hook 出力 JSON を返す。context が空なら nil（何も出力しない）。
func Output(ev Event, context string) []byte {
	if context == "" {
		return nil
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false) // "a -> b" を \u003e にしない
	_ = enc.Encode(map[string]any{
		"hookSpecificOutput": map[string]any{
			"hookEventName":     string(ev),
			"additionalContext": context,
		},
	})
	return buf.Bytes()
}
