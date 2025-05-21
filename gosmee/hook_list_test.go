package gosmee

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/google/go-github/v57/github"
	"golang.org/x/exp/slog"
	"gotest.tools/v3/assert"
)

// mockGHOp is a mock implementation of the GHOp interface for testing.
type mockGHOp struct {
	hooks      []*github.Hook
	deliveries []*github.HookDelivery
	err        error
}

func (m *mockGHOp) Starting() {}

func (m *mockGHOp) ListHooks(_ context.Context, _, _ string, _ *github.ListOptions) ([]*github.Hook, *github.Response, error) {
	if m.err != nil {
		return nil, &github.Response{}, m.err
	}
	return m.hooks, &github.Response{}, nil
}

func (m *mockGHOp) ListHookDeliveries(_ context.Context, _, _ string, _ int64, _ *github.ListCursorOptions) ([]*github.HookDelivery, *github.Response, error) {
	if m.err != nil {
		return nil, &github.Response{}, m.err
	}
	return m.deliveries, &github.Response{}, nil
}

func (m *mockGHOp) GetHookDelivery(_ context.Context, _, _ string, _, _ int64) (*github.HookDelivery, *github.Response, error) {
	if m.err != nil {
		return nil, &github.Response{}, m.err
	}
	// Return the first delivery in the list (simplified for testing)
	if len(m.deliveries) > 0 {
		return m.deliveries[0], &github.Response{}, nil
	}
	return nil, &github.Response{}, nil
}

func TestListHooks(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	t.Run("Success", func(t *testing.T) {
		// Create test hooks
		hooks := []*github.Hook{
			{
				ID:   github.Int64(123),
				Name: github.String("web"),
				Config: map[string]any{
					"url": "https://example.com/webhook",
				},
			},
			{
				ID:   github.Int64(456),
				Name: github.String("custom"),
				Config: map[string]any{
					"url": "https://test.com/webhook",
				},
			},
		}

		// Create mock GHOp implementation
		mockGh := &mockGHOp{hooks: hooks}

		// Create test replayOpts
		opts := &replayOpts{
			logger: logger,
			org:    "testorg",
			repo:   "testrepo",
			ghop:   mockGh,
		}

		// Call listHooks
		err := opts.listHooks(context.Background())

		// Verify no error
		assert.NilError(t, err)
	})

	t.Run("Error", func(t *testing.T) {
		// Create mock GHOp with error
		mockGh := &mockGHOp{err: errors.New("test error")}

		// Create test replayOpts
		opts := &replayOpts{
			logger: logger,
			org:    "testorg",
			repo:   "testrepo",
			ghop:   mockGh,
		}

		// Call listHooks
		err := opts.listHooks(context.Background())

		// Verify error
		assert.Assert(t, err != nil)
	})
}

func TestListDeliveries(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	t.Run("Success", func(t *testing.T) {
		// Create test deliveries
		deliveryTime := time.Now()
		deliveries := []*github.HookDelivery{
			{
				ID:          github.Int64(789),
				GUID:        github.String("guid-1"),
				DeliveredAt: &github.Timestamp{Time: deliveryTime},
				Event:       github.String("push"),
			},
			{
				ID:          github.Int64(101112),
				GUID:        github.String("guid-2"),
				DeliveredAt: &github.Timestamp{Time: deliveryTime.Add(-1 * time.Hour)},
				Event:       github.String("pull_request"),
			},
		}

		// Create mock GHOp implementation
		mockGh := &mockGHOp{deliveries: deliveries}

		// Create test replayOpts
		opts := &replayOpts{
			logger: logger,
			org:    "testorg",
			repo:   "testrepo",
			ghop:   mockGh,
		}

		// Call listDeliveries
		err := opts.listDeliveries(context.Background(), 123)

		// Verify no error
		assert.NilError(t, err)
	})

	t.Run("Error", func(t *testing.T) {
		// Create mock GHOp with error
		mockGh := &mockGHOp{err: errors.New("test error")}

		// Create test replayOpts
		opts := &replayOpts{
			logger: logger,
			org:    "testorg",
			repo:   "testrepo",
			ghop:   mockGh,
		}

		// Call listDeliveries
		err := opts.listDeliveries(context.Background(), 123)

		// Verify error
		assert.Assert(t, err != nil)
	})
}
