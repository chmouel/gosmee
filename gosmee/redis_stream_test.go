package gosmee

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/redis/go-redis/v9"
	"gotest.tools/v3/assert"
)

type fakeRedisStreamClient struct {
	mu sync.Mutex

	xaddArgs *redis.XAddArgs
	xaddID   string
	xaddErr  error

	xrangeMessages []redis.XMessage
	xrangeErr      error

	xrevrangeMessages []redis.XMessage
	xrevrangeErr      error

	xreadStreams [][]string
	xreadResults [][]redis.XStream
	xreadErrs    []error
}

func (f *fakeRedisStreamClient) XAdd(_ context.Context, args *redis.XAddArgs) *redis.StringCmd {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.xaddArgs = args
	return redis.NewStringResult(f.xaddID, f.xaddErr)
}

func (f *fakeRedisStreamClient) XRead(ctx context.Context, args *redis.XReadArgs) *redis.XStreamSliceCmd {
	f.mu.Lock()
	f.xreadStreams = append(f.xreadStreams, append([]string(nil), args.Streams...))
	idx := len(f.xreadStreams) - 1
	var result []redis.XStream
	var err error
	if idx < len(f.xreadResults) {
		result = f.xreadResults[idx]
	}
	if idx < len(f.xreadErrs) {
		err = f.xreadErrs[idx]
	}
	f.mu.Unlock()

	if result == nil && err == nil {
		<-ctx.Done()
		err = ctx.Err()
	}
	return redis.NewXStreamSliceCmdResult(result, err)
}

func (f *fakeRedisStreamClient) XRangeN(_ context.Context, _, _, _ string, _ int64) *redis.XMessageSliceCmd {
	f.mu.Lock()
	defer f.mu.Unlock()
	return redis.NewXMessageSliceCmdResult(f.xrangeMessages, f.xrangeErr)
}

func (f *fakeRedisStreamClient) XRevRangeN(_ context.Context, _, _, _ string, _ int64) *redis.XMessageSliceCmd {
	f.mu.Lock()
	defer f.mu.Unlock()
	return redis.NewXMessageSliceCmdResult(f.xrevrangeMessages, f.xrevrangeErr)
}

func (f *fakeRedisStreamClient) Close() error {
	return nil
}

func redisStreamRouter(relay *redisPayloadRelay, protectedChannels *ProtectedChannels) *chi.Mux {
	router := chi.NewRouter()
	router.Get("/events/{channel:[a-zA-Z0-9_-]{12,64}}", handleRedisEventsGet(relay, protectedChannels, "*"))
	return router
}

func waitForRedisReads(t *testing.T, client *fakeRedisStreamClient) {
	t.Helper()
	assert.Assert(t, eventually(t, func() bool {
		client.mu.Lock()
		defer client.mu.Unlock()
		return len(client.xreadStreams) >= 2
	}))
}

func TestRedisPayloadRelayPublish(t *testing.T) {
	t.Run("XADD success returns stream ID and applies approximate maxlen", func(t *testing.T) {
		client := &fakeRedisStreamClient{xaddID: "1700000000000-0"}
		relay := newRedisPayloadRelayWithClient(client, 10000)

		id, err := relay.Publish(context.Background(), "test-channel", []byte(`{"ok":true}`))
		assert.NilError(t, err)
		assert.Equal(t, id, "1700000000000-0")
		assert.Equal(t, client.xaddArgs.Stream, "gosmee:stream:test-channel")
		assert.Equal(t, client.xaddArgs.MaxLen, int64(10000))
		assert.Equal(t, client.xaddArgs.Approx, true)
		assert.DeepEqual(t, client.xaddArgs.Values, map[string]any{
			"payload": `{"ok":true}`,
		})
	})

	t.Run("XADD failure is surfaced", func(t *testing.T) {
		client := &fakeRedisStreamClient{xaddErr: errors.New("redis down")}
		relay := newRedisPayloadRelayWithClient(client, 10000)

		_, err := relay.Publish(context.Background(), "test-channel", []byte(`{"ok":true}`))
		assert.ErrorContains(t, err, "write to redis stream")
		assert.ErrorContains(t, err, "redis down")
	})

	t.Run("maxlen zero disables trimming", func(t *testing.T) {
		client := &fakeRedisStreamClient{xaddID: "1700000000000-0"}
		relay := newRedisPayloadRelayWithClient(client, 0)

		_, err := relay.Publish(context.Background(), "test-channel", []byte(`{"ok":true}`))
		assert.NilError(t, err)
		assert.Equal(t, client.xaddArgs.MaxLen, int64(0))
		assert.Equal(t, client.xaddArgs.Approx, false)
	})
}

func TestRelayEventMetadata(t *testing.T) {
	deliveryID, eventType := relayEventMetadata([]byte(`{"x-gitea-delivery":"delivery-1","x-gitea-event":"pull_request","bodyB":"ignored"}`))
	assert.Equal(t, deliveryID, "delivery-1")
	assert.Equal(t, eventType, "pull_request")

	deliveryID, eventType = relayEventMetadata([]byte(`{"x-github-delivery":"delivery-2","x-github-event":"push"}`))
	assert.Equal(t, deliveryID, "delivery-2")
	assert.Equal(t, eventType, "push")
}

func TestRedisPayloadRelayStreamIDs(t *testing.T) {
	t.Run("oldest ID surfaces Redis errors", func(t *testing.T) {
		relay := newRedisPayloadRelayWithClient(&fakeRedisStreamClient{xrangeErr: errors.New("redis down")}, 10000)

		_, _, err := relay.OldestID(context.Background(), "test-channel")
		assert.ErrorContains(t, err, "read oldest redis stream id")
		assert.ErrorContains(t, err, "redis down")
	})

	t.Run("newest ID surfaces Redis errors", func(t *testing.T) {
		relay := newRedisPayloadRelayWithClient(&fakeRedisStreamClient{xrevrangeErr: errors.New("redis down")}, 10000)

		_, _, err := relay.NewestID(context.Background(), "test-channel")
		assert.ErrorContains(t, err, "read newest redis stream id")
		assert.ErrorContains(t, err, "redis down")
	})
}

func TestHandleRedisEventsGet(t *testing.T) {
	protectedChannels, err := LoadProtectedChannels("")
	assert.NilError(t, err)

	t.Run("new connection starts after current retained tail", func(t *testing.T) {
		client := &fakeRedisStreamClient{
			xrevrangeMessages: []redis.XMessage{{ID: "1700000000000-0"}},
			xreadResults: [][]redis.XStream{{
				{
					Stream: "gosmee:stream:test-channel",
					Messages: []redis.XMessage{{
						ID:     "1700000000001-0",
						Values: map[string]any{"payload": `{"future":true}`},
					}},
				},
			}},
		}
		relay := newRedisPayloadRelayWithClient(client, 10000)
		router := redisStreamRouter(relay, protectedChannels)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/events/test-channel", nil)
		reqCtx, cancel := context.WithCancel(req.Context())
		req = req.WithContext(reqCtx)
		response := httptest.NewRecorder()
		done := make(chan struct{})
		go func() {
			router.ServeHTTP(response, req)
			close(done)
		}()

		waitForRedisReads(t, client)
		cancel()
		<-done

		body := response.Body.String()
		assert.DeepEqual(t, client.xreadStreams[0], []string{"gosmee:stream:test-channel", "1700000000000-0"})
		assert.Assert(t, strings.Contains(body, "id: 1700000000001-0"))
		assert.Assert(t, strings.Contains(body, `{"future":true}`))
	})

	t.Run("Last-Event-ID replays newer entries", func(t *testing.T) {
		client := &fakeRedisStreamClient{
			xrangeMessages: []redis.XMessage{{ID: "1700000000000-0"}},
			xreadResults: [][]redis.XStream{{
				{
					Stream: "gosmee:stream:test-channel",
					Messages: []redis.XMessage{{
						ID:     "1700000000002-0",
						Values: map[string]any{"payload": `{"newer":true}`},
					}},
				},
			}},
		}
		relay := newRedisPayloadRelayWithClient(client, 10000)
		router := redisStreamRouter(relay, protectedChannels)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/events/test-channel", nil)
		req.Header.Set("Last-Event-ID", "1700000000001-0")
		reqCtx, cancel := context.WithCancel(req.Context())
		req = req.WithContext(reqCtx)
		response := httptest.NewRecorder()
		done := make(chan struct{})
		go func() {
			router.ServeHTTP(response, req)
			close(done)
		}()

		waitForRedisReads(t, client)
		cancel()
		<-done

		assert.Assert(t, strings.Contains(response.Body.String(), `{"newer":true}`))
		assert.DeepEqual(t, client.xreadStreams[0], []string{"gosmee:stream:test-channel", "1700000000001-0"})
	})

	t.Run("malformed Last-Event-ID is rejected", func(t *testing.T) {
		relay := newRedisPayloadRelayWithClient(&fakeRedisStreamClient{}, 10000)
		router := redisStreamRouter(relay, protectedChannels)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/events/test-channel", nil)
		req.Header.Set("Last-Event-ID", "not-a-stream-id")
		response := httptest.NewRecorder()

		router.ServeHTTP(response, req)

		assert.Equal(t, response.Code, http.StatusBadRequest)
	})

	t.Run("trimmed history emits gap event and continues from oldest retained", func(t *testing.T) {
		client := &fakeRedisStreamClient{
			xrangeMessages: []redis.XMessage{{ID: "1700000000005-0"}},
			xreadResults: [][]redis.XStream{{
				{
					Stream: "gosmee:stream:test-channel",
					Messages: []redis.XMessage{{
						ID:     "1700000000005-0",
						Values: map[string]any{"payload": `{"retained":true}`},
					}},
				},
			}},
		}
		relay := newRedisPayloadRelayWithClient(client, 10000)
		router := redisStreamRouter(relay, protectedChannels)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/events/test-channel", nil)
		req.Header.Set("Last-Event-ID", "1700000000001-0")
		reqCtx, cancel := context.WithCancel(req.Context())
		req = req.WithContext(reqCtx)
		response := httptest.NewRecorder()
		done := make(chan struct{})
		go func() {
			router.ServeHTTP(response, req)
			close(done)
		}()

		waitForRedisReads(t, client)
		cancel()
		<-done

		body := response.Body.String()
		assert.Assert(t, strings.Contains(body, "event: gosmee-gap"))
		assert.Assert(t, strings.Contains(body, `"oldest_id":"1700000000005-0"`))
		assert.Assert(t, strings.Contains(body, `{"retained":true}`))
		assert.DeepEqual(t, client.xreadStreams[0], []string{"gosmee:stream:test-channel", "0-0"})
	})

	t.Run("protected channel encrypts payload and preserves SSE ID", func(t *testing.T) {
		publicKey, privateKey, err := GenerateKeyPair()
		assert.NilError(t, err)
		allowed := EncodePublicKey(publicKey)
		protected := mustProtectedChannels(t, map[string][]string{
			"test-channel": {allowed},
		})
		client := &fakeRedisStreamClient{
			xrevrangeMessages: []redis.XMessage{{ID: "1700000000008-0"}},
			xreadResults: [][]redis.XStream{{
				{
					Stream: "gosmee:stream:test-channel",
					Messages: []redis.XMessage{{
						ID:     "1700000000009-0",
						Values: map[string]any{"payload": `{"secret":true}`},
					}},
				},
			}},
		}
		relay := newRedisPayloadRelayWithClient(client, 10000)
		router := redisStreamRouter(relay, protected)

		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/events/test-channel?pubkey="+url.QueryEscape(allowed), nil)
		reqCtx, cancel := context.WithCancel(req.Context())
		req = req.WithContext(reqCtx)
		response := httptest.NewRecorder()
		done := make(chan struct{})
		go func() {
			router.ServeHTTP(response, req)
			close(done)
		}()

		waitForRedisReads(t, client)
		cancel()
		<-done

		body := response.Body.String()
		parts := strings.Split(body, "data: ")
		lastData := strings.TrimSpace(parts[len(parts)-1])
		decrypted, err := Decrypt([]byte(lastData), privateKey)
		assert.NilError(t, err)
		assert.DeepEqual(t, decrypted, []byte(`{"secret":true}`))
		assert.Assert(t, !strings.Contains(body, `{"secret":true}`))
	})
}
