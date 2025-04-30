package gosmee

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/r3labs/sse/v2"
	"github.com/urfave/cli/v2"
	"golang.org/x/crypto/acme/autocert"
)

// Version header constant.
const (
	timeFormat        = "2006-01-02T15.04.01.000"
	contentType       = "application/json"
	versionHeaderName = "X-Gosmee-Version"
	maxChannelLength  = 64               // Set maximum channel length to prevent DoS attacks
	maxBodySize       = 25 * 1024 * 1024 // 25 MB maximum request body size (we use [GitHub's](https://docs.github.com/en/webhooks/webhook-events-and-payloads#payload-cap) limit)
)

var (
	defaultServerPort    = 3333
	defaultServerAddress = "localhost"
)

//go:embed templates/index.tmpl
var indexTmpl []byte

//go:embed templates/favicon.svg
var faviconSVG []byte

// Subscriber represents a client connection listening for events.
type Subscriber struct {
	Channel string
	Events  chan []byte
}

// EventBroker manages event subscriptions and publications.
type EventBroker struct {
	sync.RWMutex
	subscribers map[string][]*Subscriber
}

// NewEventBroker creates a new event broker.
func NewEventBroker() *EventBroker {
	return &EventBroker{
		subscribers: make(map[string][]*Subscriber),
	}
}

// Subscribe adds a subscriber for a specific channel.
func (eb *EventBroker) Subscribe(channel string) *Subscriber {
	eb.Lock()
	defer eb.Unlock()

	subscriber := &Subscriber{
		Channel: channel,
		Events:  make(chan []byte, 100), // Buffer size to prevent blocking
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
}

// Publish sends an event to all subscribers of a channel.
func (eb *EventBroker) Publish(channel string, data []byte) {
	eb.RLock()
	subscribers := eb.subscribers[channel]
	eb.RUnlock()

	// Send to each subscriber
	for _, s := range subscribers {
		// Non-blocking send - if buffer is full, we'll skip this subscriber
		select {
		case s.Events <- data:
		default:
			// Channel buffer full, could log this if desired
		}
	}
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
		fmt.Fprintf(os.Stdout, "Received request %s %s\n", r.Method, r.URL.Path)
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
func handleWebhookPost(events *sse.Server, eventBroker *EventBroker, webhookSecrets []string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		now := time.Now().UTC()
		if !strings.Contains(r.Header.Get("Content-Type"), contentType) {
			http.Error(w, "content-type must be application/json", http.StatusBadRequest)
			return
		}
		channel := chi.URLParam(r, "channel")
		defer r.Body.Close()

		// Limit request body size to prevent memory exhaustion attacks
		r.Body = http.MaxBytesReader(w, r.Body, maxBodySize)
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
		var headersBuilder strings.Builder
		payload := make(map[string]any)
		for k, v := range r.Header {
			headersBuilder.WriteString(fmt.Sprintf(" %s=%s", k, v[0]))
			payload[strings.ToLower(k)] = v[0]
		}
		payload["timestamp"] = fmt.Sprintf("%d", now.UnixMilli())
		payload["bodyB"] = base64.StdEncoding.EncodeToString(body)
		reencoded, err := json.Marshal(payload)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// Publish to both systems (r3labs/sse for backward compatibility)
		events.CreateStream(channel)
		events.Publish(channel, &sse.Event{Data: reencoded})

		// Publish to our custom event broker for the web UI
		eventBroker.Publish(channel, reencoded)

		// Add server version to response headers
		w.Header().Set(versionHeaderName, strings.TrimSpace(string(Version)))

		w.WriteHeader(http.StatusAccepted)
		resp := map[string]any{
			"status":  http.StatusAccepted,
			"channel": channel,
			"message": "ok",
			"version": strings.TrimSpace(string(Version)),
		}
		_ = json.NewEncoder(w).Encode(resp)
		fmt.Fprintf(os.Stdout, "%s Published %s%s on channel %s\n",
			now.Format(timeFormat),
			middleware.GetReqID(r.Context()),
			headersBuilder.String(),
			channel)
	}
}

// handleReplayPost handles POST requests to the replay endpoint.
func handleReplayPost(events *sse.Server, eventBroker *EventBroker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		channel := chi.URLParam(r, "channel")
		if channel == "" {
			http.Error(w, "Channel name missing in URL", http.StatusBadRequest)
			return
		}

		// Validate channel length
		if len(channel) > maxChannelLength {
			http.Error(w, "Channel name exceeds maximum length", http.StatusBadRequest)
			return
		}

		now := time.Now().UTC()
		// Limit request body size to prevent memory exhaustion attacks
		r.Body = http.MaxBytesReader(w, r.Body, maxBodySize)
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

		// Publish to both systems (for UI and legacy clients)
		events.CreateStream(channel)
		events.Publish(channel, &sse.Event{Data: reencoded})
		eventBroker.Publish(channel, reencoded)

		w.WriteHeader(http.StatusAccepted)
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

func serve(c *cli.Context) error {
	publicURL := c.String("public-url")
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

	// Parse IP restrictions if configured
	var allowedRanges *ipRanges
	if ips := c.StringSlice("allowed-ips"); len(ips) > 0 {
		var err error
		allowedRanges, err = parseIPRanges(ips)
		if err != nil {
			return fmt.Errorf("failed to parse allowed IPs: %w", err)
		}
	}

	// Initialize the SSE server and event broker
	events := sse.New()
	events.AutoReplay = false
	events.AutoStream = true
	eventBroker := NewEventBroker()

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

	// Define handlers
	showNewURL := func(w http.ResponseWriter, _ *http.Request) {
		channel := randomString(12)
		url := fmt.Sprintf("%s/%s", publicURL, channel)
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "%s\n", url)
	}

	serveIndex := func(w http.ResponseWriter, r *http.Request) {
		channel := chi.URLParam(r, "channel")
		if channel == "" {
			channel = randomString(12)
			// Redirect / to /random_channel
			http.Redirect(w, r, fmt.Sprintf("%s/%s", publicURL, channel), http.StatusFound)
			return
		}

		url := fmt.Sprintf("%s/%s", publicURL, channel)
		eventsURL := fmt.Sprintf("/events/%s", channel) // Relative path for EventSource

		w.WriteHeader(http.StatusOK)
		t, err := template.New("index").Parse(string(indexTmpl))
		if err != nil {
			errorIt(w, r, http.StatusInternalServerError, err)
			return
		}
		varmap := map[string]string{
			"URL":       url,
			"EventsURL": eventsURL, // Pass events URL to template
			"Channel":   channel,   // Pass channel name to template
			"Version":   string(Version),
			"Footer":    footer,
		}
		w.Header().Set("Content-Type", "text/html")
		if err := t.ExecuteTemplate(w, "index", varmap); err != nil {
			errorIt(w, r, http.StatusInternalServerError, err)
		}
	}

	// Register all GET routes on the main router
	mainRouter.Get("/favicon.ico", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/svg+xml")
		_, _ = w.Write(faviconSVG)
	})
	mainRouter.Get("/", serveIndex)
	mainRouter.Get("/new", showNewURL)
	mainRouter.Get("/{channel:[a-zA-Z0-9-_]{12,64}}", serveIndex)

	mainRouter.Get("/version", retVersion)
	mainRouter.Get("/health", retVersion)
	mainRouter.Get("/livez", retVersion)

	// SSE endpoint for event streaming
	mainRouter.Get("/events/{channel:[a-zA-Z0-9-_]{12,64}}", func(w http.ResponseWriter, r *http.Request) {
		channel := chi.URLParam(r, "channel")
		if channel == "" {
			http.Error(w, "Channel name missing in URL", http.StatusBadRequest)
			return
		}

		// Validate channel length
		if len(channel) > maxChannelLength {
			http.Error(w, "Channel name exceeds maximum length", http.StatusBadRequest)
			return
		}

		// Set headers for SSE
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")
		w.Header().Set("Access-Control-Allow-Origin", "*")

		// Get the flusher for immediate writes
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
			return
		}

		// Send initial connected message
		fmt.Fprintf(w, "data: %s\n\n", `{"message":"connected"}`)
		flusher.Flush()

		// Subscribe to the channel
		subscriber := eventBroker.Subscribe(channel)
		defer eventBroker.Unsubscribe(channel, subscriber)

		// Send ready message
		fmt.Fprintf(w, "data: %s\n\n", `{"message":"ready"}`)
		flusher.Flush()

		// Start a ticker for keepalive messages
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		// Watch for client disconnection
		clientGone := r.Context().Done()

		// Event loop
		for {
			select {
			case <-clientGone:
				// Client disconnected
				return

			case data, ok := <-subscriber.Events:
				// Check if channel is closed
				if !ok {
					return
				}
				// Send event data to client
				fmt.Fprintf(w, "data: %s\n\n", data)
				flusher.Flush()

			case <-ticker.C:
				// Send keepalive comment
				fmt.Fprint(w, ": keepalive\n\n")
				flusher.Flush()
			}
		}
	})

	// Register POST routes on the restricted router
	restrictedRouter.Post("/{channel:[a-zA-Z0-9-_]{12,64}}", handleWebhookPost(events, eventBroker, c.StringSlice("webhook-signature")))
	restrictedRouter.Post("/replay/{channel:[a-zA-Z0-9-_]{12,64}}", handleReplayPost(events, eventBroker))

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

	// Server setup
	autoCert := c.Bool("auto-cert")
	certFile := c.String("tls-cert")
	certKey := c.String("tls-key")
	sslEnabled := certFile != "" && certKey != ""
	portAddr := fmt.Sprintf("%s:%d", c.String("address"), c.Int("port"))
	if publicURL == "" {
		publicURL = "http://"
		if sslEnabled {
			publicURL = "https://"
		}
		publicURL = fmt.Sprintf("%s%s", publicURL, portAddr)
	}

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
