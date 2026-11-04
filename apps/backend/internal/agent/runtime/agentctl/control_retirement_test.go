package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kandev/kandev/internal/agentctl/journal"
	"github.com/kandev/kandev/internal/common/processidentity"
)

func TestControlClientRetiresRetainedDelivery(t *testing.T) {
	request := journal.RetainedRetirementRequest{
		Root: "/backend/session-data", SessionID: "session", ExecutionID: "execution",
		IncarnationID: "incarnation", HarnessGeneration: 7,
		Submissions: []journal.RetainedRetirementTarget{{
			ID: "prompt:one", StreamID: "stream", Hash: "hash",
			HarnessGeneration: 7, State: journal.SubmissionInterruptedUnknown,
		}},
		OriginalRuntime: processidentity.Identity{PID: 42, SessionID: 42, BirthToken: "linux:boot:42"},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/delivery/retained/retire" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer retire-secret" {
			t.Errorf("authorization = %q, want bearer token", got)
		}
		var received journal.RetainedRetirementRequest
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if received.SessionID != "session" || received.ExecutionID != "execution" ||
			received.IncarnationID != "incarnation" || received.HarnessGeneration != 7 ||
			len(received.Submissions) != 1 || received.Submissions[0].ID != "prompt:one" ||
			received.OriginalRuntime != request.OriginalRuntime {
			t.Errorf("retirement request = %+v", received)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(journal.RetainedRetirementResponse{
			Descriptor: journal.RecoveryDescriptor{
				StorageCapability: journal.StorageCapability{Version: journal.CurrentVersion, Durable: true},
				SessionID:         "session", IncarnationID: "incarnation", HarnessGeneration: 7,
			},
			Submissions: []journal.Submission{{
				ID: "prompt:one", StreamID: "stream", Hash: "hash",
				HarnessGeneration: 7, State: journal.SubmissionInterruptedUnknown, Retired: true,
			}},
			ProcessTerminated: true,
		})
	}))
	t.Cleanup(server.Close)
	client := newTestControlClient(t, server, WithControlAuthToken("retire-secret"))

	response, err := client.RetireRetainedDelivery(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !response.ProcessTerminated || len(response.Submissions) != 1 || !response.Submissions[0].Retired {
		t.Fatalf("retirement response = %+v", response)
	}
	if response.Descriptor.SessionID != "session" || response.Descriptor.IncarnationID != "incarnation" {
		t.Fatalf("retirement descriptor = %+v", response.Descriptor)
	}
}

func TestControlClientRetireRetainedDeliveryPreservesOwnerMismatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"code":"OWNER_MISMATCH","message":"OWNER_MISMATCH"}`))
	}))
	t.Cleanup(server.Close)
	client := newTestControlClient(t, server)

	_, err := client.RetireRetainedDelivery(context.Background(), journal.RetainedRetirementRequest{
		SessionID: "session", ExecutionID: "execution", IncarnationID: "incarnation", HarnessGeneration: 7,
		Submissions:     []journal.RetainedRetirementTarget{{ID: "prompt:one"}},
		OriginalRuntime: processidentity.Identity{PID: 1, BirthToken: "linux:boot:1"},
	})
	var httpErr *DeliveryHTTPError
	if !errors.As(err, &httpErr) || httpErr.StatusCode != http.StatusConflict {
		t.Fatalf("retirement error = %v", err)
	}
	if !json.Valid([]byte(httpErr.Body)) {
		t.Fatalf("retirement error body = %q", httpErr.Body)
	}
}
