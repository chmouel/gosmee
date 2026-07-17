package gosmee

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"gotest.tools/v3/assert"
)

func TestClassifyTargetErrorDNS(t *testing.T) {
	t.Run("NXDOMAIN is permanent", func(t *testing.T) {
		err := &net.DNSError{Err: "no such host", Name: "does-not-exist.invalid", IsNotFound: true}
		kind, retryable := classifyTargetError(err)
		assert.Equal(t, kind, "dns")
		assert.Equal(t, retryable, false)
	})

	t.Run("temporary DNS failure is retryable", func(t *testing.T) {
		err := &net.DNSError{Err: "server misbehaving", Name: "example.com", IsTemporary: true}
		kind, retryable := classifyTargetError(err)
		assert.Equal(t, kind, "dns")
		assert.Equal(t, retryable, true)
	})

	t.Run("wrapped NXDOMAIN is still permanent", func(t *testing.T) {
		err := &net.OpError{Op: "dial", Err: &net.DNSError{Err: "no such host", IsNotFound: true}}
		kind, retryable := classifyTargetError(err)
		assert.Equal(t, kind, "dns")
		assert.Equal(t, retryable, false)
	})

	t.Run("generic network error is retryable", func(t *testing.T) {
		err := &net.OpError{Op: "dial", Err: errors.New("connection refused")}
		kind, retryable := classifyTargetError(err)
		assert.Equal(t, kind, "network")
		assert.Equal(t, retryable, true)
	})

	t.Run("deadline exceeded is a retryable timeout", func(t *testing.T) {
		kind, retryable := classifyTargetError(context.DeadlineExceeded)
		assert.Equal(t, kind, "timeout")
		assert.Equal(t, retryable, true)
	})
}

func TestTargetRetryLimit(t *testing.T) {
	newGS := func(retries int) goSmee {
		return goSmee{replayDataOpts: &replayDataOpts{targetRetries: retries}}
	}
	assert.Equal(t, newGS(0).targetRetryLimit(), 0)  // explicit zero disables retries
	assert.Equal(t, newGS(5).targetRetryLimit(), 5)  // configured value honored
	assert.Equal(t, newGS(-1).targetRetryLimit(), 0) // negatives clamp to zero
}

func TestReplayDataSuccessLogCorrelation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	opts := replayDataOpts{targetURL: server.URL, targetCnxTimeout: 5, decorate: false}
	pm := payloadMsg{
		body:        []byte(`{"k":"v"}`),
		contentType: "application/json",
		headers:     map[string]string{"X-Test": "1"},
		eventType:   "push",
		eventID:     "delivery-123",
		streamID:    "1700000000000-0",
		timestamp:   "2023-10-27T10:00:00.000",
	}

	err := replayData(&opts, logger, pm)
	assert.NilError(t, err)

	var record map[string]any
	assert.NilError(t, json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &record))
	assert.Equal(t, record["stream_id"], "1700000000000-0")
	assert.Equal(t, record["delivery_id"], "delivery-123")
	assert.Equal(t, record["error_kind"], "none")
	assert.Equal(t, record["retryable"], false)
	// The redacted target must not leak credentials/query.
	assert.Equal(t, record["target"], server.URL)
}

func TestReplayDataRedactsTarget(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	opts := replayDataOpts{targetURL: server.URL + "/hook?token=secret", targetCnxTimeout: 5}
	pm := payloadMsg{body: []byte(`{}`), contentType: "application/json", headers: map[string]string{"X-Test": "1"}}

	err := replayData(&opts, logger, pm)
	assert.NilError(t, err)

	var record map[string]any
	assert.NilError(t, json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &record))
	target, _ := record["target"].(string)
	assert.Equal(t, target, server.URL+"/hook")
}

func TestReplayDataReusesConnection(t *testing.T) {
	var newConns atomic.Int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		// Emit a response body so connection reuse depends on draining it.
		_, _ = w.Write(bytes.Repeat([]byte("x"), 512))
	}))
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			newConns.Add(1)
		}
	}
	server.Start()
	defer server.Close()

	logger := slog.New(slog.DiscardHandler)
	opts := replayDataOpts{targetURL: server.URL, targetCnxTimeout: 5}
	pm := payloadMsg{body: []byte(`{}`), contentType: "application/json", headers: map[string]string{"X-Test": "1"}}

	for range 3 {
		assert.NilError(t, replayData(&opts, logger, pm))
	}

	// A single keep-alive connection should serve all three deliveries once
	// the response body is drained before Close.
	assert.Equal(t, newConns.Load(), int32(1))
}
