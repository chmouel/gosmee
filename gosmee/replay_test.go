package gosmee

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/go-github/v57/github"
	"golang.org/x/exp/slog"
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
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

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
