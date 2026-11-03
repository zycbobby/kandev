package api

import (
	"testing"

	"github.com/kandev/kandev/internal/agentctl/journal"
)

func TestDurableDeliveryInfoAdvertisesAdmissionBlocked(t *testing.T) {
	info := durableDeliveryInfoFromCapability(journal.StorageCapability{
		Version: journal.CurrentProtocolVersion, Durable: true,
		AdmissionBlocked: true, Reason: "delivery_admission_blocked",
	})
	if !info.Durable {
		t.Fatal("a blocked but readable journal must stay durable")
	}
	if !info.AdmissionBlocked {
		t.Fatal("initialize advertisement dropped blocked admission")
	}
	if info.Reason != "delivery_admission_blocked" {
		t.Fatalf("reason = %q", info.Reason)
	}
	healthy := durableDeliveryInfoFromCapability(journal.StorageCapability{
		Version: journal.CurrentProtocolVersion, Durable: true,
	})
	if healthy.AdmissionBlocked || healthy.Reason != "" {
		t.Fatalf("healthy advertisement = %#v", healthy)
	}
}
