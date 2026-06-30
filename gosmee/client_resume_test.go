package gosmee

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gotest.tools/v3/assert"
)

func newTestGoSmeeForProcessing(opts *replayDataOpts) goSmee {
	if opts.targetCnxTimeout == 0 {
		opts.targetCnxTimeout = 5
	}
	return goSmee{
		replayDataOpts: opts,
		logger:         slog.New(slog.DiscardHandler),
	}
}

func TestResumeState(t *testing.T) {
	t.Run("missing file starts empty and advance writes atomically", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "resume.state")

		state, err := newResumeState(path)
		assert.NilError(t, err)
		assert.Equal(t, state.ID(), "")

		err = state.Advance("1700000000000-0")
		assert.NilError(t, err)
		assert.Equal(t, state.ID(), "1700000000000-0")

		data, err := os.ReadFile(path)
		assert.NilError(t, err)
		assert.Equal(t, string(data), "1700000000000-0\n")

		loaded, err := newResumeState(path)
		assert.NilError(t, err)
		assert.Equal(t, loaded.ID(), "1700000000000-0")
	})

	t.Run("invalid saved ID fails clearly", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "resume.state")
		err := os.WriteFile(path, []byte("not-a-stream-id\n"), 0o600)
		assert.NilError(t, err)

		_, err = newResumeState(path)
		assert.ErrorContains(t, err, "invalid Redis stream ID")
	})
}

func TestClientDurableProcessing(t *testing.T) {
	baseEvent := clientSSEEvent{
		ID:   "1700000000000-0",
		Data: []byte(simpleJSON),
	}

	t.Run("target HTTP >=300 fails only for durable Redis stream events", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer server.Close()

		gs := newTestGoSmeeForProcessing(&replayDataOpts{
			targetURL: server.URL,
			decorate:  false,
		})

		processed, err := gs.processClientEvent(time.Now().UTC(), baseEvent, nil)
		assert.Assert(t, err != nil)
		assert.Assert(t, !processed)
		assert.ErrorContains(t, err, "target returned 500")

		nonDurableEvent := baseEvent
		nonDurableEvent.ID = ""
		processed, err = gs.processClientEvent(time.Now().UTC(), nonDurableEvent, nil)
		assert.NilError(t, err)
		assert.Assert(t, processed)
	})

	t.Run("checkpoint advances after successful processing", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		path := filepath.Join(t.TempDir(), "resume.state")
		state := &resumeState{path: path}
		gs := newTestGoSmeeForProcessing(&replayDataOpts{
			targetURL: server.URL,
			decorate:  false,
		})

		err := gs.processClientEventWithRetry(context.Background(), baseEvent, nil, state)
		assert.NilError(t, err)
		assert.Equal(t, state.ID(), baseEvent.ID)

		data, err := os.ReadFile(path)
		assert.NilError(t, err)
		assert.Equal(t, string(data), baseEvent.ID+"\n")
	})

	t.Run("canceled context prevents processing", func(t *testing.T) {
		var serverCalled bool
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			serverCalled = true
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		state := &resumeState{path: filepath.Join(t.TempDir(), "resume.state")}
		gs := newTestGoSmeeForProcessing(&replayDataOpts{
			targetURL: server.URL,
			decorate:  false,
		})
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		err := gs.processClientEventWithRetry(ctx, baseEvent, nil, state)
		assert.ErrorIs(t, err, context.Canceled)
		assert.Assert(t, !serverCalled)
		assert.Equal(t, state.ID(), "")
	})

	t.Run("save failure prevents processing success", func(t *testing.T) {
		tmpFile, err := os.CreateTemp(t.TempDir(), "not-a-dir")
		assert.NilError(t, err)
		assert.NilError(t, tmpFile.Close())

		gs := newTestGoSmeeForProcessing(&replayDataOpts{
			noReplay: true,
			saveDir:  tmpFile.Name(),
			decorate: false,
		})

		processed, err := gs.processClientEvent(time.Now().UTC(), baseEvent, nil)
		assert.Assert(t, err != nil)
		assert.Assert(t, !processed)
		assert.ErrorContains(t, err, "saving message")
	})

	t.Run("exec failure prevents processing success", func(t *testing.T) {
		gs := newTestGoSmeeForProcessing(&replayDataOpts{
			noReplay:    true,
			execCommand: "exit 1",
			decorate:    false,
		})

		processed, err := gs.processClientEvent(time.Now().UTC(), baseEvent, nil)
		assert.Assert(t, err != nil)
		assert.Assert(t, !processed)
		assert.ErrorContains(t, err, "exec command failed")
	})

	t.Run("ID-less events do not write resume state", func(t *testing.T) {
		event := baseEvent
		event.ID = ""
		path := filepath.Join(t.TempDir(), "resume.state")
		state := &resumeState{path: path}
		gs := newTestGoSmeeForProcessing(&replayDataOpts{
			noReplay: true,
			decorate: false,
		})

		err := gs.processClientEventWithRetry(context.Background(), event, nil, state)
		assert.NilError(t, err)
		assert.Equal(t, state.ID(), "")
		_, statErr := os.Stat(path)
		assert.Assert(t, os.IsNotExist(statErr))
	})

	t.Run("ignored durable events advance checkpoint", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "resume.state")
		state := &resumeState{path: path}
		gs := newTestGoSmeeForProcessing(&replayDataOpts{
			noReplay:     true,
			ignoreEvents: []string{"push"},
			decorate:     false,
		})

		err := gs.processClientEventWithRetry(context.Background(), baseEvent, nil, state)
		assert.NilError(t, err)
		assert.Equal(t, state.ID(), baseEvent.ID)
	})

	t.Run("legitimate durable payload containing ready or connected is processed", func(t *testing.T) {
		var serverCalled bool
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			serverCalled = true
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		event := clientSSEEvent{
			ID: "1700000000001-0",
			Data: []byte(`{
				"x-github-event": "push",
				"content-type": "application/json",
				"body": {"status":"system is ready","message":"connected"}
			}`),
		}
		gs := newTestGoSmeeForProcessing(&replayDataOpts{
			targetURL: server.URL,
			decorate:  false,
		})

		processed, err := gs.processClientEvent(time.Now().UTC(), event, nil)
		assert.NilError(t, err)
		assert.Assert(t, processed)
		assert.Assert(t, serverCalled)
	})

	t.Run("durable malformed poison event fails fast without checkpoint", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "resume.state")
		state := &resumeState{path: path}
		event := clientSSEEvent{
			ID:   "1700000000002-0",
			Data: []byte(`{"body":{}}`),
		}
		gs := newTestGoSmeeForProcessing(&replayDataOpts{
			noReplay: true,
			decorate: false,
		})

		err := gs.processClientEventWithRetry(context.Background(), event, nil, state)
		assert.Assert(t, err != nil)
		assert.Assert(t, isPermanentClientProcessingError(err))
		assert.ErrorContains(t, err, "no headers found")
		assert.Equal(t, state.ID(), "")
		_, statErr := os.Stat(path)
		assert.Assert(t, os.IsNotExist(statErr))
	})
}
