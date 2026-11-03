---
status: draft
system: platform
created: 2026-11-04
updated: 2026-11-04
owners:
  - kandev
---

# Recovery of missing durable delivery records

## Overview

This contract extends delivery recovery for existing installations. It does not
change the authority of the canonical conversation.

### REQ-PLATFORM-DURABLE-AGENT-DELIVERY-008: Recovery of missing delivery records

**Intent:** A retained delivery record can restore a missing backend association without repeating the interrupted instruction.

#### Acceptance criteria

- **AC-PLATFORM-DURABLE-AGENT-DELIVERY-008.1:** When an authorized user retries recovery, Kandev shall recover a uniquely verified retained submission whose backend association is missing. Recovery shall preserve the session, conversation, workspace, and recorded uncertainty.
- **AC-PLATFORM-DURABLE-AGENT-DELIVERY-008.2:** Recovery shall refuse conflicting owners, incomplete candidate sets, invalid payloads, and ambiguous submissions. One valid candidate shall not override another unresolved candidate. Missing process evidence shall never imply termination.
- **AC-PLATFORM-DURABLE-AGENT-DELIVERY-008.3:** Concurrent recovery requests and retries after interruption shall create at most one association. Conflicting existing records shall remain unchanged. Other recovery causes shall remain effective.
- **AC-PLATFORM-DURABLE-AGENT-DELIVERY-008.4:** Reconstructing an association shall not dispatch a prompt, replay tools, release queued instructions, or fabricate completion. Retained output and authoritative terminal evidence shall use normal reconciliation.
- **AC-PLATFORM-DURABLE-AGENT-DELIVERY-008.5:** Missing associations shall produce a persistent recovery notice on desktop and phone. A rejected instruction shall remain visible with its blocked delivery status. Retry shall show progress and the remaining cause after reload.
- **AC-PLATFORM-DURABLE-AGENT-DELIVERY-008.6:** After reconstruction, restoration shall follow AC-PLATFORM-DURABLE-AGENT-DELIVERY-006.20 through 006.24. Verified termination, current ownership, and native identity shall permit restoration of the same conversation without prompt dispatch. New instructions shall use ordinary admission after restoration; duplicate recovery shall not dispatch them.
- **AC-PLATFORM-DURABLE-AGENT-DELIVERY-008.7:** Older or unsupported peers shall retain their records and expose a specific blocked result. Reconstruction shall not create a replacement conversation or downgrade unresolved durable work to legacy delivery.

## Implementation records

- [Delivery record recovery design](../system-design/durable-agent-record-recovery.md).
- [Missing delivery record fix package](../../../plans/durable-agent-record-recovery/plan.md).
