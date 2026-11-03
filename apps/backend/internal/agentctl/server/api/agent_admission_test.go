package api

import (
	"errors"
	"testing"

	"github.com/kandev/kandev/internal/agentctl/journal"
	"github.com/kandev/kandev/internal/agentctl/server/process"
	"github.com/kandev/kandev/pkg/websocket"
)

func admissionErrorPayload(t *testing.T, err error) websocket.ErrorPayload {
	t.Helper()
	resp := durableAdmissionErrorResponse(&websocket.Message{ID: "msg-1", Action: "agent.prompt"}, "prompt:test", err)
	if resp == nil {
		t.Fatal("admission error produced no response")
	}
	var payload websocket.ErrorPayload
	if parseErr := resp.ParsePayload(&payload); parseErr != nil {
		t.Fatal(parseErr)
	}
	return payload
}

func TestDurableAdmissionErrorKinds(t *testing.T) {
	t.Run("pre-write rejection", func(t *testing.T) {
		payload := admissionErrorPayload(t, errors.Join(process.ErrDeliveryAdmissionRejected, journal.ErrJournalFull))
		if payload.Details["kind"] != "durable_delivery_admission_rejected" {
			t.Fatalf("kind = %v", payload.Details["kind"])
		}
		if payload.Details["reason"] != "journal_full" {
			t.Fatalf("reason = %v", payload.Details["reason"])
		}
	})
	t.Run("post-write uncertainty", func(t *testing.T) {
		payload := admissionErrorPayload(t, errors.Join(process.ErrDeliveryAdmissionUncertain, journal.ErrJournalCorrupt))
		if payload.Details["kind"] != "durable_delivery_admission_uncertain" {
			t.Fatalf("kind = %v", payload.Details["kind"])
		}
		if payload.Details["submission_id"] != "prompt:test" {
			t.Fatalf("submission id = %v", payload.Details["submission_id"])
		}
		if payload.Details["reason"] != "journal_corrupt" {
			t.Fatalf("reason = %v", payload.Details["reason"])
		}
	})
}

func TestDurableSubmissionResponseMarksUncertainty(t *testing.T) {
	resp := durableSubmissionResponse(&websocket.Message{ID: "msg-2", Action: "agent.prompt"}, journal.Submission{
		ID: "prompt:test", State: journal.SubmissionDispatching,
	})
	if resp == nil {
		t.Fatal("dispatching submission produced no response")
	}
	var payload websocket.ErrorPayload
	if err := resp.ParsePayload(&payload); err != nil {
		t.Fatal(err)
	}
	if payload.Details["kind"] != "durable_delivery_admission_uncertain" {
		t.Fatalf("kind = %v", payload.Details["kind"])
	}
}
