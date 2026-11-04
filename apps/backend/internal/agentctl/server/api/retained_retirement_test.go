package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/kandev/kandev/internal/agentctl/journal"
	"github.com/kandev/kandev/internal/agentctl/server/config"
	"github.com/kandev/kandev/internal/agentctl/server/instance"
	"github.com/kandev/kandev/internal/agentctl/server/process"
	"github.com/kandev/kandev/internal/common/logger"
	"github.com/kandev/kandev/internal/common/processidentity"
	"github.com/kandev/kandev/pkg/agent"
)

const (
	retirementAPISession     = "session-retirement-api"
	retirementAPIIncarnation = "incarnation-retirement-api"
	retirementAPIStream      = "stream-retirement-api"
	retirementAPISubmission  = "prompt:retirement-api"
	retirementAPIGeneration  = uint64(4)
)

// deadRetirementAPIOwner spawns an isolated session leader, captures it, and
// reaps it so retirement can prove the historical owner terminated.
func deadRetirementAPIOwner(t *testing.T) processidentity.Identity {
	t.Helper()
	if _, err := exec.LookPath("setsid"); err != nil {
		t.Skip("setsid is unavailable; an isolated owner session cannot be created")
	}
	command := exec.Command("setsid", "/bin/sleep", "60")
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = command.Process.Kill(); _ = command.Wait() })
	identity := awaitRetirementAPISessionLeader(t, command.Process.Pid)
	if err := command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = command.Wait()
	return identity
}

func awaitRetirementAPISessionLeader(t *testing.T, pid int) processidentity.Identity {
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

func seedRetirementAPIJournal(t *testing.T, submissionID string, terminal bool) (string, journal.Submission) {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	capability := journal.CheckStorage(root, retirementAPISession)
	retained, err := journal.Open(journal.Config{Path: capability.Path})
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte(`{"text":"abandoned instruction"}`)
	submission := journal.Submission{
		ID: submissionID, SessionID: retirementAPISession, IncarnationID: retirementAPIIncarnation,
		HarnessGeneration: retirementAPIGeneration, StreamID: retirementAPIStream,
		Hash: journal.SubmissionHash(payload), Payload: payload, State: journal.SubmissionInterruptedUnknown,
	}
	if terminal {
		submission.TerminalEventRetained = true
		submission.TerminalSequence = 1
	}
	if _, err = retained.PutSubmission(ctx, submission); err != nil {
		t.Fatal(err)
	}
	if err = retained.Close(); err != nil {
		t.Fatal(err)
	}
	return root, submission
}

func retirementAPIRequestBody(root, submissionID, hash string, owner processidentity.Identity) map[string]any {
	return map[string]any{
		"root": root, "session_id": retirementAPISession, "execution_id": "execution-retirement-api",
		"incarnation_id": retirementAPIIncarnation, "harness_generation": retirementAPIGeneration,
		"submissions": []map[string]any{{
			"id": submissionID, "stream_id": retirementAPIStream, "hash": hash,
			"harness_generation": retirementAPIGeneration, "state": string(journal.SubmissionInterruptedUnknown),
		}},
		"original_runtime": owner,
	}
}

func postRetirementAPI(t *testing.T, server *ControlServer, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/delivery/retained/retire", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()
	server.Router().ServeHTTP(response, request)
	return response
}

func TestRetainedRetirementRetiresUnownedDeadOwnerJournal(t *testing.T) {
	root, submission := seedRetirementAPIJournal(t, retirementAPISubmission, false)
	manager := &instance.Manager{}
	server := NewControlServer(&config.Config{AuthToken: "secret"}, manager, logger.Default())
	body, err := json.Marshal(retirementAPIRequestBody(root, submission.ID, submission.Hash, deadRetirementAPIOwner(t)))
	if err != nil {
		t.Fatal(err)
	}
	response := postRetirementAPI(t, server, body)
	if response.Code != http.StatusOK {
		t.Fatalf("retained retirement status=%d: %s", response.Code, response.Body.String())
	}
	payload := []byte(`{"text":"abandoned instruction"}`)
	if bytes.Contains(response.Body.Bytes(), payload) {
		t.Fatalf("retirement response exposed retained prompt payload: %s", response.Body.String())
	}
	var result journal.RetainedRetirementResponse
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if !result.ProcessTerminated || result.Descriptor.Unresolved || len(result.Submissions) != 1 || !result.Submissions[0].Retired {
		t.Fatalf("retained retirement = %+v", result)
	}
	if result.Submissions[0].Payload != nil {
		t.Fatal("retirement response must not carry the retained payload")
	}
	if len(manager.ListInstances()) != 0 {
		t.Fatal("retained retirement created a runtime instance")
	}
}

func TestRetainedRetirementRejectsLiveInstanceOwner(t *testing.T) {
	root, submission := seedRetirementAPIJournal(t, retirementAPISubmission, false)
	log := logger.Default()
	manager := instance.NewManager(&config.Config{
		Ports:    config.PortConfig{Base: 0, Max: 0},
		Defaults: config.InstanceDefaults{Protocol: agent.ProtocolACP},
	}, log)
	t.Cleanup(func() { _ = manager.Shutdown(context.Background()) })
	manager.SetServerFactory(func(*config.InstanceConfig, *process.Manager, *logger.Logger) http.Handler {
		return http.NotFoundHandler()
	})
	created, err := manager.CreateInstance(context.Background(), &instance.CreateRequest{
		WorkspacePath: t.TempDir(),
		SessionID:     retirementAPISession,
		TaskID:        "task-retirement-api",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.StopInstance(context.Background(), created.ID) })
	server := NewControlServer(&config.Config{AuthToken: "secret"}, manager, log)
	body, err := json.Marshal(retirementAPIRequestBody(root, submission.ID, submission.Hash, deadRetirementAPIOwner(t)))
	if err != nil {
		t.Fatal(err)
	}
	response := postRetirementAPI(t, server, body)
	if response.Code != http.StatusConflict {
		t.Fatalf("live instance retirement status=%d: %s", response.Code, response.Body.String())
	}
	var result map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result["code"] != deliveryOwnerMismatchCode {
		t.Fatalf("live instance retirement code = %v", result["code"])
	}
	reopened, err := journal.Open(journal.Config{Path: journal.CheckStorage(root, retirementAPISession).Path, ExistingOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	stored, err := reopened.GetSubmission(context.Background(), submission.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Retired {
		t.Fatal("a live instance must not be retired through the retained route")
	}
}

func TestRetainedRetirementRejectsInvalidIdentityOrBody(t *testing.T) {
	root, submission := seedRetirementAPIJournal(t, retirementAPISubmission, false)
	manager := &instance.Manager{}
	server := NewControlServer(&config.Config{AuthToken: "secret"}, manager, logger.Default())
	cases := map[string]map[string]any{
		"missing session":    {"root": root, "execution_id": "execution", "incarnation_id": retirementAPIIncarnation, "harness_generation": retirementAPIGeneration},
		"missing execution":  {"root": root, "session_id": retirementAPISession, "incarnation_id": retirementAPIIncarnation, "harness_generation": retirementAPIGeneration},
		"missing generation": {"root": root, "session_id": retirementAPISession, "execution_id": "execution", "incarnation_id": retirementAPIIncarnation},
		"missing submissions": {
			"root": root, "session_id": retirementAPISession, "execution_id": "execution",
			"incarnation_id": retirementAPIIncarnation, "harness_generation": retirementAPIGeneration,
		},
		"target without hash": {
			"root": root, "session_id": retirementAPISession, "execution_id": "execution",
			"incarnation_id": retirementAPIIncarnation, "harness_generation": retirementAPIGeneration,
			"submissions": []map[string]any{{"id": submission.ID, "stream_id": retirementAPIStream, "harness_generation": retirementAPIGeneration, "state": string(journal.SubmissionInterruptedUnknown)}},
		},
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			encoded, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			response := postRetirementAPI(t, server, encoded)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("invalid retirement body status=%d: %s", response.Code, response.Body.String())
			}
			var result map[string]any
			if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if result["code"] != "INVALID_RECOVERY_IDENTITY" {
				t.Fatalf("invalid retirement body code = %v", result["code"])
			}
		})
	}
	liveOwner, err := processidentity.Capture(os.Getpid())
	if err != nil {
		t.Logf("live process identity unavailable: %v", err)
		return
	}
	valid := retirementAPIRequestBody(root, submission.ID, submission.Hash, liveOwner)
	encoded, err := json.Marshal(valid)
	if err != nil {
		t.Fatal(err)
	}
	response := postRetirementAPI(t, server, encoded)
	if response.Code != http.StatusConflict {
		t.Fatalf("live owner identity status=%d: %s", response.Code, response.Body.String())
	}
}
