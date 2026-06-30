package gosmee

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/redis/go-redis/v9"
	"gotest.tools/v3/assert"
)

func openRedisIntegrationStream(t *testing.T, serverURL, channel, lastEventID string) (*http.Response, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, serverURL+"/events/"+channel, nil)
	assert.NilError(t, err)
	if lastEventID != "" {
		req.Header.Set("Last-Event-ID", lastEventID)
	}
	resp, err := http.DefaultClient.Do(req)
	assert.NilError(t, err)
	assert.Equal(t, resp.StatusCode, http.StatusOK)
	t.Cleanup(func() {
		cancel()
		_ = resp.Body.Close()
	})
	return resp, cancel
}

func readRedisIntegrationStreamUntil(t *testing.T, body io.Reader, want string) string {
	t.Helper()
	lines := make(chan string)
	errs := make(chan error, 1)
	go func() {
		defer close(lines)
		scanner := bufio.NewScanner(body)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
		errs <- scanner.Err()
	}()

	var collected strings.Builder
	dataLines := make([]string, 0, 2)
	matchesWant := func(data string) bool {
		if strings.Contains(data, want) {
			return true
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(data), &payload); err != nil {
			return false
		}
		bodyB, ok := payload["bodyB"].(string)
		if !ok {
			return false
		}
		decoded, err := base64.StdEncoding.DecodeString(bodyB)
		if err != nil {
			return false
		}
		fmt.Fprintf(&collected, "decoded-body: %s\n", decoded)
		return strings.Contains(string(decoded), want)
	}
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		select {
		case line, ok := <-lines:
			if !ok {
				if err := <-errs; err != nil {
					t.Fatalf("reading SSE stream failed: %v", err)
				}
				t.Fatalf("SSE stream ended before %q; got %s", want, collected.String())
			}
			collected.WriteString(line)
			collected.WriteByte('\n')
			switch {
			case line == "":
				data := strings.Join(dataLines, "\n")
				dataLines = dataLines[:0]
				if matchesWant(data) {
					return collected.String()
				}
			case strings.HasPrefix(line, "data: "):
				dataLines = append(dataLines, strings.TrimPrefix(line, "data: "))
			case strings.HasPrefix(line, "data:"):
				dataLines = append(dataLines, strings.TrimPrefix(line, "data:"))
			}
			if strings.Contains(collected.String(), want) {
				return collected.String()
			}
		case <-timer.C:
			t.Fatalf("timed out waiting for %q in SSE stream; got %s", want, collected.String())
		}
	}
}

func TestRedisStreamsIntegration(t *testing.T) {
	redisURL := os.Getenv("GOSMEE_REDIS_TEST_URL")
	if redisURL == "" {
		t.Skip("GOSMEE_REDIS_TEST_URL is not set")
	}

	ctx := context.Background()
	redisOptions, err := redis.ParseURL(redisURL)
	assert.NilError(t, err)
	cleanupClient := redis.NewClient(redisOptions)
	defer cleanupClient.Close()

	relayA, err := newRedisPayloadRelay(ctx, redisURL, 0)
	assert.NilError(t, err)
	defer relayA.Close()
	relayB, err := newRedisPayloadRelay(ctx, redisURL, 0)
	assert.NilError(t, err)

	protectedChannels, err := LoadProtectedChannels("")
	assert.NilError(t, err)
	streamRouter := redisStreamRouter(relayB, protectedChannels)
	streamServer := httptest.NewServer(streamRouter)
	defer streamServer.Close()
	defer relayB.Close()
	postCtx := newTestContext()
	channel := "itest-" + randomString(16)
	defer func() {
		assert.NilError(t, cleanupClient.Del(ctx, redisStreamKeyPrefix+channel).Err())
	}()

	postWebhook := func(t *testing.T, body string) {
		t.Helper()
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/"+channel, strings.NewReader(body))
		req.Header.Set("Content-Type", contentType)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("channel", channel)
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
		w := httptest.NewRecorder()
		handleWebhookPost(postCtx, relayA, nil)(w, req)
		assert.Equal(t, w.Result().StatusCode, http.StatusAccepted)
	}

	t.Run("POST to one relay is readable from another", func(t *testing.T) {
		resp, cancel := openRedisIntegrationStream(t, streamServer.URL, channel, "")
		defer resp.Body.Close()

		postWebhook(t, `{"integration":"cross-instance"}`)

		body := readRedisIntegrationStreamUntil(t, resp.Body, "cross-instance")
		cancel()
		assert.Assert(t, strings.Contains(body, "cross-instance"))
	})

	t.Run("Last-Event-ID resumes newer entries", func(t *testing.T) {
		resp, cancel := openRedisIntegrationStream(t, streamServer.URL, channel, "")

		postWebhook(t, `{"integration":"first"}`)
		body := readRedisIntegrationStreamUntil(t, resp.Body, "first")
		cancel()
		resp.Body.Close()

		firstID := ""
		for _, line := range strings.Split(body, "\n") {
			if strings.HasPrefix(line, "id: ") {
				firstID = strings.TrimSpace(strings.TrimPrefix(line, "id: "))
			}
		}
		assert.Assert(t, firstID != "")

		postWebhook(t, `{"integration":"second"}`)

		resp, cancel = openRedisIntegrationStream(t, streamServer.URL, channel, firstID)
		defer resp.Body.Close()

		body = readRedisIntegrationStreamUntil(t, resp.Body, "second")
		cancel()
		assert.Assert(t, strings.Contains(body, "second"))
		assert.Assert(t, !strings.Contains(body, `decoded-body: {"integration":"first"}`), body)
	})
}
