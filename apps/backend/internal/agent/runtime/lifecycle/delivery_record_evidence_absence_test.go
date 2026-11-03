package lifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync/atomic"
	"testing"

	agentctl "github.com/kandev/kandev/internal/agent/runtime/agentctl"
	"github.com/kandev/kandev/internal/agentctl/journal"
)

func absenceDescriptor(sessionID string) journal.RecoveryDescriptor {
	return journal.RecoveryDescriptor{
		StorageCapability: journal.StorageCapability{Version: journal.CurrentVersion, Durable: true},
		SessionID:         sessionID, IncarnationID: "incarnation", HarnessGeneration: 7,
	}
}

func TestInspectDeliveryRecordEvidenceRetainedAbsenceIsStable(t *testing.T) {
	ctx := context.Background()
	t.Run("missing submission is reported absent", func(t *testing.T) {
		client := ackTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch r.URL.Path {
			case "/api/v1/instances":
				_, _ = w.Write([]byte(`{"instances":[]}`))
			case "/api/v1/delivery/retained/evidence":
				var request journal.RetainedReconstructionEvidenceRequest
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Errorf("decode retained evidence request: %v", err)
				}
				if request.AbsentSubmissionID != "prompt:missing" {
					t.Errorf("absent submission id = %q", request.AbsentSubmissionID)
				}
				_ = json.NewEncoder(w).Encode(journal.RetainedReconstructionEvidence{
					Descriptor: absenceDescriptor("session"), SubmissionAbsent: true,
				})
			default:
				http.NotFound(w, r)
			}
		})
		manager := newTestManager(t)
		manager.dataDir = t.TempDir()
		manager.SetRuntimeOwner(runtimeOwnerForTestClient(t, client))

		evidence, err := manager.InspectDeliveryRecordEvidence(ctx, DeliveryRecordEvidenceRequest{
			TaskID: "task", SessionID: "session", IncarnationID: "incarnation", HarnessGeneration: 7,
			AbsentSubmissionID: "prompt:missing",
		})
		if err != nil {
			t.Fatal(err)
		}
		if !evidence.SubmissionAbsent {
			t.Fatal("missing retained submission was not reported absent")
		}
	})
	t.Run("changed evidence is stale", func(t *testing.T) {
		var reads atomic.Int32
		client := ackTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch r.URL.Path {
			case "/api/v1/instances":
				_, _ = w.Write([]byte(`{"instances":[]}`))
			case "/api/v1/delivery/retained/evidence":
				descriptor := absenceDescriptor("session")
				present := reads.Add(1) != 2
				if !present {
					descriptor.SubmissionCount = 1
				}
				_ = json.NewEncoder(w).Encode(journal.RetainedReconstructionEvidence{
					Descriptor: descriptor, SubmissionAbsent: present,
				})
			default:
				http.NotFound(w, r)
			}
		})
		manager := newTestManager(t)
		manager.dataDir = t.TempDir()
		manager.SetRuntimeOwner(runtimeOwnerForTestClient(t, client))

		_, err := manager.InspectDeliveryRecordEvidence(ctx, DeliveryRecordEvidenceRequest{
			TaskID: "task", SessionID: "session", IncarnationID: "incarnation", HarnessGeneration: 7,
			AbsentSubmissionID: "prompt:missing",
		})
		if !errors.Is(err, ErrDeliveryRecordEvidenceStale) {
			t.Fatalf("changed absence evidence error = %v, want stale", err)
		}
	})
}

func TestInspectDeliveryRecordEvidenceLiveAbsenceMaps404(t *testing.T) {
	ctx := context.Background()
	descriptor := absenceDescriptor("session")
	var instancePort int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/instances":
			_ = json.NewEncoder(w).Encode(map[string]any{"instances": []map[string]any{{
				"id": "live-execution", "task_id": "task", "session_id": "session",
				"port": instancePort, "listener_active": true,
			}}})
		case "/api/v1/agent/delivery":
			_ = json.NewEncoder(w).Encode(descriptor)
		case "/api/v1/agent/submissions/prompt:missing":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"code":"SUBMISSION_NOT_FOUND"}`))
		case "/api/v1/agent/submissions/prompt:present":
			_ = json.NewEncoder(w).Encode(journal.Submission{
				ID: "prompt:present", SessionID: "session", IncarnationID: "incarnation",
				HarnessGeneration: 7, State: journal.SubmissionCompleted, TerminalEventRetained: true,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	endpoint, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	instancePort, err = strconv.Atoi(endpoint.Port())
	if err != nil {
		t.Fatal(err)
	}
	client := agentctl.NewClient(endpoint.Hostname(), instancePort, newTestLogger())
	manager := newTestManager(t)
	manager.dataDir = t.TempDir()
	manager.SetRuntimeOwner(runtimeOwnerForTestClient(t, client))

	evidence, err := manager.InspectDeliveryRecordEvidence(ctx, DeliveryRecordEvidenceRequest{
		TaskID: "task", SessionID: "session", IncarnationID: "incarnation", HarnessGeneration: 7,
		AbsentSubmissionID: "prompt:missing",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !evidence.SubmissionAbsent {
		t.Fatal("live 404 submission was not reported absent")
	}
	evidence, err = manager.InspectDeliveryRecordEvidence(ctx, DeliveryRecordEvidenceRequest{
		TaskID: "task", SessionID: "session", IncarnationID: "incarnation", HarnessGeneration: 7,
		AbsentSubmissionID: "prompt:present",
	})
	if err != nil {
		t.Fatal(err)
	}
	if evidence.SubmissionAbsent {
		t.Fatal("live retained submission was reported absent")
	}
}
