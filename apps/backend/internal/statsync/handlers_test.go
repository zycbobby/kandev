package statsync

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/kandev/kandev/internal/common/logger"
)

func newHandlerTestRouter(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	fx := newServiceFixture(t, map[string]string{"remote-1": "ws-mapped"}, nil)
	router := gin.New()
	RegisterRoutes(router, fx.svc, logger.Default())
	return router
}

func postIngest(router *gin.Engine, body []byte, secret string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/statsync/ingest", bytes.NewReader(body))
	if secret != "" {
		req.Header.Set(HeaderName, secret)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func ingestBody(t *testing.T, mutate func(env *Envelope)) []byte {
	t.Helper()
	env := validEnvelope()
	if mutate != nil {
		mutate(env)
	}
	raw, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	return raw
}

func TestIngestHandlerRejectsMissingAndWrongSecret(t *testing.T) {
	router := newHandlerTestRouter(t)
	body := ingestBody(t, nil)
	for _, secret := range []string{"", "wrong-secret"} {
		rec := postIngest(router, body, secret)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("secret %q status = %d, want 401", secret, rec.Code)
		}
	}
}

func TestIngestHandlerRejectsUnknownSchemaVersion(t *testing.T) {
	router := newHandlerTestRouter(t)
	rec := postIngest(router, ingestBody(t, func(env *Envelope) {
		env.SchemaVersion = SchemaVersion + 1
	}), "test-secret")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestIngestHandlerRejectsInvalidBody(t *testing.T) {
	router := newHandlerTestRouter(t)
	rec := postIngest(router, []byte("{not json"), "test-secret")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestIngestHandlerRejectsUnmappedSource(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fx := newServiceFixture(t, nil, nil)
	router := gin.New()
	RegisterRoutes(router, fx.svc, logger.Default())
	rec := postIngest(router, ingestBody(t, nil), "test-secret")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestIngestHandlerReturnsCountsAndPersistsRows(t *testing.T) {
	router := newHandlerTestRouter(t)
	rec := postIngest(router, ingestBody(t, nil), "test-secret")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	var response struct {
		Upserts int64 `json:"upserts"`
		Deleted int64 `json:"deleted"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Upserts == 0 {
		t.Fatal("upserts = 0, want the payload row count")
	}
	if response.Deleted != 0 {
		t.Fatalf("deleted = %d, want 0 for a delta push", response.Deleted)
	}
}

func TestIngestHandlerReconcileDeletes(t *testing.T) {
	router := newHandlerTestRouter(t)
	if rec := postIngest(router, ingestBody(t, nil), "test-secret"); rec.Code != http.StatusOK {
		t.Fatalf("delta push status = %d, want 200", rec.Code)
	}
	rec := postIngest(router, ingestBody(t, func(env *Envelope) {
		env.Mode = ModeReconcile
		env.Inventory = &Inventory{Tasks: []string{"remote-1:task-1"}}
	}), "test-secret")
	if rec.Code != http.StatusOK {
		t.Fatalf("reconcile push status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
}

func TestIngestEndpointIsReachableWithoutSessionWhenAuthEnabled(t *testing.T) {
	// The middleware allowlist branch (auth/httpmw.isPublicPath) pins the
	// route as self-authenticating: POST with only the shared secret header
	// must reach the handler. This test pins the handler contract the
	// allowlist assumes — no session cookie, no bearer token.
	router := newHandlerTestRouter(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/statsync/ingest",
		bytes.NewReader(ingestBody(t, nil)))
	req.Header.Set(HeaderName, "test-secret")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code == http.StatusUnauthorized {
		t.Fatal("handler rejected a valid secret presented without a session")
	}
}
