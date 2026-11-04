//go:build linux

package lifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	agentctl "github.com/kandev/kandev/internal/agent/runtime/agentctl"
	"github.com/kandev/kandev/internal/agentctl/journal"
	"github.com/kandev/kandev/internal/common/processidentity"
)

const (
	retirementTaskID      = "task-retirement"
	retirementSessionID   = "session-retirement"
	retirementIncarnation = "incarnation-retirement"
	retirementExecutionID = "old-execution"
	retirementStreamID    = "stream-retirement"
	retirementPromptID    = "prompt:retirement"
	retirementGeneration  = uint64(7)
)

func deadDeliveryRecordOwner(t *testing.T) processidentity.Identity {
	t.Helper()
	if _, err := exec.LookPath("setsid"); err != nil {
		t.Skip("setsid is unavailable; an isolated owner session cannot be created")
	}
	command := exec.Command("setsid", "/bin/sleep", "60")
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = command.Process.Kill(); _ = command.Wait() })
	identity := awaitDeliveryRecordSessionLeader(t, command.Process.Pid)
	if err := command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = command.Wait()
	return identity
}

func awaitDeliveryRecordSessionLeader(t *testing.T, pid int) processidentity.Identity {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		identity, err := processidentity.Capture(pid)
		if err == nil && identity.SessionID == identity.PID && identity.GroupID == identity.PID {
			return identity
		}
		if time.Now().After(deadline) {
			t.Fatalf("process %d did not become a session leader: %+v, %v", pid, identity, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func retirementSummary() journal.SubmissionSummary {
	return journal.SubmissionSummary{
		ID: retirementPromptID, SessionID: retirementSessionID, IncarnationID: retirementIncarnation,
		HarnessGeneration: retirementGeneration, StreamID: retirementStreamID,
		Hash:  journal.SubmissionHash([]byte(`{"text":"abandoned instruction"}`)),
		State: journal.SubmissionInterruptedUnknown,
	}
}

func retirementDescriptor(summaries ...journal.SubmissionSummary) journal.RecoveryDescriptor {
	return journal.RecoveryDescriptor{
		StorageCapability: journal.StorageCapability{Version: journal.CurrentProtocolVersion, Durable: true},
		SessionID:         retirementSessionID, IncarnationID: retirementIncarnation,
		HarnessGeneration: retirementGeneration, Submissions: summaries, SubmissionCount: len(summaries),
	}
}

func retirementTarget(summary journal.SubmissionSummary) DeliveryRecordRetirementTarget {
	return DeliveryRecordRetirementTarget{
		ID: summary.ID, StreamID: summary.StreamID, Hash: summary.Hash,
		HarnessGeneration: summary.HarnessGeneration, State: summary.State,
	}
}

func retirementRequest(owner processidentity.Identity, targets ...DeliveryRecordRetirementTarget) DeliveryRecordRetirementRequest {
	if len(targets) == 0 {
		targets = []DeliveryRecordRetirementTarget{retirementTarget(retirementSummary())}
	}
	return DeliveryRecordRetirementRequest{
		TaskID: retirementTaskID, SessionID: retirementSessionID, ExecutionID: retirementExecutionID,
		IncarnationID: retirementIncarnation, HarnessGeneration: retirementGeneration,
		Submissions: targets, OriginalRuntime: owner,
	}
}

func retirementTestManager(t *testing.T, handler http.HandlerFunc) *Manager {
	t.Helper()
	manager := newTestManager(t)
	manager.dataDir = t.TempDir()
	manager.SetRuntimeOwner(runtimeOwnerForTestClient(t, ackTestClient(t, handler)))
	return manager
}

func TestRetireDeliveryRecordSubmissionsRetiresDeadOwnerEvidence(t *testing.T) {
	var retireCalls atomic.Int32
	owner := deadDeliveryRecordOwner(t)
	manager := newTestManager(t)
	manager.dataDir = t.TempDir()
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/instances":
			_, _ = w.Write([]byte(`{"instances":[]}`))
		case "/api/v1/delivery/retained/evidence":
			_ = json.NewEncoder(w).Encode(journal.RetainedReconstructionEvidence{Descriptor: retirementDescriptor(retirementSummary())})
		case "/api/v1/delivery/retained/retire":
			retireCalls.Add(1)
			var received journal.RetainedRetirementRequest
			if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
				t.Error(err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if received.Root != manager.dataDir || received.SessionID != retirementSessionID ||
				received.ExecutionID != retirementExecutionID || received.IncarnationID != retirementIncarnation ||
				received.HarnessGeneration != retirementGeneration || received.OriginalRuntime != owner ||
				len(received.Submissions) != 1 || received.Submissions[0].ID != retirementPromptID {
				t.Errorf("retirement request = %+v", received)
			}
			retired := journal.Submission{
				ID: retirementPromptID, SessionID: retirementSessionID, IncarnationID: retirementIncarnation,
				HarnessGeneration: retirementGeneration, StreamID: retirementStreamID,
				Hash: retirementSummary().Hash, State: journal.SubmissionInterruptedUnknown, Retired: true,
			}
			_ = json.NewEncoder(w).Encode(journal.RetainedRetirementResponse{
				Descriptor: retirementDescriptor(), Submissions: []journal.Submission{retired}, ProcessTerminated: true,
			})
		default:
			t.Errorf("unexpected runtime request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}
	manager.SetRuntimeOwner(runtimeOwnerForTestClient(t, ackTestClient(t, handler)))

	result, err := manager.RetireDeliveryRecordSubmissions(context.Background(), retirementRequest(owner))
	if err != nil {
		t.Fatal(err)
	}
	if !result.ProcessTerminated || len(result.Retired) != 1 || !result.Retired[0].Retired {
		t.Fatalf("retirement result = %+v", result)
	}
	if result.Descriptor.Unresolved || result.Descriptor.SubmissionCount != 0 || len(result.Descriptor.Submissions) != 0 {
		t.Fatalf("post-retirement descriptor = %+v", result.Descriptor)
	}
	if retireCalls.Load() != 1 {
		t.Fatalf("retained retirement calls = %d", retireCalls.Load())
	}
}

func TestRetireDeliveryRecordSubmissionsRetiresThroughLiveSuccessor(t *testing.T) {
	ctx := context.Background()
	summary := retirementSummary()
	var retired atomic.Bool
	var retireCalls atomic.Int32
	var instancePort int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/instances":
			_ = json.NewEncoder(w).Encode(map[string]any{"instances": []map[string]any{{
				"id": "successor-execution", "task_id": retirementTaskID, "session_id": retirementSessionID,
				"port": instancePort, "listener_active": true,
			}}})
		case "/api/v1/agent/delivery":
			if retired.Load() {
				_ = json.NewEncoder(w).Encode(retirementDescriptor())
				return
			}
			_ = json.NewEncoder(w).Encode(retirementDescriptor(summary))
		case "/api/v1/agent/submissions/" + retirementPromptID + "/retire":
			retireCalls.Add(1)
			retired.Store(true)
			w.WriteHeader(http.StatusNoContent)
		case "/api/v1/agent/submissions/" + retirementPromptID:
			submission := journal.Submission{
				ID: summary.ID, SessionID: summary.SessionID, IncarnationID: summary.IncarnationID,
				HarnessGeneration: summary.HarnessGeneration, StreamID: summary.StreamID,
				Hash: summary.Hash, State: summary.State, Retired: retired.Load(),
			}
			_ = json.NewEncoder(w).Encode(submission)
		default:
			t.Errorf("unexpected runtime request %s %s", r.Method, r.URL.Path)
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
	controlClient := agentctl.NewClient(endpoint.Hostname(), instancePort, newTestLogger())
	manager := newTestManager(t)
	manager.dataDir = t.TempDir()
	manager.SetRuntimeOwner(runtimeOwnerForTestClient(t, controlClient))

	result, err := manager.RetireDeliveryRecordSubmissions(ctx, retirementRequest(deadDeliveryRecordOwner(t)))
	if err != nil {
		t.Fatal(err)
	}
	if !result.ProcessTerminated || len(result.Retired) != 1 || !result.Retired[0].Retired {
		t.Fatalf("live retirement result = %+v", result)
	}
	if retireCalls.Load() != 1 {
		t.Fatalf("live retirement calls = %d", retireCalls.Load())
	}
	if result.Descriptor.Unresolved || result.Descriptor.SubmissionCount != 0 {
		t.Fatalf("live post-retirement descriptor = %+v", result.Descriptor)
	}
}

func TestRetireDeliveryRecordSubmissionsRejectsLiveOwner(t *testing.T) {
	t.Run("live historical process", func(t *testing.T) {
		var retireCalls atomic.Int32
		manager := retirementTestManager(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch r.URL.Path {
			case "/api/v1/instances":
				_, _ = w.Write([]byte(`{"instances":[]}`))
			case "/api/v1/delivery/retained/evidence", "/api/v1/delivery/retained/retire":
				retireCalls.Add(1)
				_ = json.NewEncoder(w).Encode(journal.RetainedReconstructionEvidence{Descriptor: retirementDescriptor(retirementSummary())})
			default:
				http.NotFound(w, r)
			}
		})
		owner, err := processidentity.Capture(os.Getpid())
		if err != nil {
			t.Fatal(err)
		}
		_, err = manager.RetireDeliveryRecordSubmissions(context.Background(), retirementRequest(owner))
		if !errors.Is(err, ErrDeliveryRecoveryBlocked) || !errors.Is(err, ErrDeliveryOwnerMismatch) {
			t.Fatalf("live process retirement error = %v", err)
		}
		if retireCalls.Load() != 0 {
			t.Fatal("a live owner must not reach retirement transport")
		}
	})

	t.Run("live original execution", func(t *testing.T) {
		var transportCalls atomic.Int32
		var instancePort int
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch r.URL.Path {
			case "/api/v1/instances":
				_ = json.NewEncoder(w).Encode(map[string]any{"instances": []map[string]any{{
					"id": retirementExecutionID, "task_id": retirementTaskID, "session_id": retirementSessionID,
					"port": instancePort, "listener_active": true,
				}}})
			default:
				transportCalls.Add(1)
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
		controlClient := agentctl.NewClient(endpoint.Hostname(), instancePort, newTestLogger())
		manager := newTestManager(t)
		manager.dataDir = t.TempDir()
		manager.SetRuntimeOwner(runtimeOwnerForTestClient(t, controlClient))

		_, err = manager.RetireDeliveryRecordSubmissions(context.Background(), retirementRequest(deadDeliveryRecordOwner(t)))
		if !errors.Is(err, ErrDeliveryRecoveryBlocked) || !errors.Is(err, ErrDeliveryOwnerMismatch) {
			t.Fatalf("live execution retirement error = %v", err)
		}
		if transportCalls.Load() != 0 {
			t.Fatal("the live original execution must not reach retirement transport")
		}
	})

	t.Run("missing process proof", func(t *testing.T) {
		manager := retirementTestManager(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if r.URL.Path == "/api/v1/instances" {
				_, _ = w.Write([]byte(`{"instances":[]}`))
				return
			}
			t.Errorf("missing proof reached runtime request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		})
		_, err := manager.RetireDeliveryRecordSubmissions(context.Background(), retirementRequest(processidentity.Identity{}))
		if !errors.Is(err, ErrDeliveryRecoveryBlocked) || !errors.Is(err, ErrDeliveryOwnerMismatch) {
			t.Fatalf("missing proof retirement error = %v", err)
		}
	})
}

func TestRetireDeliveryRecordSubmissionsRejectsTerminalOrMismatchedTarget(t *testing.T) {
	terminal := retirementSummary()
	terminal.TerminalEventRetained = true
	terminal.TerminalSequence = 3
	peer := retirementSummary()
	peer.ID = "prompt:peer"
	peer.StreamID = "stream-peer"

	cases := map[string]struct {
		descriptor journal.RecoveryDescriptor
		targets    func() []DeliveryRecordRetirementTarget
	}{
		"terminal target": {
			descriptor: retirementDescriptor(terminal),
			targets: func() []DeliveryRecordRetirementTarget {
				return []DeliveryRecordRetirementTarget{retirementTarget(retirementSummary())}
			},
		},
		"hash mismatch": {
			descriptor: retirementDescriptor(retirementSummary()),
			targets: func() []DeliveryRecordRetirementTarget {
				target := retirementTarget(retirementSummary())
				target.Hash = "different-hash"
				return []DeliveryRecordRetirementTarget{target}
			},
		},
		"unlisted unresolved peer": {
			descriptor: retirementDescriptor(retirementSummary(), peer),
			targets: func() []DeliveryRecordRetirementTarget {
				return []DeliveryRecordRetirementTarget{retirementTarget(retirementSummary())}
			},
		},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			var retireCalls atomic.Int32
			manager := retirementTestManager(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/v1/instances":
					_, _ = w.Write([]byte(`{"instances":[]}`))
				case "/api/v1/delivery/retained/evidence":
					_ = json.NewEncoder(w).Encode(journal.RetainedReconstructionEvidence{Descriptor: testCase.descriptor})
				case "/api/v1/delivery/retained/retire":
					retireCalls.Add(1)
					http.NotFound(w, r)
				default:
					http.NotFound(w, r)
				}
			})
			request := retirementRequest(deadDeliveryRecordOwner(t), testCase.targets()...)
			_, err := manager.RetireDeliveryRecordSubmissions(context.Background(), request)
			if !errors.Is(err, ErrDeliveryRecoveryBlocked) {
				t.Fatalf("retirement error = %v, want blocked", err)
			}
			if retireCalls.Load() != 0 {
				t.Fatal("an invalid target must not reach retirement transport")
			}
		})
	}
}
