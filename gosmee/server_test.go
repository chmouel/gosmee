package gosmee

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/r3labs/sse/v2"
	"github.com/urfave/cli/v2"
	"gotest.tools/v3/assert"
)

func TestEventBroker(t *testing.T) {
	t.Run("Subscribe and Publish", func(t *testing.T) {
		eb := NewEventBroker()

		// Subscribe to a channel
		channel := "test-channel"
		subscriber := eb.Subscribe(channel)

		// Verify subscriber was added
		assert.Equal(t, subscriber.Channel, channel)
		assert.Assert(t, subscriber.Events != nil, "Events channel should not be nil")
		assert.Equal(t, len(eb.subscribers[channel]), 1)

		// Publish a message
		testData := []byte(`{"test":"data"}`)
		eb.Publish(channel, testData)

		// Verify subscriber received the message
		receivedData := <-subscriber.Events
		assert.DeepEqual(t, receivedData, testData)

		// Unsubscribe
		eb.Unsubscribe(channel, subscriber)

		// Verify subscriber was removed
		assert.Equal(t, len(eb.subscribers[channel]), 0)

		// Verify channel was closed (this would panic if not closed properly)
		_, isOpen := <-subscriber.Events
		assert.Assert(t, !isOpen, "Channel should be closed after unsubscribing")
	})

	t.Run("Multiple Subscribers", func(t *testing.T) {
		eb := NewEventBroker()
		channel := "test-channel"

		// Create multiple subscribers
		sub1 := eb.Subscribe(channel)
		sub2 := eb.Subscribe(channel)

		// Verify both were added
		assert.Equal(t, len(eb.subscribers[channel]), 2)

		// Publish a message
		testData := []byte(`{"test":"data"}`)
		eb.Publish(channel, testData)

		// Verify both received the message
		assert.DeepEqual(t, <-sub1.Events, testData)
		assert.DeepEqual(t, <-sub2.Events, testData)

		// Unsubscribe one
		eb.Unsubscribe(channel, sub1)

		// Verify only sub1 was removed
		assert.Equal(t, len(eb.subscribers[channel]), 1)

		// Publish another message
		testData2 := []byte(`{"test":"data2"}`)
		eb.Publish(channel, testData2)

		// Verify only sub2 received it
		assert.DeepEqual(t, <-sub2.Events, testData2)
	})
}

func TestWebhookSignatureValidation(t *testing.T) {
	t.Run("GitHub Signature", func(t *testing.T) {
		secret := "test-secret"
		payload := []byte(`{"event":"test"}`)

		// Generate a valid signature
		mac := createGitHubSignature(secret, payload)

		// Test valid signature
		valid := validateGitHubWebhookSignature(secret, payload, "sha256="+mac)
		assert.Assert(t, valid, "Valid signature should be accepted")

		// Test invalid signature
		invalid := validateGitHubWebhookSignature(secret, payload, "sha256=invalid")
		assert.Assert(t, !invalid, "Invalid signature should be rejected")

		// Test invalid format
		invalidFormat := validateGitHubWebhookSignature(secret, payload, "invalid-format")
		assert.Assert(t, !invalidFormat, "Invalid format should be rejected")
	})

	t.Run("Bitbucket HMAC", func(t *testing.T) {
		secret := "test-secret"
		payload := []byte(`{"event":"test"}`)

		// Create valid signature
		mac := createBitbucketSignature(secret, payload)

		// Test valid signature
		valid := validateBitbucketHMAC(secret, payload, mac)
		assert.Assert(t, valid, "Valid signature should be accepted")

		// Test invalid signature
		invalid := validateBitbucketHMAC(secret, payload, "invalid")
		assert.Assert(t, !invalid, "Invalid signature should be rejected")
	})

	t.Run("Gitea Signature", func(t *testing.T) {
		secret := "test-secret"
		payload := []byte(`{"event":"test"}`)

		// Create valid signature
		mac := createGiteaSignature(secret, payload)

		// Test valid signature
		valid := validateGiteaSignature(secret, payload, "sha256="+mac)
		assert.Assert(t, valid, "Valid signature should be accepted")

		// Test invalid signature
		invalid := validateGiteaSignature(secret, payload, "sha256=invalid")
		assert.Assert(t, !invalid, "Invalid signature should be rejected")

		// Test invalid format
		invalidFormat := validateGiteaSignature(secret, payload, "invalid-format")
		assert.Assert(t, !invalidFormat, "Invalid format should be rejected")
	})

	t.Run("Validate Multiple Providers", func(t *testing.T) {
		secrets := []string{"secret1", "secret2"}
		payload := []byte(`{"event":"test"}`)

		// GitHub header
		r := httptest.NewRequest(http.MethodPost, "/webhook", nil)
		r.Header.Set("X-Hub-Signature-256", "sha256="+createGitHubSignature("secret1", payload))
		valid := validateWebhookSignature(secrets, payload, r)
		assert.Assert(t, valid, "Valid GitHub signature should be accepted")

		// Bitbucket header
		r = httptest.NewRequest(http.MethodPost, "/webhook", nil)
		r.Header.Set("X-Hub-Signature", createBitbucketSignature("secret2", payload))
		valid = validateWebhookSignature(secrets, payload, r)
		assert.Assert(t, valid, "Valid Bitbucket signature should be accepted")

		// GitLab token
		r = httptest.NewRequest(http.MethodPost, "/webhook", nil)
		r.Header.Set("X-Gitlab-Token", "secret1")
		valid = validateWebhookSignature(secrets, payload, r)
		assert.Assert(t, valid, "Valid GitLab token should be accepted")

		// Gitea signature
		r = httptest.NewRequest(http.MethodPost, "/webhook", nil)
		r.Header.Set("X-Gitea-Signature", "sha256="+createGiteaSignature("secret2", payload))
		valid = validateWebhookSignature(secrets, payload, r)
		assert.Assert(t, valid, "Valid Gitea signature should be accepted")

		// No secrets provided
		valid = validateWebhookSignature([]string{}, payload, r)
		assert.Assert(t, valid, "No secrets should always return true")

		// Invalid signatures
		r = httptest.NewRequest(http.MethodPost, "/webhook", nil)
		r.Header.Set("X-Hub-Signature-256", "sha256=invalid")
		valid = validateWebhookSignature(secrets, payload, r)
		assert.Assert(t, !valid, "Invalid signature should be rejected")
	})
}

func newTestContext() *cli.Context {
	app := cli.NewApp()
	flagSet := flag.NewFlagSet("test", 0)
	flagSet.Int("max-body-size", 26214400, "doc")
	return cli.NewContext(app, flagSet, nil)
}

func TestHandleWebhookPost(t *testing.T) {
	// Set up router, SSE server, and event broker
	router := chi.NewRouter()
	events := sse.New()
	eventBroker := NewEventBroker()
	ctx := newTestContext()

	// Set up the webhook endpoint
	router.Post("/webhook/{channel}", handleWebhookPost(ctx, events, eventBroker, []string{}))

	t.Run("Valid Webhook", func(t *testing.T) {
		// Create a subscriber to verify event was published
		subscriber := eventBroker.Subscribe("test-channel")

		// Create a test request
		payload := map[string]any{
			"event": "test",
			"data":  "value",
		}
		payloadBytes, _ := json.Marshal(payload)
		req := httptest.NewRequest(http.MethodPost, "/webhook/test-channel", bytes.NewReader(payloadBytes))
		req.Header.Set("Content-Type", contentType)
		req.Header.Set("X-Event-Type", "test-event")

		// Record the response
		w := httptest.NewRecorder()

		// Route the request
		// Set up URL parameters since we're not using the full router
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("channel", "test-channel")
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

		handler := handleWebhookPost(ctx, events, eventBroker, []string{})
		handler(w, req)

		// Check response
		resp := w.Result()
		assert.Equal(t, resp.StatusCode, http.StatusAccepted)

		// Check that the event was published
		select {
		case event := <-subscriber.Events:
			// Verify the event data contains our payload
			assert.Assert(t, len(event) > 0)

			// Parse the event and check key fields
			var eventData map[string]any
			err := json.Unmarshal(event, &eventData)
			assert.NilError(t, err)

			// Check that headers were properly set
			assert.Equal(t, eventData["x-event-type"], "test-event")

			// Check that the body was base64 encoded
			assert.Assert(t, eventData["bodyB"] != nil)

			// Check that timestamp was added
			assert.Assert(t, eventData["timestamp"] != nil)
		default:
			t.Fatal("Expected event to be published but none was received")
		}

		// Clean up
		eventBroker.Unsubscribe("test-channel", subscriber)
	})

	t.Run("Invalid Content Type", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/webhook/test-channel", strings.NewReader("not json"))
		req.Header.Set("Content-Type", "text/plain")

		w := httptest.NewRecorder()

		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("channel", "test-channel")
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

		handler := handleWebhookPost(ctx, events, eventBroker, []string{})
		handler(w, req)

		resp := w.Result()
		assert.Equal(t, resp.StatusCode, http.StatusBadRequest)

		body, _ := io.ReadAll(resp.Body)
		assert.Assert(t, strings.Contains(string(body), "content-type must be application/json"))
	})

	t.Run("Invalid JSON", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/webhook/test-channel", strings.NewReader("not json"))
		req.Header.Set("Content-Type", contentType)

		w := httptest.NewRecorder()

		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("channel", "test-channel")
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

		handler := handleWebhookPost(ctx, events, eventBroker, []string{})
		handler(w, req)

		resp := w.Result()
		assert.Equal(t, resp.StatusCode, http.StatusBadRequest)
	})

	t.Run("Signature Validation", func(t *testing.T) {
		secrets := []string{"test-secret"}
		payload := []byte(`{"event":"test"}`)

		// Valid signature
		req := httptest.NewRequest(http.MethodPost, "/webhook/test-channel", bytes.NewReader(payload))
		req.Header.Set("Content-Type", contentType)
		req.Header.Set("X-Hub-Signature-256", "sha256="+createGitHubSignature("test-secret", payload))

		w := httptest.NewRecorder()

		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("channel", "test-channel")
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

		handler := handleWebhookPost(ctx, events, eventBroker, secrets)
		handler(w, req)

		resp := w.Result()
		assert.Equal(t, resp.StatusCode, http.StatusAccepted)

		// Invalid signature
		req = httptest.NewRequest(http.MethodPost, "/webhook/test-channel", bytes.NewReader(payload))
		req.Header.Set("Content-Type", contentType)
		req.Header.Set("X-Hub-Signature-256", "sha256=invalid")

		w = httptest.NewRecorder()

		rctx = chi.NewRouteContext()
		rctx.URLParams.Add("channel", "test-channel")
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

		handler = handleWebhookPost(ctx, events, eventBroker, secrets)
		handler(w, req)

		resp = w.Result()
		assert.Equal(t, resp.StatusCode, http.StatusUnauthorized)
	})
}

func TestRetVersion(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/version", nil)
	w := httptest.NewRecorder()

	retVersion(w, req)

	resp := w.Result()
	assert.Equal(t, resp.StatusCode, http.StatusOK)
	assert.Equal(t, resp.Header.Get("Content-Type"), contentType)
	assert.Equal(t, resp.Header.Get(versionHeaderName), strings.TrimSpace(string(Version)))

	// Verify JSON response
	body, _ := io.ReadAll(resp.Body)
	var response map[string]string
	err := json.Unmarshal(body, &response)
	assert.NilError(t, err)
	assert.Equal(t, response["version"], strings.TrimSpace(string(Version)))
}

func TestIPRestrictions(t *testing.T) {
	t.Run("Parse IP Ranges", func(t *testing.T) {
		ranges := []string{
			"192.168.0.0/24",
			"10.0.0.1",
			"2001:db8::/32",
		}

		ipRanges, err := parseIPRanges(ranges)
		assert.NilError(t, err)
		assert.Equal(t, len(ipRanges.networks), 2)
		assert.Equal(t, len(ipRanges.ips), 1)

		// Test valid IPs
		assert.Assert(t, ipRanges.contains(net.ParseIP("192.168.0.100")))
		assert.Assert(t, ipRanges.contains(net.ParseIP("10.0.0.1")))
		assert.Assert(t, ipRanges.contains(net.ParseIP("2001:db8::1")))

		// Test invalid IPs
		assert.Assert(t, !ipRanges.contains(net.ParseIP("192.168.1.1")))
		assert.Assert(t, !ipRanges.contains(net.ParseIP("10.0.0.2")))
		assert.Assert(t, !ipRanges.contains(net.ParseIP("2001:db9::1")))

		// Test invalid ranges
		_, err = parseIPRanges([]string{"invalid"})
		assert.Assert(t, err != nil)
	})

	t.Run("Get Real IP", func(t *testing.T) {
		// Test direct IP
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "192.168.0.1:12345"

		ip, err := getRealIP(req, false)
		assert.NilError(t, err)
		assert.Equal(t, ip.String(), "192.168.0.1")

		// Test X-Forwarded-For with trust proxy
		req = httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "127.0.0.1:12345"
		req.Header.Set("X-Forwarded-For", "10.0.0.1")

		ip, err = getRealIP(req, true)
		assert.NilError(t, err)
		assert.Equal(t, ip.String(), "10.0.0.1")

		// Test X-Forwarded-For without trust proxy
		ip, err = getRealIP(req, false)
		assert.NilError(t, err)
		assert.Equal(t, ip.String(), "127.0.0.1")

		// Test invalid remote addr
		req = httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "invalid"

		_, err = getRealIP(req, false)
		assert.Assert(t, err != nil)
	})

	t.Run("IP Restrict Middleware", func(t *testing.T) {
		// Create allowed IP ranges that include the test IP
		ranges, _ := parseIPRanges([]string{"127.0.0.0/8"})
		middleware := ipRestrictMiddleware(ranges, false)

		// Create a test handler
		nextCalled := false
		next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			nextCalled = true
			w.WriteHeader(http.StatusOK)
		})

		// Test allowed IP with POST request (IP restriction is only applied to POST requests)
		req := httptest.NewRequest(http.MethodPost, "/", nil) // Changed from GET to POST
		req.RemoteAddr = "127.0.0.1:12345"
		w := httptest.NewRecorder()

		middleware(next).ServeHTTP(w, req)
		assert.Assert(t, nextCalled, "Next handler should be called for allowed IP")
		assert.Equal(t, w.Result().StatusCode, http.StatusOK)

		// Test disallowed IP with POST request
		nextCalled = false
		req = httptest.NewRequest(http.MethodPost, "/", nil) // Changed from GET to POST
		req.RemoteAddr = "192.168.0.1:12345"
		w = httptest.NewRecorder()

		middleware(next).ServeHTTP(w, req)
		assert.Assert(t, !nextCalled, "Next handler should not be called for disallowed IP")
		assert.Equal(t, w.Result().StatusCode, http.StatusForbidden)

		// Test that GET request bypasses IP restriction
		nextCalled = false
		req = httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "192.168.0.1:12345" // IP would be restricted for POST
		w = httptest.NewRecorder()

		middleware(next).ServeHTTP(w, req)
		assert.Assert(t, nextCalled, "Next handler should be called for GET request regardless of IP")
		assert.Equal(t, w.Result().StatusCode, http.StatusOK)
	})
}

// Helper functions to create signatures for testing

func createGitHubSignature(secret string, payload []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil))
}

func createBitbucketSignature(secret string, payload []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil))
}

func createGiteaSignature(secret string, payload []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil))
}
