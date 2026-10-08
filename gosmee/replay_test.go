package gosmee

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/go-github/v57/github"
	"gotest.tools/v3/assert"
)

func TestChooseDeliveries(t *testing.T) {
	type args struct {
		sinceTime  time.Time
		deliveries []*github.HookDelivery
	}
	tests := []struct {
		name          string
		args          args
		wantErr       bool
		deliveryCount int
		deliveryIDs   []int64
	}{
		{
			name:          "choose deliveries",
			deliveryCount: 2,
			deliveryIDs:   []int64{2, 3},
			args: args{
				sinceTime: time.Now(),
				deliveries: []*github.HookDelivery{
					{
						ID:          github.Int64(3),
						DeliveredAt: &github.Timestamp{Time: time.Now().Add(1 * time.Hour)},
					},
					{
						ID:          github.Int64(2),
						DeliveredAt: &github.Timestamp{Time: time.Now().Add(2 * time.Hour)},
					},
					{
						ID:          github.Int64(1),
						DeliveredAt: &github.Timestamp{Time: time.Now().Add(-1 * time.Hour)},
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &replayOpts{sinceTime: tt.args.sinceTime}
			ret := r.chooseDeliveries(tt.args.deliveries)
			if len(ret) != tt.deliveryCount {
				t.Errorf("chooseDeliveries() = %v, want %v", len(ret), tt.deliveryCount)
			}
			for i, d := range ret {
				if *d.ID != tt.deliveryIDs[i] {
					t.Errorf("chooseDeliveries() = %v, want %v", *d.ID, tt.deliveryIDs[i])
				}
			}
		})
	}
}

// mockGHOpForReplay is a specialized implementation for testing replayHooks.
type mockGHOpForReplay struct {
	deliveries []*github.HookDelivery
	err        error
	mtx        sync.Mutex // protect concurrent access during tests
}

func (m *mockGHOpForReplay) Starting() {}

func (m *mockGHOpForReplay) ListHooks(_ context.Context, _, _ string, _ *github.ListOptions) ([]*github.Hook, *github.Response, error) {
	if m.err != nil {
		return nil, &github.Response{Response: &http.Response{StatusCode: http.StatusInternalServerError}}, m.err
	}
	return []*github.Hook{}, &github.Response{Response: &http.Response{StatusCode: http.StatusOK}}, nil
}

func (m *mockGHOpForReplay) ListHookDeliveries(_ context.Context, _, _ string, _ int64, _ *github.ListCursorOptions) ([]*github.HookDelivery, *github.Response, error) {
	if m.err != nil {
		return nil, &github.Response{Response: &http.Response{StatusCode: http.StatusInternalServerError}}, m.err
	}

	m.mtx.Lock()
	defer m.mtx.Unlock()

	return m.deliveries, &github.Response{Response: &http.Response{StatusCode: http.StatusOK}}, nil
}

func (m *mockGHOpForReplay) GetHookDelivery(_ context.Context, _, _ string, _, deliveryID int64) (*github.HookDelivery, *github.Response, error) {
	if m.err != nil {
		return nil, &github.Response{Response: &http.Response{StatusCode: http.StatusInternalServerError}}, m.err
	}

	m.mtx.Lock()
	defer m.mtx.Unlock()

	// Find the matching delivery
	for _, delivery := range m.deliveries {
		if delivery.GetID() == deliveryID {
			return delivery, &github.Response{Response: &http.Response{StatusCode: http.StatusOK}}, nil
		}
	}

	// If not found, return 404
	return nil, &github.Response{Response: &http.Response{StatusCode: http.StatusNotFound}}, errors.New("delivery not found")
}

// mockGHOpForReplayWithNotFound is a variant that always returns not found for GetHookDelivery.
type mockGHOpForReplayWithNotFound struct {
	mockGHOpForReplay
}

func (m *mockGHOpForReplayWithNotFound) GetHookDelivery(_ context.Context, _, _ string, _, _ int64) (*github.HookDelivery, *github.Response, error) {
	// Always return 404
	return nil, &github.Response{Response: &http.Response{StatusCode: http.StatusNotFound}}, errors.New("delivery not found")
}

// replayHooksForTest is a modified version of replayHooks that doesn't have an infinite loop for testing.
func (r *replayOpts) replayHooksForTest(ctx context.Context, hookid int64) error {
	r.ghop.Starting()
	// Just run one cycle for testing purposes
	opt := &github.ListCursorOptions{PerPage: 100}
	deliveries, _, err := r.ghop.ListHookDeliveries(ctx, r.org, r.repo, hookid, opt)
	if err != nil {
		return err
	}

	// reverse deliveries to replay from oldest to newest
	deliveries = r.chooseDeliveries(deliveries)
	for _, hd := range deliveries {
		var delivery *github.HookDelivery
		// Try only once in tests to avoid timeouts
		delivery, resp, err := r.ghop.GetHookDelivery(ctx, r.org, r.repo, hookid, hd.GetID())
		if err != nil {
			if resp != nil && resp.StatusCode == http.StatusNotFound {
				// In tests, just continue rather than waiting and retrying
				continue
			}
			return err
		}

		pm := payloadMsg{}
		var ok bool
		if pm.contentType, ok = delivery.Request.Headers["Content-Type"]; !ok {
			pm.contentType = "application/json"
		}
		pm.body = delivery.Request.GetRawPayload()
		pm.headers = delivery.Request.GetHeaders()
		pm.eventID = hd.GetGUID()

		// get the event type
		if pv, ok := pm.headers["X-GitHub-Event"]; ok {
			// For test simplicity, skip the complex event processing
			pm.eventType = pv
		}

		dt := delivery.DeliveredAt.GetTime()
		pm.timestamp = dt.Format(tsFormat)

		if err := replayData(r.replayDataOpts, r.logger, pm); err != nil {
			continue
		}

		if r.replayDataOpts.saveDir != "" {
			_ = saveData(r.replayDataOpts, r.logger, pm)
		}
	}

	// No sleep in test implementation
	return ctx.Err() // Return context error if cancelled
}

func TestReplayHooks(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)

	// Create a simple mock delivery response
	deliveryTime := time.Now()
	payloadStr := `{"ref":"refs/heads/main","repository":{"name":"test-repo","owner":{"login":"test-org"}}}`
	rawMessage := json.RawMessage(payloadStr)
	mockDelivery := &github.HookDelivery{
		ID:          github.Int64(123),
		GUID:        github.String("guid-123"),
		DeliveredAt: &github.Timestamp{Time: deliveryTime},
		Event:       github.String("push"),
		Request: &github.HookRequest{
			Headers: map[string]string{
				"Content-Type":      "application/json",
				"X-GitHub-Event":    "push",
				"X-GitHub-Delivery": "guid-123",
			},
			RawPayload: &rawMessage,
		},
	}

	// Set up test server to simulate the webhook target
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	t.Run("Successful Replay", func(t *testing.T) {
		// Create mock GHOp implementation that will return our mock delivery
		mockGh := &mockGHOpForReplay{
			deliveries: []*github.HookDelivery{mockDelivery},
		}

		// Create a channel to signal when we want to terminate the test
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		// Set up the replayOpts
		opts := &replayOpts{
			logger:    logger,
			org:       "test-org",
			repo:      "test-repo",
			ghop:      mockGh,
			sinceTime: time.Now().Add(-1 * time.Hour), // Set time in the past
			replayDataOpts: &replayDataOpts{
				targetURL:        server.URL,
				decorate:         false,
				targetCnxTimeout: 1,
			},
		}

		// Call our test version of replayHooks
		err := opts.replayHooksForTest(ctx, 456)

		// Should succeed with no error
		assert.NilError(t, err)
	})

	t.Run("ListHookDeliveries Error", func(t *testing.T) {
		// Create mock GHOp implementation that will return an error for ListHookDeliveries
		mockGh := &mockGHOpForReplay{
			err: errors.New("list deliveries error"),
		}

		// Create a context
		ctx := context.Background()

		// Set up the replayOpts
		opts := &replayOpts{
			logger: logger,
			org:    "test-org",
			repo:   "test-repo",
			ghop:   mockGh,
			replayDataOpts: &replayDataOpts{
				targetURL: server.URL,
			},
		}

		// Call replayHooksForTest - it should return with an error
		err := opts.replayHooksForTest(ctx, 456)

		// Verify that the error was returned
		assert.ErrorContains(t, err, "list deliveries error")
	})

	t.Run("GetHookDelivery Not Found", func(t *testing.T) {
		// Create a delivery but make it never found by GetHookDelivery
		failDelivery := &github.HookDelivery{
			ID:          github.Int64(999),
			GUID:        github.String("guid-fail"),
			DeliveredAt: &github.Timestamp{Time: deliveryTime},
			Event:       github.String("push"),
		}

		// Create a specialized mock that will return a delivery from list but always 404 from get
		mockGhNotFound := &mockGHOpForReplayWithNotFound{
			mockGHOpForReplay: mockGHOpForReplay{
				deliveries: []*github.HookDelivery{failDelivery},
			},
		}

		// Create a context
		ctx := context.Background()

		// Set up the replayOpts
		opts := &replayOpts{
			logger:    logger,
			org:       "test-org",
			repo:      "test-repo",
			ghop:      mockGhNotFound,
			sinceTime: time.Now().Add(-1 * time.Hour), // Set time in the past
			replayDataOpts: &replayDataOpts{
				targetURL: server.URL,
			},
		}

		// Call our test version which will skip deliveries with 404 status
		err := opts.replayHooksForTest(ctx, 456)

		// Should succeed with no error since we handle 404s
		assert.NilError(t, err)
	})
}

type mockGHOpForDeliveryLookup struct {
	mockGHOpForReplay
	getDelivery func(context.Context, string, string, int64, int64) (*github.HookDelivery, *github.Response, error)
}

func (m *mockGHOpForDeliveryLookup) GetHookDelivery(ctx context.Context, org, repo string, hookID, deliveryID int64) (*github.HookDelivery, *github.Response, error) {
	return m.getDelivery(ctx, org, repo, hookID, deliveryID)
}

func TestGetHookDelivery(t *testing.T) {
	delivery := &github.HookDelivery{ID: github.Int64(123)}
	okResponse := &github.Response{Response: &http.Response{StatusCode: http.StatusOK}}
	notFoundResponse := &github.Response{Response: &http.Response{StatusCode: http.StatusNotFound}}
	serverErrorResponse := &github.Response{Response: &http.Response{StatusCode: http.StatusServiceUnavailable}}
	notFoundErr := errors.New("delivery not found")
	lastNotFoundErr := errors.New("delivery still not found")
	transportErr := errors.New("connection failed")
	serverErr := errors.New("service unavailable")
	type lookupResult struct {
		delivery *github.HookDelivery
		response *github.Response
		err      error
	}
	success := lookupResult{delivery: delivery, response: okResponse}
	notFound := lookupResult{response: notFoundResponse, err: notFoundErr}
	tests := []struct {
		name    string
		results []lookupResult
		wantErr error
	}{
		{
			name:    "success",
			results: []lookupResult{success},
		},
		{
			name:    "success without response",
			results: []lookupResult{{delivery: delivery}},
		},
		{
			name:    "success after one 404",
			results: []lookupResult{notFound, success},
		},
		{
			name:    "success after two 404s",
			results: []lookupResult{notFound, notFound, success},
		},
		{
			name:    "exhausted 404 retries",
			results: []lookupResult{notFound, notFound, {response: notFoundResponse, err: lastNotFoundErr}},
			wantErr: lastNotFoundErr,
		},
		{
			name:    "transport error without response",
			results: []lookupResult{{err: transportErr}},
			wantErr: transportErr,
		},
		{
			name:    "non404 error",
			results: []lookupResult{{response: serverErrorResponse, err: serverErr}},
			wantErr: serverErr,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			calls := 0
			mockGh := &mockGHOpForDeliveryLookup{
				getDelivery: func(gotCtx context.Context, org, repo string, hookID, deliveryID int64) (*github.HookDelivery, *github.Response, error) {
					assert.Equal(t, gotCtx, ctx)
					assert.Equal(t, org, "test-org")
					assert.Equal(t, repo, "test-repo")
					assert.Equal(t, hookID, int64(456))
					assert.Equal(t, deliveryID, int64(123))
					if calls >= len(tt.results) {
						t.Fatalf("unexpected delivery lookup %d", calls+1)
					}
					result := tt.results[calls]
					calls++
					return result.delivery, result.response, result.err
				},
			}
			opts := &replayOpts{org: "test-org", repo: "test-repo", ghop: mockGh}

			got, err := opts.getHookDelivery(ctx, 456, 123)

			assert.Equal(t, calls, len(tt.results))
			if tt.wantErr != nil {
				assert.Assert(t, errors.Is(err, tt.wantErr))
				assert.ErrorContains(t, err, "cannot get delivery")
				assert.Assert(t, got == nil)
				return
			}
			assert.NilError(t, err)
			assert.Equal(t, got, delivery)
		})
	}
}

func TestGetHookDeliveryCanceledWait(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	firstLookup := make(chan struct{})
	calls := 0
	mockGh := &mockGHOpForDeliveryLookup{
		getDelivery: func(context.Context, string, string, int64, int64) (*github.HookDelivery, *github.Response, error) {
			calls++
			if calls == 1 {
				close(firstLookup)
			}
			return nil, &github.Response{Response: &http.Response{StatusCode: http.StatusNotFound}}, errors.New("delivery not found")
		},
	}
	opts := &replayOpts{ghop: mockGh}
	type lookupResult struct {
		delivery *github.HookDelivery
		err      error
	}
	result := make(chan lookupResult, 1)
	go func() {
		delivery, err := opts.getHookDelivery(ctx, 456, 123)
		result <- lookupResult{delivery: delivery, err: err}
	}()
	<-firstLookup
	cancel()

	select {
	case got := <-result:
		assert.Assert(t, errors.Is(got.err, context.Canceled))
		assert.ErrorContains(t, got.err, "cannot get delivery")
		assert.Assert(t, got.delivery == nil)
		assert.Equal(t, calls, 1)
	case <-time.After(5 * time.Second):
		t.Fatal("delivery lookup did not stop after cancellation")
	}
}

func TestReplayHooksLookupFailure(t *testing.T) {
	lookupErr := errors.New("connection failed")
	calls := 0
	mockGh := &mockGHOpForDeliveryLookup{
		mockGHOpForReplay: mockGHOpForReplay{
			deliveries: []*github.HookDelivery{{
				ID:          github.Int64(123),
				DeliveredAt: &github.Timestamp{Time: time.Now()},
			}},
		},
		getDelivery: func(context.Context, string, string, int64, int64) (*github.HookDelivery, *github.Response, error) {
			calls++
			return nil, nil, lookupErr
		},
	}
	opts := &replayOpts{ghop: mockGh}

	err := opts.replayHooks(context.Background(), 456)

	assert.Assert(t, errors.Is(err, lookupErr))
	assert.ErrorContains(t, err, "cannot get delivery")
	assert.Equal(t, calls, 1)
}
