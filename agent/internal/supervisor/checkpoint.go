package supervisor

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Checkpoint status values.
const (
	StatusIdle        = ""            // no in-flight turn
	StatusRunning     = "running"     // a turn is in flight right now
	StatusInterrupted = "interrupted" // §5.5 — a turn was cut short by drain/redeploy
)

// Checkpoint is §5.5's on-disk state, field for field:
//
//	{ taskId, sessionId, turnStartedAt, replySubject, natsMsgSeq,
//	  workspaceGitSha, status: "interrupted", attempt: n }
//
// SessionID is stored OUTSIDE Turn as well, because §5.6 makes it the
// agent-level authority ("sessionId | 권위 위치: 워크스페이스 볼륨
// /workspace/.osiris/state.json") — it survives across tasks and across
// container recreation, and it is what makes `--resume` possible after a
// Suspended→Provisioning round trip.
type Checkpoint struct {
	SessionID string `json:"sessionId"`

	TaskID          string    `json:"taskId,omitempty"`
	TurnStartedAt   time.Time `json:"turnStartedAt,omitempty"`
	ReplySubject    string    `json:"replySubject,omitempty"`
	NATSMsgSeq      uint64    `json:"natsMsgSeq,omitempty"`
	WorkspaceGitSha string    `json:"workspaceGitSha,omitempty"`
	Status          string    `json:"status,omitempty"`
	Attempt         int       `json:"attempt,omitempty"`

	// LastConfigHash lets the supervisor publish the §3.2.1
	// kind:"system_prompt" transcript record exactly once per rendered-prompt
	// CHANGE rather than once per task ("렌더 결과가 바뀔 때마다 supervisor가
	// 렌더된 프롬프트 전문을 ops.transcript.<employeeId>에 1회 발행").
	LastConfigHash string `json:"lastConfigHash,omitempty"`

	UpdatedAt time.Time `json:"updatedAt"`
}

func loadCheckpoint() (Checkpoint, error) {
	var cp Checkpoint
	b, err := os.ReadFile(StateFile)
	if err != nil {
		if os.IsNotExist(err) {
			return cp, nil // first boot — an empty checkpoint is the correct answer
		}
		return cp, err
	}
	if err := json.Unmarshal(b, &cp); err != nil {
		// A corrupt state.json must not wedge the agent forever. Losing the
		// sessionId costs one fresh conversation; refusing to boot costs the
		// whole agent. §5.5's at-least-once safety argument (external-effect
		// actions are all behind idempotent approval gates) is what makes
		// this the cheaper failure.
		return Checkpoint{}, fmt.Errorf("state.json is corrupt, starting fresh: %w", err)
	}
	return cp, nil
}

// saveCheckpoint writes state.json durably. §5.5 step 1 says "체크포인트
// fsync" and means it: without the fsync + atomic rename, a container killed
// during the write leaves a truncated file, i.e. exactly the "작업 유실" this
// whole mechanism exists to prevent.
func saveCheckpoint(cp Checkpoint) error {
	cp.UpdatedAt = time.Now().UTC()
	b, err := json.MarshalIndent(cp, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(StateFile), 0o755); err != nil {
		return err
	}
	tmp := StateFile + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, StateFile); err != nil {
		return err
	}
	// fsync the directory too, so the rename itself is durable.
	if d, err := os.Open(filepath.Dir(StateFile)); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}
