package gosmee

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
	"gotest.tools/v3/assert"
)

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestNewEventBrokerUsesInjectedLogger(t *testing.T) {
	buf := &lockedBuffer{}
	logger := slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	broker := NewEventBroker(logger)
	assert.Equal(t, broker.logger, logger)
}

func TestHandleEventsGetLogsDisconnect(t *testing.T) {
	buf := &lockedBuffer{}
	logger := slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	broker := NewEventBroker(logger)

	router := chi.NewRouter()
	router.Get(eventsPath, handleEventsGet(broker, nil, "*"))

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/events/plain-channel", nil)
	reqCtx, cancel := context.WithCancel(req.Context())
	req = req.WithContext(reqCtx)

	response := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		router.ServeHTTP(response, req)
		close(done)
	}()

	assert.Assert(t, eventually(t, func() bool {
		broker.RLock()
		defer broker.RUnlock()
		return len(broker.subscribers["plain-channel"]) == 1
	}))

	cancel()
	<-done

	logs := buf.String()
	assert.Assert(t, strings.Contains(logs, "SSE subscriber disconnected"), logs)
	assert.Assert(t, strings.Contains(logs, `"channel":"plain-channel"`), logs)
	assert.Assert(t, strings.Contains(logs, `"request_id"`), logs)
}
