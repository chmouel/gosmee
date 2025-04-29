package gosmee

import (
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
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
	maxChannelLength  = 64 // Set maximum channel length to prevent DoS attacks
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

// handleWebhookPost handles POST requests to the webhook endpoint.
func handleWebhookPost(events *sse.Server, eventBroker *EventBroker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		now := time.Now().UTC()
		if !strings.Contains(r.Header.Get("Content-Type"), contentType) {
			http.Error(w, "content-type must be application/json", http.StatusBadRequest)
			return
		}
		channel := chi.URLParam(r, "channel")
		defer r.Body.Close()
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
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
	router := chi.NewRouter()
	router.Use(middleware.RequestID)
	router.Use(middleware.RealIP)
	router.Use(middleware.Logger)
	router.Use(middleware.Recoverer)

	// Initialize the SSE server (for backward compatibility)
	events := sse.New()
	events.AutoReplay = false
	events.AutoStream = true

	// Initialize our custom event broker for the web UI
	eventBroker := NewEventBroker()

	showNewURL := func(w http.ResponseWriter, _ *http.Request) {
		channel := randomString(12)
		url := fmt.Sprintf("%s/%s", publicURL, channel)
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "%s\n", url)
	}
	// Serve the main UI page
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

	// Serve favicon
	router.Get("/favicon.ico", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/svg+xml")
		_, _ = w.Write(faviconSVG)
	})
	router.Get("/", serveIndex)
	router.Get("/new", showNewURL) // Redirects to /random_channel via serveIndex
	router.Get("/{channel:[a-zA-Z0-9-_]{12,64}}", serveIndex)

	// Dedicated endpoint for SSE events
	router.Get("/events/{channel:[a-zA-Z0-9-_]{12,64}}", func(w http.ResponseWriter, r *http.Request) {
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

	router.Post("/{channel:[a-zA-Z0-9-_]{12,64}}", func(w http.ResponseWriter, r *http.Request) {
		channel := chi.URLParam(r, "channel")

		// Validate channel length
		if len(channel) > maxChannelLength {
			http.Error(w, "Channel name exceeds maximum length", http.StatusBadRequest)
			return
		}

		handleWebhookPost(events, eventBroker)(w, r)
	})

	// Add version endpoint to allow version checking
	router.Get("/version", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set(versionHeaderName, strings.TrimSpace(string(Version)))
		resp := map[string]string{
			"version": strings.TrimSpace(string(Version)),
		}
		_ = json.NewEncoder(w).Encode(resp)
	})

	// Add a replay endpoint to allow replaying events from the UI
	router.Post("/replay/{channel:[a-zA-Z0-9-_]{12,64}}", func(w http.ResponseWriter, r *http.Request) {
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
		body, err := io.ReadAll(r.Body)
		if err != nil {
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
		payload["content-type"] = contentType

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
	})

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
		return http.ListenAndServeTLS(portAddr, certFile, certKey, router)
	} else if autoCert {
		//nolint: gosec
		return http.Serve(autocert.NewListener(publicURL), router)
	}
	//nolint:gosec
	return http.ListenAndServe(portAddr, router)
}
