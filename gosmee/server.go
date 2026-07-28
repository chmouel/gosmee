package gosmee

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/urfave/cli/v2"
	"golang.org/x/crypto/acme/autocert"
)

// Version header constant.
const (
	timeFormat        = "2006-01-02T15.04.01.000"
	contentType       = "application/json"
	versionHeaderName = "X-Gosmee-Version"
	minChannelLength  = 12  // Length of the ids handed out by /new
	maxChannelLength  = 255 // Set maximum channel length to prevent DoS attacks
	// A channel id is one or more slash separated segments, so it can mirror a
	// real webhook path such as "github/myorg/myrepo/push".
	channelIDPattern = "[a-zA-Z0-9_-]+(?:/[a-zA-Z0-9_-]+)*"
	// Channel ids may contain slashes and chi matches a {param:regexp} only up
	// to the next slash, so the routes catch everything and the handlers
	// enforce channelIDPattern themselves via requestChannel.
	channelPath = "/*"
	eventsPath  = "/events/*"
	replayPath  = "/replay/*"
)

var (
	defaultServerPort        = 3333
	defaultServerAddress     = "localhost"
	defaultRedisStreamMaxLen = 10000
	validChannelID           = regexp.MustCompile("^" + channelIDPattern + "$")
)

//go:embed templates/index.tmpl
var indexTmpl []byte

//go:embed templates/favicon.svg
var faviconSVG []byte

// Subscriber represents a client connection listening for events.
type Subscriber struct {
	Channel   string
	Events    chan relayEvent
	PublicKey *[32]byte
}

// EventBroker manages event subscriptions and publications.
type EventBroker struct {
	sync.RWMutex
	subscribers map[string][]*Subscriber
	logger      *slog.Logger
}

// NewEventBroker creates a new event broker.
func NewEventBroker(loggers ...*slog.Logger) *EventBroker {
	logger := slog.Default()
	if len(loggers) > 0 && loggers[0] != nil {
		logger = loggers[0]
	}
	return &EventBroker{
		subscribers: make(map[string][]*Subscriber),
		logger:      logger,
	}
}

// Subscribe adds a subscriber for a specific channel.
func (eb *EventBroker) Subscribe(channel string, pubKey *[32]byte) *Subscriber {
	eb.Lock()
	defer eb.Unlock()

	subscriber := &Subscriber{
		Channel:   channel,
		Events:    make(chan relayEvent, 100), // Buffer size to prevent blocking
		PublicKey: pubKey,
	}

	eb.subscribers[channel] = append(eb.subscribers[channel], subscriber)
	return subscriber
}

// Unsubscribe removes a subscriber from a channel.
func (eb *EventBroker) Unsubscribe(channel string, subscriber *Subscriber) {
	eb.Lock()
	defer eb.Unlock()

	subscribers := eb.subscribers[channel]
	for i, s := range subscribers {
		if s == subscriber {
			// Remove subscriber from slice
			eb.subscribers[channel] = slices.Delete(subscribers, i, i+1)
			close(subscriber.Events)
			break
		}
	}

	if len(eb.subscribers[channel]) == 0 {
		delete(eb.subscribers, channel)
	}
}

// Publish sends an event to all subscribers of a channel.
func (eb *EventBroker) Publish(channel string, data []byte) {
	eb.PublishEvent(channel, relayEvent{Data: data})
}

// PublishEvent sends an event to all subscribers of a channel.
func (eb *EventBroker) PublishEvent(channel string, event relayEvent) {
	eb.RLock()
	subscribers := append([]*Subscriber(nil), eb.subscribers[channel]...)
	eb.RUnlock()
	if event.DeliveryID == "" {
		event.DeliveryID, event.EventType = relayEventMetadata(event.Data)
	}

	// Send to each subscriber
	for _, s := range subscribers {
		payload := event
		if s.PublicKey != nil {
			encrypted, err := Encrypt(event.Data, s.PublicKey)
			if err != nil {
				fmt.Fprintf(os.Stderr, "WARNING: encryption failed for subscriber on channel %s: %v\n", s.Channel, err) //nolint:gosec // stderr, not web output
				continue
			}
			payload.Data = encrypted
		}

		// Non-blocking send - if buffer is full, we'll skip this subscriber
		select {
		case s.Events <- payload:
			eb.logger.LogAttrs(context.Background(), slog.LevelDebug, "event queued for subscriber",
				slog.String("channel", channel), slog.String("delivery_id", payload.DeliveryID),
				slog.String("stream_id", payload.ID), slog.String("event_type", payload.EventType),
				slog.Int("queue_depth", len(s.Events)))
		default:
			eb.logger.LogAttrs(context.Background(), slog.LevelWarn, "event dropped for subscriber: buffer full",
				slog.String("channel", channel), slog.String("delivery_id", payload.DeliveryID),
				slog.String("stream_id", payload.ID), slog.String("event_type", payload.EventType),
				slog.Int("queue_depth", len(s.Events)))
		}
	}
}

func rejectProtectedChannelRequest(w http.ResponseWriter) {
	http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
}

func nextPlaintextChannel(protectedChannels *ProtectedChannels) string {
	for {
		channel := randomString(minChannelLength)
		if !protectedChannels.Has(channel) {
			return channel
		}
	}
}

func isValidChannelID(channel string) bool {
	return len(channel) <= maxChannelLength && validChannelID.MatchString(channel)
}

// requestChannel returns the channel id matched by a catch-all route and
// whether it is valid.
func requestChannel(r *http.Request) (string, bool) {
	channel := chi.URLParam(r, "*")
	return channel, isValidChannelID(channel)
}

func rejectInvalidChannelRequest(w http.ResponseWriter) {
	http.Error(w, "invalid channel name", http.StatusBadRequest)
}

func effectivePublicURL(publicURL, portAddr string, sslEnabled bool) string {
	if publicURL != "" {
		return publicURL
	}

	scheme := "http://"
	if sslEnabled {
		scheme = "https://"
	}

	return fmt.Sprintf("%s%s", scheme, portAddr)
}

func showNewURL(publicURL string, protectedChannels *ProtectedChannels) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		url := fmt.Sprintf("%s/%s", publicURL, nextPlaintextChannel(protectedChannels))
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "%s\n", url)
	}
}

func serveIndex(publicURL, footer string, protectedChannels *ProtectedChannels) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		channel, valid := requestChannel(r)
		if channel == "" {
			http.Redirect(w, r, fmt.Sprintf("%s/%s", publicURL, nextPlaintextChannel(protectedChannels)), http.StatusFound)
			return
		}
		if !valid {
			http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
			return
		}
		if protectedChannels.Has(channel) {
			rejectProtectedChannelRequest(w)
			return
		}

		url := fmt.Sprintf("%s/%s", publicURL, channel)
		eventsURL := fmt.Sprintf("/events/%s", channel)

		t, err := template.New("index").Parse(string(indexTmpl))
		if err != nil {
			errorIt(w, r, http.StatusInternalServerError, err)
			return
		}

		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
		varmap := map[string]any{
			"URL":       url,
			"EventsURL": eventsURL,
			"Channel":   channel,
			"Version":   string(Version),
			"Footer":    template.HTML(footer), //nolint:gosec // operator-trusted input; intentionally rendered as raw HTML
		}
		if err := t.ExecuteTemplate(w, "index", varmap); err != nil {
			errorIt(w, r, http.StatusInternalServerError, err)
		}
	}
}

// registerMainRoutes registers the unrestricted GET routes. channelPath is a
// catch-all, so chi only reaches it once every static route above has been
// ruled out.
func registerMainRoutes(mainRouter chi.Router, publicURL, footer string, protectedChannels *ProtectedChannels, eventsHandler http.HandlerFunc) {
	mainRouter.Get("/favicon.ico", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/svg+xml")
		_, _ = w.Write(faviconSVG)
	})
	mainRouter.Get("/", serveIndex(publicURL, footer, protectedChannels))
	mainRouter.Get("/new", showNewURL(publicURL, protectedChannels))
	mainRouter.Get("/version", retVersion)
	mainRouter.Get("/health", retVersion)
	mainRouter.Get("/livez", retVersion)
	mainRouter.Get(eventsPath, eventsHandler)
	mainRouter.Get(channelPath, serveIndex(publicURL, footer, protectedChannels))
}

func errorIt(w http.ResponseWriter, _ *http.Request, status int, err error) {
	w.WriteHeader(status)
	_, _ = w.Write([]byte(err.Error()))
}

// validateGitHubWebhookSignature validates the GitHub webhook signature.
func validateGitHubWebhookSignature(secret string, payload []byte, signatureHeader string) bool {
	if !strings.HasPrefix(signatureHeader, "sha256=") {
		return false
	}

	signature := strings.TrimPrefix(signatureHeader, "sha256=")
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	expectedMAC := hex.EncodeToString(mac.Sum(nil))

	return hmac.Equal([]byte(signature), []byte(expectedMAC))
}

// validateBitbucketHMAC validates Bitbucket Cloud/Server webhook HMAC signature.
func validateBitbucketHMAC(secret string, payload []byte, signatureHeader string) bool {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	expectedMAC := hex.EncodeToString(mac.Sum(nil))

	return hmac.Equal([]byte(signatureHeader), []byte(expectedMAC))
}

// validateGiteaSignature validates Gitea/Forge webhook signature.
func validateGiteaSignature(secret string, payload []byte, signatureHeader string) bool {
	if !strings.HasPrefix(signatureHeader, "sha256=") {
		return false
	}

	signature := strings.TrimPrefix(signatureHeader, "sha256=")
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	expectedMAC := hex.EncodeToString(mac.Sum(nil))

	return hmac.Equal([]byte(signature), []byte(expectedMAC))
}

// validateWebhookSignature validates webhook signatures for different providers by trying multiple secrets.
func validateWebhookSignature(secrets []string, payload []byte, r *http.Request) bool {
	if len(secrets) == 0 {
		return true // No validation needed if no secrets configured
	}

	// Check for GitLab token
	if gitlabToken := r.Header.Get("X-Gitlab-Token"); gitlabToken != "" {
		for _, secret := range secrets {
			if subtle.ConstantTimeCompare([]byte(gitlabToken), []byte(secret)) == 1 {
				return true
			}
		}
		return false
	}

	// Check for GitHub signature
	if githubSignature := r.Header.Get("X-Hub-Signature-256"); githubSignature != "" {
		fmt.Fprintf(os.Stdout, "Received request %s %s\n", r.Method, r.URL.Path) //nolint:gosec // stdout, not web output
		for _, secret := range secrets {
			if validateGitHubWebhookSignature(secret, payload, githubSignature) {
				return true
			}
		}
		return false
	}

	// Check for Bitbucket Cloud/Server signature
	if bitbucketSignature := r.Header.Get("X-Hub-Signature"); bitbucketSignature != "" {
		for _, secret := range secrets {
			if validateBitbucketHMAC(secret, payload, bitbucketSignature) {
				return true
			}
		}
		return false
	}

	// Check for Gitea/Forge signature
	if giteaSignature := r.Header.Get("X-Gitea-Signature"); giteaSignature != "" {
		for _, secret := range secrets {
			if validateGiteaSignature(secret, payload, giteaSignature) {
				return true
			}
		}
		return false
	}

	return false
}

// handleWebhookPost handles POST requests to the webhook endpoint.
func handleWebhookPost(c *cli.Context, relay payloadRelay, webhookSecrets []string, loggers ...*slog.Logger) http.HandlerFunc {
	logger := slog.Default()
	if len(loggers) > 0 && loggers[0] != nil {
		logger = loggers[0]
	}
	return func(w http.ResponseWriter, r *http.Request) {
		now := time.Now().UTC()
		if !strings.Contains(r.Header.Get("Content-Type"), contentType) {
			http.Error(w, "content-type must be application/json", http.StatusBadRequest)
			return
		}
		channel, valid := requestChannel(r)
		if !valid {
			rejectInvalidChannelRequest(w)
			return
		}
		defer r.Body.Close()

		// Limit request body size to prevent memory exhaustion attacks
		r.Body = http.MaxBytesReader(w, r.Body, int64(c.Int("max-body-size")))
		body, err := io.ReadAll(r.Body)
		if err != nil {
			if strings.Contains(err.Error(), "http: request body too large") {
				http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// Validate webhook signature if secrets are configured
		if len(webhookSecrets) > 0 {
			if !validateWebhookSignature(webhookSecrets, body, r) {
				http.Error(w, "invalid signature", http.StatusUnauthorized)
				return
			}
		}

		var d any
		if err := json.Unmarshal(body, &d); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		payload := make(map[string]any)
		for k, v := range r.Header {
			payload[strings.ToLower(k)] = v[0]
		}
		payload["timestamp"] = fmt.Sprintf("%d", now.UnixMilli())
		payload["bodyB"] = base64.StdEncoding.EncodeToString(body)
		reencoded, err := json.Marshal(payload)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		streamID, err := relay.Publish(r.Context(), channel, reencoded)
		if err != nil {
			http.Error(w, fmt.Sprintf("publish event: %v", err), http.StatusInternalServerError)
			return
		}

		// Add server version to response headers
		w.Header().Set(versionHeaderName, strings.TrimSpace(string(Version)))

		w.WriteHeader(http.StatusAccepted)
		resp := map[string]any{
			"status":  http.StatusAccepted,
			"channel": channel,
			"message": "ok",
			"version": strings.TrimSpace(string(Version)),
		}
		if streamID != "" {
			resp["stream_id"] = streamID
		}
		_ = json.NewEncoder(w).Encode(resp)
		deliveryID, eventType := relayEventMetadata(reencoded)
		logger.LogAttrs(r.Context(), slog.LevelInfo, "webhook published",
			slog.String("request_id", middleware.GetReqID(r.Context())),
			slog.String("channel", channel), slog.String("delivery_id", deliveryID),
			slog.String("event_type", eventType), slog.String("stream_id", streamID),
			slog.Int("body_bytes", len(body)))
	}
}

// handleReplayPost handles POST requests to the replay endpoint.
func handleReplayPost(c *cli.Context, relay payloadRelay) http.HandlerFunc {
	replayToken := c.String("replay-token")
	// Hash the expected token once so the comparison operates on fixed-length
	// digests, avoiding leaking the token length via ConstantTimeCompare's
	// early return on length mismatch.
	expectedTokenHash := sha256.Sum256([]byte(replayToken))

	return func(w http.ResponseWriter, r *http.Request) {
		if replayToken != "" {
			authorizationHeader := r.Header.Get("Authorization")
			if !strings.HasPrefix(authorizationHeader, "Bearer ") {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}

			providedToken := strings.TrimPrefix(authorizationHeader, "Bearer ")
			providedTokenHash := sha256.Sum256([]byte(providedToken))
			if subtle.ConstantTimeCompare(providedTokenHash[:], expectedTokenHash[:]) != 1 {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
		}

		channel, valid := requestChannel(r)
		if !valid {
			rejectInvalidChannelRequest(w)
			return
		}

		now := time.Now().UTC()
		// Limit request body size to prevent memory exhaustion attacks
		r.Body = http.MaxBytesReader(w, r.Body, int64(c.Int("max-body-size")))
		body, err := io.ReadAll(r.Body)
		if err != nil {
			if strings.Contains(err.Error(), "http: request body too large") {
				http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// Create a payload with the same format as the original webhook handler
		payload := make(map[string]any)
		// Add basic headers from the replay request
		for k, v := range r.Header {
			if strings.EqualFold(k, "Authorization") {
				continue
			}
			payload[strings.ToLower(k)] = v[0]
		}
		// Add timestamp and encode the body
		payload["timestamp"] = fmt.Sprintf("%d", now.UnixMilli())
		payload["bodyB"] = base64.StdEncoding.EncodeToString(body)
		payload["content-type"] = contentType // Ensure content-type is set for replay

		// Re-encode the payload to match the expected format
		reencoded, err := json.Marshal(payload)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		streamID, err := relay.Publish(r.Context(), channel, reencoded)
		if err != nil {
			http.Error(w, fmt.Sprintf("publish replay event: %v", err), http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusAccepted)
		if streamID != "" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status":    http.StatusAccepted,
				"channel":   channel,
				"message":   "replayed",
				"stream_id": streamID,
			})
			return
		}
		_, _ = w.Write([]byte("replayed"))
	}
}

// ipRanges represents a collection of IP networks for access control.
type ipRanges struct {
	networks []*net.IPNet
	ips      []net.IP
}

// parseIPRanges parses a list of IP addresses or CIDR ranges.
func parseIPRanges(ranges []string) (*ipRanges, error) {
	result := &ipRanges{}
	for _, r := range ranges {
		if strings.Contains(r, "/") {
			_, ipnet, err := net.ParseCIDR(r)
			if err != nil {
				return nil, fmt.Errorf("invalid CIDR range %q: %w", r, err)
			}
			result.networks = append(result.networks, ipnet)
		} else {
			ip := net.ParseIP(r)
			if ip == nil {
				return nil, fmt.Errorf("invalid IP address %q", r)
			}
			result.ips = append(result.ips, ip)
		}
	}
	return result, nil
}

// contains checks if an IP is in any of the allowed ranges.
func (r *ipRanges) contains(ip net.IP) bool {
	// Check exact IP matches
	if slices.ContainsFunc(r.ips, func(allowedIP net.IP) bool {
		return ip.Equal(allowedIP)
	}) {
		return true
	}

	// Check CIDR ranges
	for _, network := range r.networks {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

// getRealIP gets the real client IP considering X-Forwarded-For and X-Real-IP headers if trusted.
func getRealIP(r *http.Request, trustProxy bool) (net.IP, error) {
	if trustProxy {
		// Try X-Forwarded-For first
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			ips := strings.Split(xff, ",")
			// Get the original client IP (first one)
			clientIP := strings.TrimSpace(ips[0])
			ip := net.ParseIP(clientIP)
			if ip != nil {
				return ip, nil
			}
		}

		// Try X-Real-IP
		if xrip := r.Header.Get("X-Real-IP"); xrip != "" {
			ip := net.ParseIP(strings.TrimSpace(xrip))
			if ip != nil {
				return ip, nil
			}
		}
	}

	// Fall back to RemoteAddr
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		// Try RemoteAddr as-is in case it's just an IP
		ip := net.ParseIP(r.RemoteAddr)
		if ip != nil {
			return ip, nil
		}
		return nil, fmt.Errorf("invalid RemoteAddr %q: %w", r.RemoteAddr, err)
	}

	ip := net.ParseIP(host)
	if ip == nil {
		return nil, fmt.Errorf("invalid IP address %q", host)
	}
	return ip, nil
}

// ipRestrictMiddleware creates middleware that restricts access based on IP address for POST requests.
func ipRestrictMiddleware(allowedRanges *ipRanges, trustProxy bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Only check IP for POST requests
			if r.Method != http.MethodPost {
				next.ServeHTTP(w, r)
				return
			}

			// Skip IP validation if no ranges configured
			if allowedRanges == nil || (len(allowedRanges.networks) == 0 && len(allowedRanges.ips) == 0) {
				next.ServeHTTP(w, r)
				return
			}

			clientIP, err := getRealIP(r, trustProxy)
			if err != nil {
				http.Error(w, "Failed to determine client IP", http.StatusBadRequest)
				return
			}

			if !allowedRanges.contains(clientIP) {
				http.Error(w, fmt.Sprintf("IP address %s not allowed", clientIP), http.StatusForbidden)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func retVersion(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set(versionHeaderName, strings.TrimSpace(string(Version)))
	resp := map[string]string{
		"version": strings.TrimSpace(string(Version)),
	}
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		errorIt(w, nil, http.StatusInternalServerError, err)
	}
}

func authorizeEventSubscriber(w http.ResponseWriter, r *http.Request, protectedChannels *ProtectedChannels) (string, *[32]byte, bool) {
	channel, valid := requestChannel(r)
	if !valid {
		rejectInvalidChannelRequest(w)
		return "", nil, false
	}

	var pubKey *[32]byte
	if protectedChannels.Has(channel) {
		pubKeyValue := r.URL.Query().Get("pubkey")
		if pubKeyValue == "" {
			rejectProtectedChannelRequest(w)
			return "", nil, false
		}

		var err error
		pubKey, err = ParsePublicKey(pubKeyValue)
		if err != nil || !protectedChannels.IsAllowed(channel, pubKey) {
			rejectProtectedChannelRequest(w)
			return "", nil, false
		}
	}

	return channel, pubKey, true
}

func setupSSEHeaders(w http.ResponseWriter, corsOrigin string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	if corsOrigin != "" {
		w.Header().Set("Access-Control-Allow-Origin", corsOrigin)
	}
}

func writeSSEEvent(w http.ResponseWriter, id, eventName string, data []byte) error {
	if id != "" {
		if _, err := fmt.Fprintf(w, "id: %s\n", id); err != nil {
			return err
		}
	}
	if eventName != "" {
		if _, err := fmt.Fprintf(w, "event: %s\n", eventName); err != nil {
			return err
		}
	}
	for _, line := range strings.Split(string(data), "\n") {
		if _, err := fmt.Fprintf(w, "data: %s\n", line); err != nil { //nolint:gosec // text/event-stream payload, not HTML output
			return err
		}
	}
	if _, err := fmt.Fprint(w, "\n"); err != nil {
		return err
	}
	return http.NewResponseController(w).Flush()
}

func writeSSEComment(w http.ResponseWriter, comment string) error {
	if _, err := fmt.Fprintf(w, ": %s\n\n", comment); err != nil {
		return err
	}
	return http.NewResponseController(w).Flush()
}

func encryptRelayEvent(event relayEvent, pubKey *[32]byte) (relayEvent, error) {
	if pubKey == nil {
		return event, nil
	}
	encrypted, err := Encrypt(event.Data, pubKey)
	if err != nil {
		return relayEvent{}, err
	}
	event.Data = encrypted
	return event, nil
}

func parseRedisStreamID(id string) (uint64, uint64, bool) {
	millis, sequence, ok := strings.Cut(id, "-")
	if !ok || millis == "" || sequence == "" {
		return 0, 0, false
	}
	millisValue, err := strconv.ParseUint(millis, 10, 64)
	if err != nil {
		return 0, 0, false
	}
	sequenceValue, err := strconv.ParseUint(sequence, 10, 64)
	if err != nil {
		return 0, 0, false
	}
	return millisValue, sequenceValue, true
}

func isValidRedisStreamID(id string) bool {
	_, _, ok := parseRedisStreamID(id)
	return ok
}

func compareRedisStreamIDs(a, b string) (int, error) {
	aMillis, aSequence, ok := parseRedisStreamID(a)
	if !ok {
		return 0, fmt.Errorf("invalid redis stream id %q", a)
	}
	bMillis, bSequence, ok := parseRedisStreamID(b)
	if !ok {
		return 0, fmt.Errorf("invalid redis stream id %q", b)
	}
	if aMillis < bMillis {
		return -1, nil
	}
	if aMillis > bMillis {
		return 1, nil
	}
	if aSequence < bSequence {
		return -1, nil
	}
	if aSequence > bSequence {
		return 1, nil
	}
	return 0, nil
}

func handleEventsGet(eventBroker *EventBroker, protectedChannels *ProtectedChannels, corsOrigin string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := w.(http.Flusher); !ok {
			http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
			return
		}
		channel, pubKey, ok := authorizeEventSubscriber(w, r, protectedChannels)
		if !ok {
			return
		}

		subscriber := eventBroker.Subscribe(channel, pubKey)
		defer eventBroker.Unsubscribe(channel, subscriber)

		reqID := middleware.GetReqID(r.Context())

		setupSSEHeaders(w, corsOrigin)

		if err := writeSSEEvent(w, "", "", []byte(`{"message":"connected"}`)); err != nil {
			eventBroker.logger.LogAttrs(r.Context(), slog.LevelWarn, "SSE initial write failed",
				slog.String("request_id", reqID), slog.String("channel", channel),
				slog.String("error", err.Error()))
			return
		}
		if err := writeSSEEvent(w, "", "", []byte(`{"message":"ready"}`)); err != nil {
			eventBroker.logger.LogAttrs(r.Context(), slog.LevelWarn, "SSE initial write failed",
				slog.String("request_id", reqID), slog.String("channel", channel),
				slog.String("error", err.Error()))
			return
		}

		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		clientGone := r.Context().Done()

		for {
			select {
			case <-clientGone:
				eventBroker.logger.LogAttrs(r.Context(), slog.LevelDebug, "SSE subscriber disconnected",
					slog.String("request_id", reqID), slog.String("channel", channel))
				return

			case event, ok := <-subscriber.Events:
				if !ok {
					return
				}
				if err := writeSSEEvent(w, event.ID, "", event.Data); err != nil {
					eventBroker.logger.LogAttrs(r.Context(), slog.LevelWarn, "SSE event delivery failed",
						slog.String("request_id", reqID),
						slog.String("channel", channel), slog.String("delivery_id", event.DeliveryID),
						slog.String("stream_id", event.ID), slog.String("event_type", event.EventType),
						slog.String("error", err.Error()))
					return
				}

			case <-ticker.C:
				if err := writeSSEComment(w, "keepalive"); err != nil {
					eventBroker.logger.LogAttrs(r.Context(), slog.LevelWarn, "SSE keepalive write failed",
						slog.String("request_id", reqID), slog.String("channel", channel),
						slog.String("error", err.Error()))
					return
				}
			}
		}
	}
}

func handleRedisEventsGet(redisRelay *redisPayloadRelay, protectedChannels *ProtectedChannels, corsOrigin string, loggers ...*slog.Logger) http.HandlerFunc {
	logger := slog.Default()
	if len(loggers) > 0 && loggers[0] != nil {
		logger = loggers[0]
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := w.(http.Flusher); !ok {
			http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
			return
		}
		channel, pubKey, ok := authorizeEventSubscriber(w, r, protectedChannels)
		if !ok {
			return
		}

		lastEventID := r.Header.Get("Last-Event-ID")
		readAfterID := ""
		if lastEventID != "" {
			if !isValidRedisStreamID(lastEventID) {
				http.Error(w, "invalid Last-Event-ID", http.StatusBadRequest)
				return
			}
			readAfterID = lastEventID
		} else {
			newestID, exists, err := redisRelay.NewestID(r.Context(), channel)
			if err != nil {
				http.Error(w, fmt.Sprintf("read redis stream tail: %v", err), http.StatusInternalServerError)
				return
			}
			if exists {
				readAfterID = newestID
			} else {
				readAfterID = "0-0"
			}
		}

		var gapEvent []byte
		if lastEventID != "" {
			oldestID, exists, err := redisRelay.OldestID(r.Context(), channel)
			if err != nil {
				http.Error(w, fmt.Sprintf("read redis stream history: %v", err), http.StatusInternalServerError)
				return
			}
			if exists {
				cmp, err := compareRedisStreamIDs(lastEventID, oldestID)
				if err != nil {
					http.Error(w, "invalid Last-Event-ID", http.StatusBadRequest)
					return
				}
				if cmp < 0 {
					fmt.Fprintf(os.Stderr, "WARNING: Redis stream history missed for channel %s: requested %s oldest retained %s\n", channel, lastEventID, oldestID) //nolint:gosec // stderr, not web output
					readAfterID = "0-0"
					gapEvent = []byte(fmt.Sprintf(`{"error":"missed_history","requested_id":%q,"oldest_id":%q}`, lastEventID, oldestID))
				}
			}
		}

		reqID := middleware.GetReqID(r.Context())

		setupSSEHeaders(w, corsOrigin)
		if err := writeSSEEvent(w, "", "", []byte(`{"message":"connected"}`)); err != nil {
			logger.LogAttrs(r.Context(), slog.LevelWarn, "Redis SSE initial write failed",
				slog.String("request_id", reqID), slog.String("channel", channel),
				slog.String("error", err.Error()))
			return
		}
		if err := writeSSEEvent(w, "", "", []byte(`{"message":"ready"}`)); err != nil {
			logger.LogAttrs(r.Context(), slog.LevelWarn, "Redis SSE initial write failed",
				slog.String("request_id", reqID), slog.String("channel", channel),
				slog.String("error", err.Error()))
			return
		}
		if len(gapEvent) > 0 {
			if err := writeSSEEvent(w, "", "gosmee-gap", gapEvent); err != nil {
				logger.LogAttrs(r.Context(), slog.LevelWarn, "Redis SSE gap write failed",
					slog.String("request_id", reqID), slog.String("channel", channel),
					slog.String("error", err.Error()))
				return
			}
		}

		for {
			events, err := redisRelay.Read(r.Context(), channel, readAfterID, 30*time.Second, 100)
			if err != nil {
				if r.Context().Err() != nil {
					logger.LogAttrs(r.Context(), slog.LevelDebug, "Redis SSE subscriber disconnected",
						slog.String("request_id", reqID), slog.String("channel", channel))
					return
				}
				logger.LogAttrs(r.Context(), slog.LevelWarn, "Redis stream read failed",
					slog.String("request_id", reqID), slog.String("channel", channel),
					slog.String("error", err.Error()))
				return
			}
			if len(events) == 0 {
				if r.Context().Err() != nil {
					logger.LogAttrs(r.Context(), slog.LevelDebug, "Redis SSE subscriber disconnected",
						slog.String("request_id", reqID), slog.String("channel", channel))
					return
				}
				if err := writeSSEComment(w, "keepalive"); err != nil {
					logger.LogAttrs(r.Context(), slog.LevelWarn, "Redis SSE keepalive write failed",
						slog.String("request_id", reqID), slog.String("channel", channel),
						slog.String("error", err.Error()))
					return
				}
				continue
			}
			for _, event := range events {
				event, err = encryptRelayEvent(event, pubKey)
				if err != nil {
					logger.LogAttrs(r.Context(), slog.LevelWarn, "Redis SSE encryption failed",
						slog.String("request_id", reqID), slog.String("channel", channel),
						slog.String("stream_id", event.ID), slog.String("error", err.Error()))
					continue
				}
				if err := writeSSEEvent(w, event.ID, "", event.Data); err != nil {
					logger.LogAttrs(r.Context(), slog.LevelWarn, "Redis SSE event delivery failed",
						slog.String("request_id", reqID),
						slog.String("channel", channel), slog.String("delivery_id", event.DeliveryID),
						slog.String("stream_id", event.ID), slog.String("event_type", event.EventType),
						slog.String("error", err.Error()))
					return
				}
				readAfterID = event.ID
			}
		}
	}
}

func serve(c *cli.Context) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	publicURL := c.String("public-url")
	corsOrigin := c.String("cors-origin")
	footer := c.String("footer")
	footerFile := c.String("footer-file")
	if footer != "" && footerFile != "" {
		return fmt.Errorf("cannot use both --footer and --footer-file")
	}
	if footerFile != "" {
		b, err := os.ReadFile(footerFile)
		if err != nil {
			return err
		}
		footer = string(b)
	}

	protectedChannels, err := LoadProtectedChannels(c.String("encrypted-channels-file"))
	if err != nil {
		return fmt.Errorf("load protected channels: %w", err)
	}

	// Parse IP restrictions if configured
	var allowedRanges *ipRanges
	if ips := c.StringSlice("allowed-ips"); len(ips) > 0 {
		allowedRanges, err = parseIPRanges(ips)
		if err != nil {
			return fmt.Errorf("failed to parse allowed IPs: %w", err)
		}
	}

	// Initialize the configured logger so server correlation logs honor
	// --output and --log-level / GOSMEE_LOG_LEVEL.
	logger, _, err := getLogger(c)
	if err != nil {
		return err
	}
	slog.SetDefault(logger)

	// Initialize the in-process event broker used by non-Redis mode.
	eventBroker := NewEventBroker(logger)
	localRelay := newLocalPayloadRelay(eventBroker)
	var relay payloadRelay = localRelay
	var redisRelay *redisPayloadRelay
	if redisURL := c.String("redis-url"); redisURL != "" {
		redisRelay, err = newRedisPayloadRelay(ctx, redisURL, int64(c.Int("redis-stream-maxlen")))
		if err != nil {
			return fmt.Errorf("configure redis relay: %w", err)
		}
		defer redisRelay.Close()
		relay = redisRelay
		fmt.Fprintln(os.Stdout, "Using Redis Streams relay")
	}
	autoCert := c.Bool("auto-cert")
	certFile := c.String("tls-cert")
	certKey := c.String("tls-key")
	sslEnabled := certFile != "" && certKey != ""
	portAddr := fmt.Sprintf("%s:%d", c.String("address"), c.Int("port"))
	publicURL = effectivePublicURL(publicURL, portAddr, sslEnabled)

	// Create two separate routers
	mainRouter := chi.NewRouter()       // For unrestricted GET requests
	restrictedRouter := chi.NewRouter() // For restricted POST requests

	// Apply middleware to both routers (but NOT RealIP middleware which would interfere with our custom IP handling)
	mainRouter.Use(middleware.RequestID)
	// Do NOT use middleware.RealIP - it would override our trust-proxy setting
	mainRouter.Use(middleware.Logger)
	mainRouter.Use(middleware.Recoverer)

	restrictedRouter.Use(middleware.RequestID)
	// Do NOT use middleware.RealIP - it would override our trust-proxy setting
	restrictedRouter.Use(middleware.Logger)
	restrictedRouter.Use(middleware.Recoverer)

	// Apply IP restriction middleware ONLY to restricted router
	restrictedRouter.Use(ipRestrictMiddleware(allowedRanges, c.Bool("trust-proxy")))

	// SSE endpoint for event streaming
	var eventsHandler http.HandlerFunc
	if redisRelay != nil {
		eventsHandler = handleRedisEventsGet(redisRelay, protectedChannels, corsOrigin, logger)
	} else {
		eventsHandler = handleEventsGet(eventBroker, protectedChannels, corsOrigin)
	}
	registerMainRoutes(mainRouter, publicURL, footer, protectedChannels, eventsHandler)

	// Register POST routes on the restricted router
	restrictedRouter.Post(channelPath, handleWebhookPost(c, relay, c.StringSlice("webhook-signature"), logger))
	restrictedRouter.Post(replayPath, handleReplayPost(c, relay))

	// Create a final router which will route to the appropriate sub-router
	finalRouter := chi.NewRouter()

	// First mount the restrictedRouter to handle POST requests
	finalRouter.Mount("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			restrictedRouter.ServeHTTP(w, r)
		} else {
			mainRouter.ServeHTTP(w, r)
		}
	}))

	fmt.Fprintf(os.Stdout, "Serving for webhooks on %s\n", publicURL)

	if sslEnabled {
		//nolint:gosec
		return http.ListenAndServeTLS(portAddr, certFile, certKey, finalRouter)
	} else if autoCert {
		//nolint: gosec
		return http.Serve(autocert.NewListener(publicURL), finalRouter)
	}
	//nolint:gosec
	return http.ListenAndServe(portAddr, finalRouter)
}
