package statsync

import (
	"errors"
	"strings"
	"testing"
)

func validEnvelope() *Envelope {
	completed := true
	archived := "2026-10-12T10:00:00Z"
	filesChanged := int64(3)
	return &Envelope{
		SchemaVersion:  SchemaVersion,
		SourceInstance: "remote-1",
		Mode:           ModeDelta,
		Repositories: []RepositoryRow{
			{UID: "remote-1:repo-1", Name: "kandev"},
		},
		Tasks: []TaskRow{
			{
				UID:             "remote-1:task-1",
				OrigWorkspaceID: "ws-remote",
				Title:           "Fix the thing",
				State:           "DONE",
				ArchivedAt:      &archived,
				Completed:       &completed,
				CreatedAt:       "2026-10-11T09:00:00Z",
				UpdatedAt:       "2026-10-12T10:00:00Z",
			},
		},
		TaskRepositories: []TaskRepositoryRow{
			{UID: "remote-1:tr-1", TaskUID: "remote-1:task-1", RepositoryID: "remote-1:repo-1"},
		},
		Sessions: []SessionRow{
			{
				UID:                  "remote-1:sess-1",
				TaskUID:              "remote-1:task-1",
				StartedAt:            "2026-10-11T09:05:00Z",
				RepositoryID:         "remote-1:repo-1",
				AgentProfileSnapshot: []byte(`{"model":"claude-opus-5-5","model_name":"Opus","llm":"claude"}`),
			},
		},
		Turns: []TurnRow{
			{UID: "remote-1:turn-1", SessionUID: "remote-1:sess-1", StartedAt: "2026-10-11T09:05:00Z"},
		},
		Messages: []MessageRow{
			{UID: "remote-1:msg-1", SessionUID: "remote-1:sess-1", TurnUID: "remote-1:turn-1", AuthorType: "user", Type: "text", CreatedAt: "2026-10-11T09:05:01Z"},
		},
		Commits: []CommitRow{
			{UID: "remote-1:commit-1", SessionUID: "remote-1:sess-1", CommitSHA: "abc123", CommittedAt: "2026-10-11T10:00:00Z", FilesChanged: &filesChanged},
		},
	}
}

func TestValidateEnvelopeAcceptsValidDelta(t *testing.T) {
	env := validEnvelope()
	if err := ValidateEnvelope(env); err != nil {
		t.Fatalf("ValidateEnvelope() = %v, want nil", err)
	}
}

func TestValidateEnvelopeRejectsUnknownSchemaVersion(t *testing.T) {
	env := validEnvelope()
	env.SchemaVersion = SchemaVersion + 1
	err := ValidateEnvelope(env)
	if !errors.Is(err, ErrUnsupportedSchemaVersion) {
		t.Fatalf("ValidateEnvelope() = %v, want ErrUnsupportedSchemaVersion", err)
	}
}

func TestValidateEnvelopeRejectsMissingSourceInstance(t *testing.T) {
	env := validEnvelope()
	env.SourceInstance = ""
	if err := ValidateEnvelope(env); !errors.Is(err, ErrMissingSourceInstance) {
		t.Fatalf("ValidateEnvelope() = %v, want ErrMissingSourceInstance", err)
	}
}

func TestValidateEnvelopeRejectsInvalidMode(t *testing.T) {
	env := validEnvelope()
	env.Mode = "full"
	if err := ValidateEnvelope(env); !errors.Is(err, ErrInvalidMode) {
		t.Fatalf("ValidateEnvelope() = %v, want ErrInvalidMode", err)
	}
}

func TestValidateEnvelopeReconcileRequiresInventory(t *testing.T) {
	env := validEnvelope()
	env.Mode = ModeReconcile
	if err := ValidateEnvelope(env); !errors.Is(err, ErrMissingInventory) {
		t.Fatalf("ValidateEnvelope() = %v, want ErrMissingInventory", err)
	}
	env.Inventory = &Inventory{Tasks: []string{"remote-1:task-1"}}
	if err := ValidateEnvelope(env); err != nil {
		t.Fatalf("ValidateEnvelope() with inventory = %v, want nil", err)
	}
}

func TestValidateEnvelopeRejectsRowMissingRequiredField(t *testing.T) {
	tests := []struct {
		name    string
		breaker func(env *Envelope)
		wantSub string
	}{
		{
			name:    "task without orig_workspace_id",
			breaker: func(env *Envelope) { env.Tasks[0].OrigWorkspaceID = "" },
			wantSub: "tasks[0]",
		},
		{
			name:    "session without started_at",
			breaker: func(env *Envelope) { env.Sessions[0].StartedAt = "" },
			wantSub: "sessions[0]",
		},
		{
			name:    "message without author_type",
			breaker: func(env *Envelope) { env.Messages[0].AuthorType = "" },
			wantSub: "messages[0]",
		},
		{
			name:    "commit without committed_at",
			breaker: func(env *Envelope) { env.Commits[0].CommittedAt = "" },
			wantSub: "commits[0]",
		},
		{
			name:    "task repository without repository_id",
			breaker: func(env *Envelope) { env.TaskRepositories[0].RepositoryID = "" },
			wantSub: "task_repositories[0]",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := validEnvelope()
			tt.breaker(env)
			err := ValidateEnvelope(env)
			if !errors.Is(err, ErrInvalidRow) {
				t.Fatalf("ValidateEnvelope() = %v, want ErrInvalidRow", err)
			}
			if !strings.Contains(err.Error(), tt.wantSub) {
				t.Fatalf("ValidateEnvelope() = %v, want error naming %s", err, tt.wantSub)
			}
		})
	}
}

func TestValidateEnvelopeRejectsForeignUID(t *testing.T) {
	env := validEnvelope()
	env.Tasks[0].UID = "other-source:task-1"
	err := ValidateEnvelope(env)
	if !errors.Is(err, ErrInvalidRow) {
		t.Fatalf("ValidateEnvelope() = %v, want ErrInvalidRow", err)
	}
}

func TestValidateEnvelopeRejectsForeignInventoryUID(t *testing.T) {
	env := validEnvelope()
	env.Mode = ModeReconcile
	env.Inventory = &Inventory{Tasks: []string{"remote-1:task-1", "elsewhere:task-2"}}
	err := ValidateEnvelope(env)
	if err == nil || !strings.Contains(err.Error(), "inventory") {
		t.Fatalf("ValidateEnvelope() = %v, want inventory error", err)
	}
}

func TestValidateEnvelopeRejectsNonRFC3339Timestamp(t *testing.T) {
	env := validEnvelope()
	env.Tasks[0].CreatedAt = "2026-10-11 09:00:00"
	err := ValidateEnvelope(env)
	if !errors.Is(err, ErrInvalidRow) {
		t.Fatalf("ValidateEnvelope() = %v, want ErrInvalidRow", err)
	}
}

func TestValidateEnvelopeRejectsInvalidSnapshotJSON(t *testing.T) {
	env := validEnvelope()
	env.Sessions[0].AgentProfileSnapshot = []byte("{not json")
	if err := ValidateEnvelope(env); !errors.Is(err, ErrInvalidRow) {
		t.Fatalf("ValidateEnvelope() = %v, want ErrInvalidRow", err)
	}
}

func TestParseTimestampRoundTrips(t *testing.T) {
	parsed, err := ParseTimestamp("2026-10-12T07:44:36.447135Z")
	if err != nil {
		t.Fatalf("ParseTimestamp() = %v, want nil", err)
	}
	if parsed.IsZero() {
		t.Fatal("ParseTimestamp() returned zero time for a valid value")
	}
	if _, err := ParseTimestamp("nope"); err == nil {
		t.Fatal("ParseTimestamp() accepted a non-RFC3339 value")
	}
}
