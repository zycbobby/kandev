package api

import (
	"net/http"
	"testing"

	"github.com/kandev/kandev/internal/task/models"
)

func validCreationBaseForAPITest() models.CreationBase {
	return models.CreationBase{Version: models.CreationBaseVersion, Ref: "main", ForkSHA: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
}

func TestHandleSetCreationBasesRejectsMalformedAndUnsafePayloads(t *testing.T) {
	fixture := newGitAPIFixture(t)

	if rec := postGitAPI(t, fixture.server, "/api/v1/workspace/creation-bases", "{not json"); rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed status = %d, want 400", rec.Code)
	}
	if rec := postGitAPI(t, fixture.server, "/api/v1/workspace/creation-bases", SetCreationBasesRequest{
		CreationBases: map[string]models.CreationBase{"../outside": {}},
	}); rec.Code != http.StatusBadRequest {
		t.Fatalf("unsafe key status = %d, want 400", rec.Code)
	}
	if rec := postGitAPI(t, fixture.server, "/api/v1/workspace/creation-bases", SetCreationBasesRequest{
		CreationBases: map[string]models.CreationBase{"": {Version: 99}},
	}); rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid base status = %d, want 400", rec.Code)
	}
}

func TestHandleSetCreationBasesAcceptsValidMapAndClearsWithEmptyBody(t *testing.T) {
	fixture := newGitAPIFixture(t)
	root := fixture.server.procMgr.GetWorkspaceTracker()
	if root == nil {
		t.Fatal("expected root tracker for single-repo workspace")
	}

	base := validCreationBaseForAPITest()
	if rec := postGitAPI(t, fixture.server, "/api/v1/workspace/creation-bases", SetCreationBasesRequest{
		CreationBases: map[string]models.CreationBase{"": base},
	}); rec.Code != http.StatusOK {
		t.Fatalf("valid map status = %d, want 200", rec.Code)
	}
	got, present := root.CreationBaseResolution()
	if !present || !got.Equal(base) {
		t.Fatalf("root tracker creation base = %#v present=%v, want the pushed base", got, present)
	}

	if rec := postGitAPI(t, fixture.server, "/api/v1/workspace/creation-bases", SetCreationBasesRequest{}); rec.Code != http.StatusOK {
		t.Fatalf("clearing map status = %d, want 200", rec.Code)
	}
	if _, present := root.CreationBaseResolution(); present {
		t.Fatal("creation base survived an empty-body push")
	}
}
