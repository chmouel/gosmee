package gosmee

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"gotest.tools/v3/assert"
)

const multiSegmentChannel = "github/myorg/myrepo/push"

func TestIsValidChannelID(t *testing.T) {
	for _, channel := range []string{
		"abc",
		"test-channel",
		"under_score",
		multiSegmentChannel,
		"a/b/c/d/e/f/g",
		strings.Repeat("a", maxChannelLength),
		strings.Repeat("ab/", 60) + "c",
	} {
		assert.Assert(t, isValidChannelID(channel), "channel %q should be valid", channel)
	}

	for _, channel := range []string{
		"",
		"/leading",
		"trailing/",
		"double//slash",
		"has space",
		"has.dot",
		"has:colon",
		"..",
		"a/../b",
		strings.Repeat("a", maxChannelLength+1),
	} {
		assert.Assert(t, !isValidChannelID(channel), "channel %q should be invalid", channel)
	}
}

func TestMainRouterChannelPaths(t *testing.T) {
	eventsChannel := ""
	router := chi.NewRouter()
	registerMainRoutes(router, "https://example.com", "", nil, func(_ http.ResponseWriter, r *http.Request) {
		eventsChannel = chi.URLParam(r, "*")
	})

	get := func(path string) *httptest.ResponseRecorder {
		t.Helper()

		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, nil))
		return w
	}

	t.Run("static routes win over the channel catch-all", func(t *testing.T) {
		assert.Equal(t, get("/version").Code, http.StatusOK)
		assert.Equal(t, get("/health").Code, http.StatusOK)
		assert.Equal(t, get("/livez").Code, http.StatusOK)
		assert.Equal(t, get("/favicon.ico").Header().Get("Content-Type"), "image/svg+xml")

		newBody := get("/new").Body.String()
		assert.Assert(t, strings.HasPrefix(newBody, "https://example.com/"), newBody)
	})

	t.Run("root redirects to a generated channel", func(t *testing.T) {
		w := get("/")
		assert.Equal(t, w.Code, http.StatusFound)
		assert.Assert(t, strings.HasPrefix(w.Header().Get("Location"), "https://example.com/"))
	})

	t.Run("multi segment channel serves the index", func(t *testing.T) {
		w := get("/" + multiSegmentChannel)
		assert.Equal(t, w.Code, http.StatusOK)
		assert.Assert(t, strings.Contains(w.Body.String(), "https://example.com/"+multiSegmentChannel))
		// The replay button posts to this channel, so the page must carry every
		// segment and not just the last one. html/template writes the slashes as
		// \/ inside the JS string literal, which the browser reads back as "/".
		escaped := strings.ReplaceAll(multiSegmentChannel, "/", `\/`)
		assert.Assert(t, strings.Contains(w.Body.String(), "const channel = '"+escaped+"'"), "channel is missing from the replay script")
	})

	t.Run("multi segment channel reaches the events handler", func(t *testing.T) {
		eventsChannel = ""
		assert.Equal(t, get("/events/"+multiSegmentChannel).Code, http.StatusOK)
		assert.Equal(t, eventsChannel, multiSegmentChannel)
	})

	t.Run("invalid channels are not found", func(t *testing.T) {
		for _, path := range invalidChannelPaths() {
			assert.Equal(t, get(path).Code, http.StatusNotFound, "path %q should be rejected", path)
		}
	})
}

func TestRestrictedRouterChannelPaths(t *testing.T) {
	eventBroker := NewEventBroker()
	relay := newLocalPayloadRelay(eventBroker)
	cliCtx := newTestContext()

	router := chi.NewRouter()
	router.Post(channelPath, handleWebhookPost(cliCtx, relay, []string{}))
	router.Post(replayPath, handleReplayPost(cliCtx, relay))

	post := func(path string) *httptest.ResponseRecorder {
		t.Helper()

		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, path, strings.NewReader(`{"ok":true}`))
		req.Header.Set("Content-Type", contentType)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}

	t.Run("multi segment channel accepts a webhook", func(t *testing.T) {
		subscriber := eventBroker.Subscribe(multiSegmentChannel, nil)
		defer eventBroker.Unsubscribe(multiSegmentChannel, subscriber)

		w := post("/" + multiSegmentChannel)
		assert.Equal(t, w.Code, http.StatusAccepted)

		var resp map[string]any
		assert.NilError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Equal(t, resp["channel"], multiSegmentChannel)

		select {
		case event := <-subscriber.Events:
			assert.Assert(t, len(event.Data) > 0)
		default:
			t.Fatal("expected the webhook to be published to the multi segment channel")
		}
	})

	// /replay/ stays a reserved prefix now that a channel id may itself contain
	// slashes, so it cannot be reached as a webhook channel.
	t.Run("replay prefix wins over the channel catch-all", func(t *testing.T) {
		w := post("/replay/" + multiSegmentChannel)
		assert.Equal(t, w.Code, http.StatusAccepted)
		assert.Equal(t, w.Body.String(), "replayed")
	})

	t.Run("invalid channels are rejected", func(t *testing.T) {
		for _, path := range invalidChannelPaths() {
			assert.Equal(t, post(path).Code, http.StatusBadRequest, "path %q should be rejected", path)
		}
	})
}

// serve() puts both routers behind a chi Mount, which matches with a wildcard of
// its own, so check that the channel a handler reads is the inner match and not
// the mount's.
func TestMountedRouterChannelPaths(t *testing.T) {
	eventsChannel := ""
	mainRouter := chi.NewRouter()
	registerMainRoutes(mainRouter, "https://example.com", "", nil, func(_ http.ResponseWriter, r *http.Request) {
		eventsChannel = chi.URLParam(r, "*")
	})

	eventBroker := NewEventBroker()
	restrictedRouter := chi.NewRouter()
	restrictedRouter.Post(channelPath, handleWebhookPost(newTestContext(), newLocalPayloadRelay(eventBroker), []string{}))

	finalRouter := chi.NewRouter()
	finalRouter.Mount("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			restrictedRouter.ServeHTTP(w, r)
		} else {
			mainRouter.ServeHTTP(w, r)
		}
	}))

	do := func(method, path string, body io.Reader) *httptest.ResponseRecorder {
		t.Helper()

		req := httptest.NewRequestWithContext(context.Background(), method, path, body)
		req.Header.Set("Content-Type", contentType)
		w := httptest.NewRecorder()
		finalRouter.ServeHTTP(w, req)
		return w
	}

	t.Run("events keeps the inner wildcard match", func(t *testing.T) {
		eventsChannel = ""
		assert.Equal(t, do(http.MethodGet, "/events/"+multiSegmentChannel, nil).Code, http.StatusOK)
		assert.Equal(t, eventsChannel, multiSegmentChannel)
	})

	t.Run("index and static routes still resolve", func(t *testing.T) {
		assert.Equal(t, do(http.MethodGet, "/version", nil).Code, http.StatusOK)

		w := do(http.MethodGet, "/"+multiSegmentChannel, nil)
		assert.Equal(t, w.Code, http.StatusOK)
		assert.Assert(t, strings.Contains(w.Body.String(), "https://example.com/"+multiSegmentChannel))
	})

	t.Run("webhook post keeps every segment", func(t *testing.T) {
		w := do(http.MethodPost, "/"+multiSegmentChannel, strings.NewReader(`{"ok":true}`))
		assert.Equal(t, w.Code, http.StatusAccepted)

		var resp map[string]any
		assert.NilError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Equal(t, resp["channel"], multiSegmentChannel)
	})

	t.Run("invalid channels are still rejected", func(t *testing.T) {
		for _, path := range invalidChannelPaths() {
			assert.Equal(t, do(http.MethodGet, path, nil).Code, http.StatusNotFound, "GET %q should be rejected", path)
			assert.Equal(t, do(http.MethodPost, path, strings.NewReader(`{}`)).Code, http.StatusBadRequest, "POST %q should be rejected", path)
		}
	})
}

func invalidChannelPaths() []string {
	return []string{
		"/a//b",
		"/has.dot",
		"/trailing/",
		"/" + strings.Repeat("a", maxChannelLength+1),
	}
}
