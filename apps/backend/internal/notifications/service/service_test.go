package service

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/kandev/kandev/internal/common/logger"
	gatewayws "github.com/kandev/kandev/internal/gateway/websocket"
	"github.com/kandev/kandev/internal/notifications/models"
	"github.com/kandev/kandev/internal/notifications/providers"
	notificationstore "github.com/kandev/kandev/internal/notifications/store"
	taskmodels "github.com/kandev/kandev/internal/task/models"
	"go.uber.org/zap"
)

type notificationTestRepository struct {
	mu            sync.Mutex
	providers     []*models.Provider
	subscriptions map[string][]*models.Subscription
	deliveries    []*models.Delivery
	occurrences   []*models.Occurrence
	sweepCutoffs  []time.Time
}

func (r *notificationTestRepository) CreateProvider(_ context.Context, provider *models.Provider) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.providers = append(r.providers, provider)
	return nil
}
func (r *notificationTestRepository) UpdateProvider(context.Context, *models.Provider) error {
	return nil
}
func (r *notificationTestRepository) GetProvider(_ context.Context, userID, id string) (*models.Provider, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, provider := range r.providers {
		if provider.ID == id && provider.UserID == userID {
			return provider, nil
		}
	}
	return nil, notificationstore.ErrProviderNotFound
}
func (r *notificationTestRepository) ListProvidersByUser(context.Context, string) ([]*models.Provider, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]*models.Provider(nil), r.providers...), nil
}
func (r *notificationTestRepository) ListProviderUserIDs(context.Context) ([]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	seen := map[string]bool{}
	owners := make([]string, 0, len(r.providers))
	for _, provider := range r.providers {
		if !seen[provider.UserID] {
			seen[provider.UserID] = true
			owners = append(owners, provider.UserID)
		}
	}
	return owners, nil
}
func (r *notificationTestRepository) DeleteProvider(context.Context, string, string) error {
	return nil
}
func (r *notificationTestRepository) ListSubscriptionsByProvider(_ context.Context, providerID string) ([]*models.Subscription, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]*models.Subscription(nil), r.subscriptions[providerID]...), nil
}
func (r *notificationTestRepository) ReplaceSubscriptions(_ context.Context, providerID, userID string, events []string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.subscriptions[providerID] = make([]*models.Subscription, 0, len(events))
	for _, eventType := range events {
		r.subscriptions[providerID] = append(r.subscriptions[providerID], &models.Subscription{ProviderID: providerID, UserID: userID, EventType: eventType, Enabled: true})
	}
	return nil
}
func (r *notificationTestRepository) InsertDelivery(_ context.Context, delivery *models.Delivery) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, existing := range r.deliveries {
		if existing.ProviderID == delivery.ProviderID && existing.EventType == delivery.EventType && existing.OccurrenceID == delivery.OccurrenceID {
			return false, nil
		}
	}
	r.deliveries = append(r.deliveries, delivery)
	return true, nil
}
func (r *notificationTestRepository) DeleteDelivery(_ context.Context, providerID, eventType, occurrenceID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for index, delivery := range r.deliveries {
		if delivery.ProviderID == providerID && delivery.EventType == eventType && delivery.OccurrenceID == occurrenceID {
			r.deliveries = append(r.deliveries[:index], r.deliveries[index+1:]...)
			return nil
		}
	}
	return nil
}
func (r *notificationTestRepository) Close() error { return nil }

type captureProvider struct{ messages []providers.Message }

func (*captureProvider) Available() bool                       { return true }
func (*captureProvider) Validate(map[string]interface{}) error { return nil }
func (p *captureProvider) Send(_ context.Context, message providers.Message) error {
	p.messages = append(p.messages, message)
	return nil
}

type failOnceProvider struct {
	attempts int
	messages []providers.Message
}

func (*failOnceProvider) Available() bool                       { return true }
func (*failOnceProvider) Validate(map[string]interface{}) error { return nil }
func (p *failOnceProvider) Send(_ context.Context, message providers.Message) error {
	p.attempts++
	if p.attempts == 1 {
		return context.DeadlineExceeded
	}
	p.messages = append(p.messages, message)
	return nil
}

type notificationTestTaskGetter struct {
	task      *taskmodels.Task
	workspace *taskmodels.Workspace
}

func (g notificationTestTaskGetter) GetTask(context.Context, string) (*taskmodels.Task, error) {
	return g.task, nil
}

func (g notificationTestTaskGetter) GetWorkspace(context.Context, string) (*taskmodels.Workspace, error) {
	return g.workspace, nil
}

func TestNewServiceSuppressesSystemProviderForDesktopOwnedLaunch(t *testing.T) {
	t.Setenv("KANDEV_DESKTOP_NATIVE_NOTIFICATIONS", "true")
	log, err := logger.NewFromZap(zap.NewNop())
	if err != nil {
		t.Fatalf("create logger: %v", err)
	}

	svc := NewService(nil, nil, nil, log, nil)

	if _, exists := svc.providers[models.ProviderTypeSystem]; exists {
		t.Fatal("system notification provider must be suppressed for a desktop-owned launch")
	}
	if _, exists := svc.providers[models.ProviderTypeLocal]; !exists {
		t.Fatal("local websocket notification provider must remain enabled")
	}
}

func TestNewServiceRetainsSystemProviderForNonDesktopLaunch(t *testing.T) {
	t.Setenv("KANDEV_DESKTOP_NATIVE_NOTIFICATIONS", "")
	log, err := logger.NewFromZap(zap.NewNop())
	if err != nil {
		t.Fatalf("create logger: %v", err)
	}

	svc := NewService(nil, nil, nil, log, nil)

	if _, exists := svc.providers[models.ProviderTypeSystem]; !exists {
		t.Fatal("system notification provider must remain enabled outside the desktop-owned launch")
	}
}

func TestSemanticOccurrencesUseEventSpecificCopyAndOccurrenceIdempotency(t *testing.T) {
	// Keep the auto-provisioned system provider out so the test only exercises
	// the single configured local provider; SystemProvider.Available() is true
	// on desktop platforms (e.g. macOS) and would add an extra delivery.
	t.Setenv(desktopNativeNotificationsEnv, "true")
	log, err := logger.NewFromZap(zap.NewNop())
	if err != nil {
		t.Fatalf("create logger: %v", err)
	}
	repo := &notificationTestRepository{
		providers: []*models.Provider{{ID: "provider-1", Type: models.ProviderTypeLocal, Enabled: true}},
		subscriptions: map[string][]*models.Subscription{
			"provider-1": {
				{ProviderID: "provider-1", EventType: EventTaskSessionTurnFinished, Enabled: true},
				{ProviderID: "provider-1", EventType: EventTaskSessionClarificationAsked, Enabled: true},
			},
		},
	}
	service := NewService(repo, notificationTestTaskGetter{task: &taskmodels.Task{Title: "Fix delivery"}}, nil, log, nil)
	capture := &captureProvider{}
	service.providers[models.ProviderTypeLocal] = capture

	service.HandleTaskTurnFinished(context.Background(), "task-1", "session-1", "turn-1")
	service.HandleTaskTurnFinished(context.Background(), "task-1", "session-1", "turn-1")
	service.HandleTaskTurnFinished(context.Background(), "task-1", "session-1", "turn-2")
	service.HandleClarificationRequested(context.Background(), "task-1", "session-1", "pending-1")

	if len(capture.messages) != 3 {
		t.Fatalf("sent %d notifications, want 3 semantic occurrences", len(capture.messages))
	}
	if got := capture.messages[0]; got.EventType != EventTaskSessionTurnFinished || got.Title != "Agent turn finished" || got.Body != "The agent finished a turn on \"Fix delivery\"." {
		t.Fatalf("turn notification = %#v", got)
	}
	if got := capture.messages[2]; got.EventType != EventTaskSessionClarificationAsked || got.Title != "Agent needs your answer" || got.Body != "The agent asked a question on \"Fix delivery\"." {
		t.Fatalf("clarification notification = %#v", got)
	}
	if len(repo.deliveries) != 3 || repo.deliveries[0].OccurrenceID != "turn-1" || repo.deliveries[2].OccurrenceID != "pending-1" {
		t.Fatalf("delivery occurrences = %#v", repo.deliveries)
	}
}

func TestUpdateAvailabilityIsAvailableEvent(t *testing.T) {
	log, err := logger.NewFromZap(zap.NewNop())
	if err != nil {
		t.Fatalf("create logger: %v", err)
	}
	service := NewService(&notificationTestRepository{}, nil, nil, log, nil)
	if !containsEvent(service.AvailableEvents(), "system.update_available") {
		t.Fatalf("available events = %#v, want system.update_available", service.AvailableEvents())
	}
}

func TestFreshLocalProviderSubscribesToUpdateAvailability(t *testing.T) {
	log, err := logger.NewFromZap(zap.NewNop())
	if err != nil {
		t.Fatalf("create logger: %v", err)
	}
	repo := &notificationTestRepository{subscriptions: make(map[string][]*models.Subscription)}
	service := NewService(repo, nil, nil, log, nil)
	if _, _, err := service.ListProviders(context.Background(), "user-1"); err != nil {
		t.Fatalf("list providers: %v", err)
	}
	for _, provider := range repo.providers {
		if provider.Type == models.ProviderTypeLocal && !containsEvent(subscriptionEventTypes(repo.subscriptions[provider.ID]), EventSystemUpdateAvailable) {
			t.Fatalf("local defaults = %#v, want update availability", repo.subscriptions[provider.ID])
		}
	}
}

func TestConcurrentProviderInitializationCreatesSingletonDefaultsAndClaimsOneUpdatePerProvider(t *testing.T) {
	log, err := logger.NewFromZap(zap.NewNop())
	if err != nil {
		t.Fatalf("create logger: %v", err)
	}
	repo := &notificationTestRepository{subscriptions: make(map[string][]*models.Subscription)}
	service := NewService(repo, nil, nil, log, nil)
	local, system := &captureProvider{}, &captureProvider{}
	service.providers[models.ProviderTypeLocal] = local
	service.providers[models.ProviderTypeSystem] = system

	var calls sync.WaitGroup
	for range 32 {
		calls.Add(1)
		go func() {
			defer calls.Done()
			if _, _, err := service.ListProviders(context.Background(), "user-1"); err != nil {
				t.Errorf("list providers: %v", err)
			}
		}()
	}
	calls.Wait()

	localCount, systemCount := 0, 0
	for _, provider := range repo.providers {
		switch provider.Type {
		case models.ProviderTypeLocal:
			localCount++
		case models.ProviderTypeSystem:
			systemCount++
		}
	}
	if localCount != 1 || systemCount != 1 {
		t.Fatalf("default providers: local=%d system=%d, want one of each", localCount, systemCount)
	}

	service.HandleUpdateAvailable(context.Background(), "v1.2.3", "https://example.test/releases/v1.2.3")
	service.HandleUpdateAvailable(context.Background(), "v1.2.3", "https://example.test/releases/v1.2.3")
	if len(local.messages) != 1 || len(system.messages) != 1 || len(repo.deliveries) != 2 {
		t.Fatalf("update delivery: local=%d system=%d claims=%d, want one per default provider", len(local.messages), len(system.messages), len(repo.deliveries))
	}
}

func subscriptionEventTypes(subscriptions []*models.Subscription) []string {
	events := make([]string, 0, len(subscriptions))
	for _, subscription := range subscriptions {
		events = append(events, subscription.EventType)
	}
	return events
}

func TestUpdateAvailabilityRoutesProviderScopedOccurrenceWithReleasePayload(t *testing.T) {
	log, err := logger.NewFromZap(zap.NewNop())
	if err != nil {
		t.Fatalf("create logger: %v", err)
	}
	repo := &notificationTestRepository{
		providers: []*models.Provider{
			{ID: "local", Type: models.ProviderTypeLocal, Enabled: true},
			{ID: "apprise", Type: models.ProviderTypeApprise, Enabled: true},
			{ID: "system", Type: models.ProviderTypeSystem, Enabled: true},
		},
		subscriptions: map[string][]*models.Subscription{
			"local":   {{ProviderID: "local", EventType: EventSystemUpdateAvailable, Enabled: true}},
			"apprise": {{ProviderID: "apprise", EventType: EventSystemUpdateAvailable, Enabled: true}},
			"system":  {{ProviderID: "system", EventType: EventSystemUpdateAvailable, Enabled: true}},
		},
	}
	service := NewService(repo, nil, nil, log, nil)
	local, apprise, system := &captureProvider{}, &captureProvider{}, &captureProvider{}
	service.providers[models.ProviderTypeLocal] = local
	service.providers[models.ProviderTypeApprise] = apprise
	service.providers[models.ProviderTypeSystem] = system

	service.HandleUpdateAvailable(context.Background(), "v1.2.3", "https://example.test/releases/v1.2.3")
	service.HandleUpdateAvailable(context.Background(), "v1.2.3", "https://example.test/releases/v1.2.3")

	for providerType, captured := range map[string]*captureProvider{"local": local, "apprise": apprise, "system": system} {
		if len(captured.messages) != 1 {
			t.Fatalf("%s messages = %#v, want exactly one", providerType, captured.messages)
		}
		message := captured.messages[0]
		if message.EventType != EventSystemUpdateAvailable || message.OccurrenceID != "v1.2.3" || message.TaskSessionID != "" || message.Title != "Kandev update available" || message.Body != "Kandev v1.2.3 is available. Open Settings > System > Updates to review it." {
			t.Fatalf("%s update message = %#v", providerType, message)
		}
		if message.Payload["version"] != "v1.2.3" || message.Payload["url"] != "https://example.test/releases/v1.2.3" {
			t.Fatalf("%s payload = %#v", providerType, message.Payload)
		}
	}
}

func TestNoEligibleLocalUpdateSubscriberReleasesOccurrenceClaimForReplay(t *testing.T) {
	// Keep the auto-provisioned system provider out so the test only exercises
	// the single configured local provider; SystemProvider.Available() is true
	// on desktop platforms (e.g. macOS) and would add an extra delivery.
	t.Setenv(desktopNativeNotificationsEnv, "true")
	log, err := logger.NewFromZap(zap.NewNop())
	if err != nil {
		t.Fatalf("create logger: %v", err)
	}
	repo := &notificationTestRepository{
		providers: []*models.Provider{{ID: "local", Type: models.ProviderTypeLocal, Enabled: true}},
		subscriptions: map[string][]*models.Subscription{
			"local": {{ProviderID: "local", EventType: EventSystemUpdateAvailable, Enabled: true}},
		},
	}
	service := NewService(repo, nil, gatewayws.NewHub(nil, log), log, nil)

	service.HandleUpdateAvailable(context.Background(), "v1.2.3", "https://example.test/releases/v1.2.3")
	if len(repo.deliveries) != 0 {
		t.Fatalf("failed local delivery claims = %#v, want none", repo.deliveries)
	}

	capture := &captureProvider{}
	service.providers[models.ProviderTypeLocal] = capture
	service.HandleUpdateAvailable(context.Background(), "v1.2.3", "https://example.test/releases/v1.2.3")
	if len(capture.messages) != 1 || len(repo.deliveries) != 1 {
		t.Fatalf("replayed local delivery: messages=%#v claims=%#v, want one", capture.messages, repo.deliveries)
	}
}

func TestFailedSemanticDeliveryReleasesOnlyItsOccurrenceClaim(t *testing.T) {
	log, err := logger.NewFromZap(zap.NewNop())
	if err != nil {
		t.Fatalf("create logger: %v", err)
	}
	repo := &notificationTestRepository{
		providers: []*models.Provider{{ID: "provider-1", Type: models.ProviderTypeLocal, Enabled: true}},
		subscriptions: map[string][]*models.Subscription{
			"provider-1": {{ProviderID: "provider-1", EventType: EventTaskSessionTurnFinished, Enabled: true}},
		},
	}
	service := NewService(repo, nil, nil, log, nil)
	provider := &failOnceProvider{}
	service.providers[models.ProviderTypeLocal] = provider

	service.HandleTaskTurnFinished(context.Background(), "task-1", "session-1", "turn-1")
	service.HandleTaskTurnFinished(context.Background(), "task-1", "session-1", "turn-2")
	service.HandleTaskTurnFinished(context.Background(), "task-1", "session-1", "turn-1")

	if provider.attempts != 3 {
		t.Fatalf("send attempts = %d, want failed occurrence replayed independently", provider.attempts)
	}
	if len(provider.messages) != 2 {
		t.Fatalf("successful messages = %#v", provider.messages)
	}
}

func TestProviderSendsClarificationAction(t *testing.T) {
	log, err := logger.NewFromZap(zap.NewNop())
	if err != nil {
		t.Fatalf("create logger: %v", err)
	}
	provider := &models.Provider{ID: "provider-1", UserID: "user-1", Type: models.ProviderTypeLocal}
	service := NewService(&notificationTestRepository{providers: []*models.Provider{provider}}, nil, nil, log, nil)
	capture := &captureProvider{}
	service.providers[models.ProviderTypeLocal] = capture

	if err := service.TestProvider(context.Background(), "user-1", provider.ID); err != nil {
		t.Fatalf("test provider: %v", err)
	}
	if len(capture.messages) != 1 || capture.messages[0].EventType != EventTaskSessionClarificationAsked {
		t.Fatalf("test message = %#v, want clarification action", capture.messages)
	}
}

func (r *notificationTestRepository) CreateProviderWithSubscriptions(ctx context.Context, provider *models.Provider, events []string) error {
	if err := r.CreateProvider(ctx, provider); err != nil {
		return err
	}
	return r.ReplaceSubscriptions(ctx, provider.ID, provider.UserID, events)
}
func (r *notificationTestRepository) UpdateProviderWithSubscriptions(ctx context.Context, provider *models.Provider, events *[]string) error {
	if err := r.UpdateProvider(ctx, provider); err != nil {
		return err
	}
	if events != nil {
		return r.ReplaceSubscriptions(ctx, provider.ID, provider.UserID, *events)
	}
	return nil
}

// TestHandleManualStepWaitSuppressesSelfMoveAndDeliversOthers pins the actor
// exclusion contract: a move the recipient performed themselves must not
// notify them, while a move by another human or by an agent must. A human
// actor with no recorded id is the pre-auth default user, so it excludes the
// default user exactly like an explicit match would.
func TestHandleManualStepWaitSuppressesSelfMoveAndDeliversOthers(t *testing.T) {
	// Keep the auto-provisioned system provider out so the single configured
	// local provider is the only delivery surface under test.
	t.Setenv(desktopNativeNotificationsEnv, "true")
	log, err := logger.NewFromZap(zap.NewNop())
	if err != nil {
		t.Fatalf("create logger: %v", err)
	}
	repo := &notificationTestRepository{
		providers: []*models.Provider{{ID: "provider-1", Type: models.ProviderTypeLocal, Enabled: true}},
		subscriptions: map[string][]*models.Subscription{
			"provider-1": {
				{ProviderID: "provider-1", EventType: EventTaskManualStepEntered, Enabled: true},
			},
		},
	}
	service := NewService(repo, notificationTestTaskGetter{task: &taskmodels.Task{Title: "Weekly routine"}}, nil, log, nil)
	capture := &captureProvider{}
	service.providers[models.ProviderTypeLocal] = capture

	// Self-move: the unauthenticated single user moved the task themselves.
	service.HandleManualStepWait(context.Background(), "task-1", "session-1", "mw:task-1:step-review:7", "human", "")
	if len(capture.messages) != 0 {
		t.Fatalf("self-move delivered %#v, want no delivery", capture.messages)
	}
	if len(repo.deliveries) != 0 || len(repo.occurrences) != 0 {
		t.Fatalf("self-move recorded claims=%#v occurrences=%#v, want none", repo.deliveries, repo.occurrences)
	}

	// Another human's move notifies the workspace owner.
	service.HandleManualStepWait(context.Background(), "task-1", "session-1", "mw:task-1:step-review:8", "human", "user-2")
	if len(capture.messages) != 1 {
		t.Fatalf("other-human move delivered %#v, want exactly one", capture.messages)
	}
	message := capture.messages[0]
	if message.EventType != EventTaskManualStepEntered || message.OccurrenceID != "mw:task-1:step-review:8" ||
		message.Title != "Task waiting on you" || message.Body != "The task \"Weekly routine\" moved to a step that waits for you." {
		t.Fatalf("manual-step message = %#v", message)
	}

	// An agent's move notifies too, and repeating the same episode occurrence
	// collapses into one delivery via the existing occurrence dedup.
	service.HandleManualStepWait(context.Background(), "task-1", "session-1", "mw:task-1:step-review:9", "agent", "session-move")
	service.HandleManualStepWait(context.Background(), "task-1", "session-1", "mw:task-1:step-review:9", "agent", "session-move")
	if len(capture.messages) != 2 {
		t.Fatalf("agent move delivered %#v, want one new delivery (2 total)", capture.messages)
	}
}

func TestManualStepEnteredIsAnAvailableEvent(t *testing.T) {
	log, err := logger.NewFromZap(zap.NewNop())
	if err != nil {
		t.Fatalf("create logger: %v", err)
	}
	service := NewService(&notificationTestRepository{}, nil, nil, log, nil)
	if !containsEvent(service.AvailableEvents(), EventTaskManualStepEntered) {
		t.Fatalf("available events = %#v, want %s", service.AvailableEvents(), EventTaskManualStepEntered)
	}
	if err := service.validateEvents([]string{EventTaskManualStepEntered}); err != nil {
		t.Fatalf("validateEvents(%s) = %v, want accepted", EventTaskManualStepEntered, err)
	}
}

// TestFreshDefaultProvidersSubscribeToManualStepEntered pins the creation
// list: a brand-new install's default local and system providers subscribe to
// the manual-step wait event from the start, so only pre-existing installs
// need the one-shot store migration.
func TestFreshDefaultProvidersSubscribeToManualStepEntered(t *testing.T) {
	log, err := logger.NewFromZap(zap.NewNop())
	if err != nil {
		t.Fatalf("create logger: %v", err)
	}
	repo := &notificationTestRepository{subscriptions: make(map[string][]*models.Subscription)}
	service := NewService(repo, nil, nil, log, nil)
	if _, _, err := service.ListProviders(context.Background(), "user-1"); err != nil {
		t.Fatalf("list providers: %v", err)
	}
	defaults := 0
	for _, provider := range repo.providers {
		if provider.Type != models.ProviderTypeLocal && provider.Type != models.ProviderTypeSystem {
			continue
		}
		defaults++
		if !containsEvent(subscriptionEventTypes(repo.subscriptions[provider.ID]), EventTaskManualStepEntered) {
			t.Fatalf("%s default subscriptions = %#v, want %s", provider.Type, repo.subscriptions[provider.ID], EventTaskManualStepEntered)
		}
	}
	if defaults == 0 {
		t.Fatalf("no default providers were provisioned: %#v", repo.providers)
	}
}
