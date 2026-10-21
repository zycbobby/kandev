package lifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/kandev/kandev/internal/common/logger"
	"github.com/kandev/kandev/internal/task/models"
)

func creationBaseForLifecycleTest(ref string) models.CreationBase {
	return models.CreationBase{Version: models.CreationBaseVersion, Ref: ref, ForkSHA: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
}

// fakeCreationBaseSetter records the maps pushed to one agentctl endpoint.
type fakeCreationBaseSetter struct {
	calls []map[string]models.CreationBase
	err   error
}

func (f *fakeCreationBaseSetter) SetCreationBases(_ context.Context, bases map[string]models.CreationBase) error {
	f.calls = append(f.calls, bases)
	return f.err
}

func TestPushTaskCreationBases(t *testing.T) {
	log, _ := logger.NewLogger(logger.LoggingConfig{Level: "error", Format: "json"})
	ctx := context.Background()

	t.Run("pushes the hydrated map", func(t *testing.T) {
		want := map[string]models.CreationBase{"": creationBaseForLifecycleTest("main")}
		m := &Manager{logger: log}
		m.SetCreationBaseProvider(func(context.Context, string) (map[string]models.CreationBase, error) {
			return want, nil
		})

		setter := &fakeCreationBaseSetter{}
		m.pushTaskCreationBases(ctx, "task-1", "exec-1", setter)

		if len(setter.calls) != 1 {
			t.Fatalf("expected 1 SetCreationBases call, got %d", len(setter.calls))
		}
		if !reflect.DeepEqual(setter.calls[0], want) {
			t.Errorf("pushed %v, want %v", setter.calls[0], want)
		}
	})

	// SetCreationBases *replaces* the map. The full-launch path already seeded
	// it from the freshly observed fork points, so pushing an empty hydration
	// on top would wipe a correct map before the executor persists the record.
	t.Run("empty hydration does not clobber launch metadata", func(t *testing.T) {
		m := &Manager{logger: log}
		m.SetCreationBaseProvider(func(context.Context, string) (map[string]models.CreationBase, error) {
			return nil, nil
		})

		setter := &fakeCreationBaseSetter{}
		m.pushTaskCreationBases(ctx, "task-1", "exec-1", setter)

		if len(setter.calls) != 0 {
			t.Fatalf("expected no push for an empty map, got %d calls", len(setter.calls))
		}
	})

	t.Run("provider error is non-fatal", func(t *testing.T) {
		m := &Manager{logger: log}
		m.SetCreationBaseProvider(func(context.Context, string) (map[string]models.CreationBase, error) {
			return nil, errors.New("db unavailable")
		})

		setter := &fakeCreationBaseSetter{}
		m.pushTaskCreationBases(ctx, "task-1", "exec-1", setter)

		if len(setter.calls) != 0 {
			t.Fatalf("expected no push after a provider error, got %d calls", len(setter.calls))
		}
	})

	t.Run("setter error is non-fatal", func(t *testing.T) {
		m := &Manager{logger: log}
		m.SetCreationBaseProvider(func(context.Context, string) (map[string]models.CreationBase, error) {
			return map[string]models.CreationBase{"": creationBaseForLifecycleTest("main")}, nil
		})

		setter := &fakeCreationBaseSetter{err: errors.New("agentctl down")}
		m.pushTaskCreationBases(ctx, "task-1", "exec-1", setter)

		if len(setter.calls) != 1 {
			t.Fatalf("expected the push to be attempted once, got %d calls", len(setter.calls))
		}
	})

	t.Run("no provider or no task is a no-op", func(t *testing.T) {
		setter := &fakeCreationBaseSetter{}
		(&Manager{logger: log}).pushTaskCreationBases(ctx, "task-1", "exec-1", setter)

		m := &Manager{logger: log}
		m.SetCreationBaseProvider(func(context.Context, string) (map[string]models.CreationBase, error) {
			return map[string]models.CreationBase{"": creationBaseForLifecycleTest("main")}, nil
		})
		m.pushTaskCreationBases(ctx, "", "exec-1", setter)

		if len(setter.calls) != 0 {
			t.Fatalf("expected no pushes, got %d calls", len(setter.calls))
		}
	})

	t.Run("nil setter is a no-op", func(t *testing.T) {
		m := &Manager{logger: log}
		m.SetCreationBaseProvider(func(context.Context, string) (map[string]models.CreationBase, error) {
			return map[string]models.CreationBase{"": creationBaseForLifecycleTest("main")}, nil
		})
		m.pushTaskCreationBases(ctx, "task-1", "exec-1", nil)
	})
}

func TestPushCreationBasesForTaskTargetsOnlyThatTask(t *testing.T) {
	var mu sync.Mutex
	var pushes []map[string]models.CreationBase
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/workspace/creation-bases" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var body struct {
			CreationBases map[string]models.CreationBase `json:"creation_bases"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode creation-bases request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		mu.Lock()
		pushes = append(pushes, body.CreationBases)
		mu.Unlock()
		_, _ = w.Write([]byte(`{"success":true}`))
	}))
	t.Cleanup(server.Close)
	client := newTestAgentctlClient(t, server.URL, newTestLogger())

	mgr := newTestManager(t)
	require.NoError(t, mgr.executionStore.Add(&AgentExecution{
		ID: "exec-1", TaskID: "task-1", SessionID: "session-1", agentctl: client,
	}))
	require.NoError(t, mgr.executionStore.Add(&AgentExecution{
		ID: "exec-2", TaskID: "task-1", SessionID: "session-2", agentctl: client,
	}))
	// Same-workspace execution on another task, plus one with no client.
	require.NoError(t, mgr.executionStore.Add(&AgentExecution{
		ID: "exec-3", TaskID: "task-other", SessionID: "session-3", agentctl: client,
	}))
	require.NoError(t, mgr.executionStore.Add(&AgentExecution{
		ID: "exec-4", TaskID: "task-1", SessionID: "session-4",
	}))

	bases := map[string]models.CreationBase{"": creationBaseForLifecycleTest("main")}
	mgr.PushCreationBasesForTask(context.Background(), "task-1", bases)

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, pushes, 2, "only the two client-bearing executions of task-1 are pushed to")
	for _, push := range pushes {
		require.Equal(t, bases, push)
	}
}

func TestPushCreationBasesForTaskIgnoresEmptyTaskID(t *testing.T) {
	mgr := newTestManager(t)
	mgr.PushCreationBasesForTask(context.Background(), "", map[string]models.CreationBase{"": creationBaseForLifecycleTest("main")})
}

// TestStashPreparedCreationBases covers the fresh-materialization seed: the
// fork points observed by the environment preparer land on the launch request
// keyed by the same projection keys as the comparison targets.
func TestStashPreparedCreationBases(t *testing.T) {
	t.Run("single repo uses the root key", func(t *testing.T) {
		req := &LaunchRequest{TaskID: "task-1", RepositoryID: "repo-1", RepoName: "widget", BaseBranch: "main"}
		prep := &EnvPrepareResult{Worktrees: []RepoWorktreeResult{{
			RepositoryID: "repo-1", CreationBaseRef: "main", CreationForkSHA: testSHA("a"),
		}}}

		if err := stashPreparedCreationBases(req, prep); err != nil {
			t.Fatalf("stashPreparedCreationBases: %v", err)
		}
		raw, ok := req.Metadata[MetadataKeyCreationBases]
		if !ok {
			t.Fatalf("metadata missing key %q", MetadataKeyCreationBases)
		}
		got, ok := raw.(map[string]models.CreationBase)
		if !ok {
			t.Fatalf("metadata value type = %T, want map[string]models.CreationBase", raw)
		}
		want := map[string]models.CreationBase{"": {Version: models.CreationBaseVersion, Ref: "main", ForkSHA: testSHA("a")}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("stashed = %v, want %v", got, want)
		}
	})

	t.Run("multi repo keys the first repository at the root and skips reuse results", func(t *testing.T) {
		req := &LaunchRequest{TaskID: "task-1", Repositories: []RepoLaunchSpec{
			{RepoName: "alpha", BaseBranch: "main"},
			{RepoName: "beta", BaseBranch: "develop"},
		}}
		prep := &EnvPrepareResult{Worktrees: []RepoWorktreeResult{
			{RepositoryID: "repo-a", CreationBaseRef: "main", CreationForkSHA: testSHA("a")},
			{RepositoryID: "repo-b"}, // reused worktree: no fork point observed
		}}

		if err := stashPreparedCreationBases(req, prep); err != nil {
			t.Fatalf("stashPreparedCreationBases: %v", err)
		}
		got, ok := req.Metadata[MetadataKeyCreationBases].(map[string]models.CreationBase)
		if !ok {
			t.Fatalf("metadata value type = %T", req.Metadata[MetadataKeyCreationBases])
		}
		if len(got) != 1 {
			t.Fatalf("stashed = %v, want only the freshly cut repository", got)
		}
		// The first repository of a local launch maps to the root tracker key,
		// matching the comparison-target projection convention.
		if _, ok := got[""]; !ok {
			t.Fatalf("stashed = %v, want the root key", got)
		}
	})

	t.Run("no fork points leaves metadata untouched", func(t *testing.T) {
		req := &LaunchRequest{TaskID: "task-1"}
		prep := &EnvPrepareResult{}
		if err := stashPreparedCreationBases(req, prep); err != nil {
			t.Fatalf("stashPreparedCreationBases: %v", err)
		}
		if _, ok := req.Metadata[MetadataKeyCreationBases]; ok {
			t.Fatal("metadata gained a creation-base entry without any fork point")
		}
	})
}

// TestCollectLaunchCreationBases covers the merge semantics: durable
// projection, fresh fork points, fresh wins, and validation.
func TestCollectLaunchCreationBases(t *testing.T) {
	ctx := context.Background()
	fresh := models.CreationBase{Version: models.CreationBaseVersion, Ref: "main", ForkSHA: testSHA("a")}
	durable := models.CreationBase{Version: models.CreationBaseVersion, Ref: "develop", ForkSHA: testSHA("b")}

	t.Run("durable projection seeds a reuse launch", func(t *testing.T) {
		m := &Manager{}
		m.SetCreationBaseProvider(func(context.Context, string) (map[string]models.CreationBase, error) {
			return map[string]models.CreationBase{"": durable}, nil
		})
		got, err := m.collectLaunchCreationBases(ctx, &LaunchRequest{TaskID: "task-1"})
		if err != nil {
			t.Fatalf("collectLaunchCreationBases: %v", err)
		}
		if len(got) != 1 || !got[""].Equal(durable) {
			t.Fatalf("collected = %v, want the durable base", got)
		}
	})

	t.Run("freshly observed fork point wins over the durable entry", func(t *testing.T) {
		m := &Manager{}
		m.SetCreationBaseProvider(func(context.Context, string) (map[string]models.CreationBase, error) {
			return map[string]models.CreationBase{"": durable}, nil
		})
		req := &LaunchRequest{TaskID: "task-1", Metadata: map[string]interface{}{
			MetadataKeyCreationBases: map[string]models.CreationBase{"": fresh},
		}}
		got, err := m.collectLaunchCreationBases(ctx, req)
		if err != nil {
			t.Fatalf("collectLaunchCreationBases: %v", err)
		}
		if len(got) != 1 || !got[""].Equal(fresh) {
			t.Fatalf("collected = %v, want the freshly observed base", got)
		}
	})

	t.Run("JSON-rehydrated metadata decodes", func(t *testing.T) {
		req := &LaunchRequest{TaskID: "task-1", Metadata: map[string]interface{}{
			MetadataKeyCreationBases: map[string]interface{}{
				"": map[string]interface{}{"version": float64(1), "ref": "main", "fork_sha": testSHA("a")},
			},
		}}
		got, err := (&Manager{}).collectLaunchCreationBases(ctx, req)
		if err != nil {
			t.Fatalf("collectLaunchCreationBases: %v", err)
		}
		if len(got) != 1 || !got[""].Equal(fresh) {
			t.Fatalf("collected = %v, want the decoded base", got)
		}
	})

	t.Run("invalid entry fails closed", func(t *testing.T) {
		req := &LaunchRequest{TaskID: "task-1", Metadata: map[string]interface{}{
			MetadataKeyCreationBases: map[string]models.CreationBase{"": {Version: 99}},
		}}
		if _, err := (&Manager{}).collectLaunchCreationBases(ctx, req); err == nil {
			t.Fatal("invalid creation base must fail the launch seed")
		}
	})

	t.Run("empty sources yield nil", func(t *testing.T) {
		got, err := (&Manager{}).collectLaunchCreationBases(ctx, &LaunchRequest{TaskID: "task-1"})
		if err != nil || got != nil {
			t.Fatalf("collected = (%v, %v), want (nil, nil)", got, err)
		}
	})
}

func testSHA(prefix string) string {
	return strings.Repeat(prefix, 40)
}
