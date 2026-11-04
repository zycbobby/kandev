package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/kandev/kandev/internal/agentctl/journal"
)

const (
	invalidRecoveryIdentityCode = "INVALID_RECOVERY_IDENTITY"
	deliveryErrorCodeKey        = "code"
)

func (m *ControlServer) handleRetainedDelivery(c *gin.Context) {
	var request journal.RetainedRecoveryRequest
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
	if err := c.ShouldBindJSON(&request); err != nil || request.SessionID == "" || request.ExecutionID == "" || request.SubmissionID == "" || request.StreamID == "" || request.IncarnationID == "" || request.HarnessGeneration == 0 || request.Limit < 1 || request.Limit > 4 {
		c.JSON(http.StatusBadRequest, gin.H{deliveryErrorCodeKey: invalidRecoveryIdentityCode})
		return
	}
	if m.instMgr == nil {
		writeDeliveryError(c, journal.ErrOwnerMismatch)
		return
	}
	for _, instance := range m.instMgr.ListInstances() {
		if instance.ID == request.ExecutionID || instance.SessionID == request.SessionID {
			writeDeliveryError(c, journal.ErrOwnerMismatch)
			return
		}
	}
	result, err := journal.ReadRetainedRecovery(c.Request.Context(), request)
	if err != nil {
		writeDeliveryError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

// handleRetainedRetirement retires abandoned submissions in an owner journal
// that has no live runtime instance. Ownership and process death are proven by
// the journal primitive before any record changes.
func (m *ControlServer) handleRetainedRetirement(c *gin.Context) {
	var request journal.RetainedRetirementRequest
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 32<<10)
	if err := c.ShouldBindJSON(&request); err != nil || !validRetainedRetirementBody(request) {
		c.JSON(http.StatusBadRequest, gin.H{deliveryErrorCodeKey: invalidRecoveryIdentityCode})
		return
	}
	if !m.retainedSessionIsUnowned(c, request.SessionID) {
		return
	}
	result, err := journal.RetireRetainedRecovery(c.Request.Context(), request)
	if err != nil {
		writeDeliveryError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

func validRetainedRetirementBody(request journal.RetainedRetirementRequest) bool {
	if request.SessionID == "" || request.ExecutionID == "" || request.IncarnationID == "" ||
		request.HarnessGeneration == 0 || len(request.Submissions) == 0 {
		return false
	}
	if request.NeverStartedSubmissionID != "" && !retainedRetirementAuthorizationNamesTarget(request) {
		return false
	}
	for _, target := range request.Submissions {
		if !validRetainedRetirementTargetBody(target) {
			return false
		}
	}
	return true
}

// retainedRetirementAuthorizationNamesTarget requires the never-started
// authorization to name one of the submitted target IDs. The authorization is
// target-bound, so an unlisted ID is a malformed body rather than a silently
// ignored one.
func retainedRetirementAuthorizationNamesTarget(request journal.RetainedRetirementRequest) bool {
	for _, target := range request.Submissions {
		if target.ID == request.NeverStartedSubmissionID {
			return true
		}
	}
	return false
}

func validRetainedRetirementTargetBody(target journal.RetainedRetirementTarget) bool {
	return target.ID != "" && target.StreamID != "" && target.Hash != "" &&
		target.HarnessGeneration != 0 && target.State != ""
}

func (m *ControlServer) handleRetainedReconstructionEvidence(c *gin.Context) {
	var request journal.RetainedReconstructionEvidenceRequest
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
	if err := c.ShouldBindJSON(&request); err != nil || request.SessionID == "" || request.IncarnationID == "" || request.HarnessGeneration == 0 {
		c.JSON(http.StatusBadRequest, gin.H{deliveryErrorCodeKey: invalidRecoveryIdentityCode})
		return
	}
	if !m.retainedSessionIsUnowned(c, request.SessionID) {
		return
	}
	result, err := journal.ReadRetainedReconstructionEvidence(c.Request.Context(), request)
	if err != nil {
		writeDeliveryError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

func (m *ControlServer) handleRetainedReconstructionSubmission(c *gin.Context) {
	var request journal.RetainedReconstructionSubmissionRequest
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 32<<10)
	if err := c.ShouldBindJSON(&request); err != nil || request.SessionID == "" || request.IncarnationID == "" || request.HarnessGeneration == 0 || request.Candidate.ID == "" {
		c.JSON(http.StatusBadRequest, gin.H{deliveryErrorCodeKey: invalidRecoveryIdentityCode})
		return
	}
	if !m.retainedSessionIsUnowned(c, request.SessionID) {
		return
	}
	result, err := journal.ReadRetainedReconstructionSubmission(c.Request.Context(), request)
	if err != nil {
		writeDeliveryError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

func (m *ControlServer) retainedSessionIsUnowned(c *gin.Context, sessionID string) bool {
	if m.instMgr == nil {
		writeDeliveryError(c, journal.ErrOwnerMismatch)
		return false
	}
	for _, instance := range m.instMgr.ListInstances() {
		if instance.SessionID == sessionID {
			writeDeliveryError(c, journal.ErrOwnerMismatch)
			return false
		}
	}
	return true
}
