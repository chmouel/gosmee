package gosmee

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"text/template"
	"time"

	_ "embed"

	"github.com/mgutz/ansi"
	"github.com/mitchellh/mapstructure"
	"github.com/r3labs/sse/v2"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
	"gopkg.in/cenkalti/backoff.v1"
)

//go:embed templates/version
var Version []byte

//go:embed templates/replay_script.tmpl.bash
var shellScriptTmpl []byte

//go:embed templates/replay_script.tmpl.httpie.bash
var shellScriptHttpieTmpl []byte

var pmEventRe = regexp.MustCompile(`(\w+|\d+|_|-|:)`)

const (
	defaultTimeout       = 5
	smeeChannel          = "messages"
	defaultLocalDebugURL = "http://localhost:8080"
	tsFormat             = "2006-01-02T15.04.01.000"
)

type goSmee struct {
	replayDataOpts *replayDataOpts
	channel        string
	logger         *slog.Logger
}

type payloadMsg struct {
	headers     map[string]string
	body        []byte
	timestamp   string
	contentType string
	eventType   string
	eventID     string
}

type messageBody struct {
	Body  json.RawMessage `json:"body"`
	BodyB string          `json:"bodyB"`
}

// title returns a copy of the string s with all Unicode letters that begin words
// mapped to their Unicode title case.
func title(source string) string {
	return cases.Title(language.Und, cases.NoLower).String(source)
}

func (c goSmee) parse(now time.Time, data []byte) (payloadMsg, error) {
	dt := now
	pm := payloadMsg{
		headers: make(map[string]string),
	}
	pm.eventID = ""
	var message any
	_ = json.Unmarshal(data, &message)
	var payload map[string]any
	err := mapstructure.Decode(message, &payload)
	if err != nil {
		return payloadMsg{}, err
	}

	// Debug: Log the raw payload keys we received
	keys := make([]string, 0, len(payload)) // pre-allocate for performance (prealloc)
	for k := range payload {
		keys = append(keys, k)
	}
	c.logger.DebugContext(context.Background(), fmt.Sprintf("Received payload with keys: %v", keys))

	for payloadKey, payloadValue := range payload {
		switch payloadKey {
		case "x-github-event", "x-gitlab-event", "x-event-key":
			if pv, ok := payloadValue.(string); ok {
				pm.headers[title(payloadKey)] = pv
				replace := strings.NewReplacer(":", "-", " ", "_", "/", "_")
				pv = replace.Replace(strings.ToLower(pv))
				pv = pmEventRe.FindString(pv)
				pm.eventType = pv
			}
		case "x-github-delivery":
			if pv, ok := payloadValue.(string); ok {
				pm.headers[title(payloadKey)] = pv
				pm.eventID = pv
			}
		case "bodyB":
			mb := &messageBody{}
			if err := json.NewDecoder(strings.NewReader(string(data))).Decode(mb); err != nil {
				return pm, err
			}
			decoded, err := base64.StdEncoding.DecodeString(string(mb.BodyB))
			if err != nil {
				return pm, err
			}
			pm.body = decoded
		case "body":
			mb := &messageBody{}
			if err := json.NewDecoder(strings.NewReader(string(data))).Decode(mb); err != nil {
				return pm, err
			}
			pm.body = mb.Body
		case "content-type":
			if pv, ok := payloadValue.(string); ok {
				pm.contentType = pv
				// Also add content-type as a header if it wasn't added already
				if _, exists := pm.headers["Content-Type"]; !exists {
					pm.headers["Content-Type"] = pv
				}
			}
		case "timestamp":
			if pv, ok := payloadValue.(string); ok {
				tsInt, err := strconv.ParseInt(pv, 10, 64)
				if err != nil {
					s := fmt.Sprintf("%s cannot convert timestamp to int64, %s", ansi.Color("ERROR", "red+b"), err.Error())
					c.logger.ErrorContext(context.Background(), s)
				} else {
					dt = time.Unix(tsInt/int64(1000), (tsInt%int64(1000))*int64(1000000)).UTC()
				}
			}
		default:
			// Handle headers with prefix "x-" or specific keys
			if strings.HasPrefix(payloadKey, "x-") || payloadKey == "user-agent" {
				if pv, ok := payloadValue.(string); ok {
					if strings.ToLower(payloadKey) == "x-forwarded-for" {
						pv = strings.Split(pv, ":")[0]
					}
					pm.headers[title(payloadKey)] = pv
				}
			} else if payloadKey != "bodyB" && payloadKey != "body" && payloadValue != nil {
				// For any other field that's not already handled and has a value,
				// consider it as a potential header
				if pv, ok := payloadValue.(string); ok {
					pm.headers[title(payloadKey)] = pv
				}
			}
		}
	}

	pm.timestamp = dt.Format(tsFormat)

	// If there are no headers but we have content-type, ensure at least that header exists
	if len(pm.headers) == 0 && pm.contentType != "" {
		pm.headers["Content-Type"] = pm.contentType
	}

	if len(c.replayDataOpts.ignoreEvents) > 0 &&
		pm.eventType != "" &&
		slices.Contains(c.replayDataOpts.ignoreEvents, pm.eventType) {
		s := fmt.Sprintf("%sskipping event %s as requested", emoji("!", "blue+b", c.replayDataOpts.decorate), pm.eventType)
		c.logger.InfoContext(context.Background(), s)
		return pm, nil
	}

	if len(pm.headers) == 0 && len(pm.body) == 0 {
		return pm, fmt.Errorf("parsed message has no headers")
	}

	return pm, nil
}

func emoji(emoji, color string, decorate bool) string {
	if !decorate {
		return ""
	}
	return ansi.Color(emoji, color) + " "
}

func buildHeaders(headers map[string]string) string {
	var b strings.Builder
	for k, v := range headers {
		b.WriteString(fmt.Sprintf("%s=%s ", k, v))
	}
	return b.String()
}

func buildCurlHeaders(headers map[string]string) string {
	var b strings.Builder
	for k, v := range headers {
		b.WriteString(fmt.Sprintf("-H '%s: %s' ", k, v))
	}
	return b.String()
}

func saveData(rd *replayDataOpts, logger *slog.Logger, pm payloadMsg) error {
	if _, err := os.Stat(rd.saveDir); os.IsNotExist(err) {
		if err := os.MkdirAll(rd.saveDir, 0o755); err != nil {
			return err
		}
	}

	fbasepath := pm.timestamp
	if pm.eventType != "" {
		fbasepath = fmt.Sprintf("%s-%s", pm.eventType, pm.timestamp)
	}

	jsonfile := fmt.Sprintf("%s/%s.json", rd.saveDir, fbasepath)
	f, err := os.Create(jsonfile)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err = f.Write(pm.body); err != nil {
		return err
	}

	shscript := fmt.Sprintf("%s/%s.sh", rd.saveDir, fbasepath)
	logger.InfoContext(context.Background(), fmt.Sprintf("%s%s and %s has been saved", emoji("⌁", "yellow+b", rd.decorate), shscript, jsonfile))
	s, err := os.Create(shscript)
	if err != nil {
		return err
	}
	defer s.Close()

	var tmpl *template.Template
	var headers string
	if rd.useHttpie {
		tmpl = template.Must(template.New("shellScriptTmplHttpie").Parse(string(shellScriptHttpieTmpl)))
		headers = buildHttpieHeaders(pm.headers)
	} else {
		tmpl = template.Must(template.New("shellScriptTmpl").Parse(string(shellScriptTmpl)))
		headers = buildCurlHeaders(pm.headers)
	}

	if err := tmpl.Execute(s, struct {
		Headers       string
		TargetURL     string
		ContentType   string
		FileBase      string
		LocalDebugURL string
	}{
		Headers:       headers,
		TargetURL:     rd.targetURL,
		LocalDebugURL: rd.localDebugURL,
		ContentType:   pm.contentType,
		FileBase:      fbasepath,
	}); err != nil {
		return err
	}
	return os.Chmod(shscript, 0o755)
}

// buildHttpieHeaders builds httpie header arguments from a map.
func buildHttpieHeaders(headers map[string]string) string {
	var b strings.Builder
	for k, v := range headers {
		// HTTPie expects headers in format 'Header-Name:value' and needs proper quoting
		b.WriteString(fmt.Sprintf("%s:%s ", strconv.Quote(k), strconv.Quote(v)))
	}
	return b.String()
}

type replayDataOpts struct {
	insecureTLSVerify           bool
	targetCnxTimeout            int
	decorate, noReplay          bool
	saveDir, smeeURL, targetURL string
	localDebugURL               string
	ignoreEvents                []string
	useHttpie                   bool // Use httpie instead of curl
}

func replayData(ropts *replayDataOpts, logger *slog.Logger, pm payloadMsg) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(ropts.targetCnxTimeout)*time.Second)
	defer cancel()
	//nolint:gosec // InsecureSkipVerify is controlled by user input for testing/self-signed certs
	client := http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: !ropts.insecureTLSVerify}}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ropts.targetURL, strings.NewReader(string(pm.body)))
	if err != nil {
		return err
	}
	for k, v := range pm.headers {
		req.Header.Add(k, v)
	}
	if _, ok := pm.headers["Content-Type"]; !ok {
		req.Header.Add("Content-Type", pm.contentType)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	msg := "request"
	if pm.eventType != "" {
		msg = fmt.Sprintf("%s event", pm.eventType)
	}
	if pm.eventID != "" {
		msg = fmt.Sprintf("%s %s", pm.eventID, msg)
	}
	msg = fmt.Sprintf("%s %s replayed to %s, status: %s", pm.timestamp, msg, ansi.Color(ropts.targetURL, "green+ub"), ansi.Color(fmt.Sprintf("%d", resp.StatusCode), "blue+b"))
	if resp.StatusCode > 299 {
		msg = fmt.Sprintf("%s, error: %s", msg, resp.Status)
	}
	s := fmt.Sprintf("%s%s", emoji("•", "magenta+b", ropts.decorate), msg)
	logger.InfoContext(context.Background(), s)
	return nil
}

// checkServerVersion verifies that the client version is compatible with the server version.
func checkServerVersion(serverURL string, clientVersion string, logger *slog.Logger, decorate bool) error {
	// Extract base URL from the smeeURL (removing the channel part)
	baseURL := serverURL
	if parts := strings.Split(serverURL, "/"); len(parts) > 3 {
		// Reconstruct the base URL (scheme + host)
		baseURL = strings.Join(parts[0:3], "/")
	}

	// Create a request to the version endpoint
	versionURL := fmt.Sprintf("%s/version", baseURL)
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(defaultTimeout)*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, versionURL, nil)
	if err != nil {
		// If we can't create the request, don't fail - just warn
		logger.WarnContext(context.Background(), fmt.Sprintf("%sCould not create version check request: %s", emoji("⚠", "yellow+b", decorate), err.Error()))
		return nil
	}

	client := http.Client{Timeout: time.Duration(defaultTimeout) * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		// If we can't reach the server, don't fail - just warn
		logger.WarnContext(context.Background(), fmt.Sprintf("%sCould not check server version: %s", emoji("⚠", "yellow+b", decorate), err.Error()))
		return nil
	}
	defer resp.Body.Close()

	// Check if the server returned a 404 Not Found status for the version endpoint
	if resp.StatusCode == http.StatusNotFound {
		errMsg := fmt.Sprintf("%sThe server appears to be too old and doesn't support version checking. Please upgrade the server or use an older client version.",
			emoji("⛔", "red+b", decorate))
		logger.ErrorContext(context.Background(), errMsg)
		return fmt.Errorf("%s", errMsg)
	}

	// Check for other non-200 status codes
	if resp.StatusCode != http.StatusOK {
		logger.WarnContext(context.Background(), fmt.Sprintf("%sServer returned unexpected status code %d when checking version",
			emoji("⚠", "yellow+b", decorate), resp.StatusCode))
		return nil
	}

	// Check for version in header first
	serverVersion := resp.Header.Get("X-Gosmee-Version")

	// If no header, try to parse from body
	if serverVersion == "" {
		var versionResp struct {
			Version string `json:"version"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&versionResp); err != nil {
			logger.WarnContext(context.Background(), fmt.Sprintf("%sCould not parse server version: %s", emoji("⚠", "yellow+b", decorate), err.Error()))
			return nil
		}
		serverVersion = versionResp.Version
	}

	// Compare versions
	if serverVersion != "" {
		if serverVersion == clientVersion {
			logger.DebugContext(context.Background(), fmt.Sprintf("Version match: client and server both at version %s", serverVersion))
		} else {
			// Check for development versions - only warn, don't fail
			if serverVersion == "dev" || clientVersion == "dev" {
				logger.WarnContext(context.Background(), fmt.Sprintf("%sVersion mismatch with development version: client %s, server %s",
					emoji("⚠", "yellow+b", decorate),
					ansi.Color(clientVersion, "blue+b"),
					ansi.Color(serverVersion, "blue+b")))
			} else {
				// Parse server and client versions for comparison
				serverParts := parseVersion(serverVersion)
				clientParts := parseVersion(clientVersion)

				// Compare major and minor version parts
				isClientOutdated := isOlderVersion(clientParts, serverParts)

				if isClientOutdated {
					errMsg := fmt.Sprintf("%sClient version %s is too old. Server version is %s. Please upgrade your gosmee client.",
						emoji("⛔", "red+b", decorate),
						ansi.Color(clientVersion, "blue+b"),
						ansi.Color(serverVersion, "blue+b"))
					logger.ErrorContext(context.Background(), errMsg)
					return fmt.Errorf("%s", errMsg)
				}
				// Client is same or newer, just warn
				logger.WarnContext(context.Background(), fmt.Sprintf("%sVersion mismatch: client %s, server %s",
					emoji("⚠", "yellow+b", decorate),
					ansi.Color(clientVersion, "blue+b"),
					ansi.Color(serverVersion, "blue+b")))
			}
		}
	}

	logger.InfoContext(context.Background(), fmt.Sprintf("%sServer version: %s", emoji("✓", "green+b", decorate), serverVersion))
	return nil
}

// parseVersion splits a version string like "1.2.3" into []int{1, 2, 3}.
func parseVersion(version string) []int {
	version = strings.TrimPrefix(version, "v")
	parts := strings.Split(version, ".")
	result := make([]int, 0, len(parts))

	for _, part := range parts {
		// Remove any non-numeric suffixes like "-alpha", "-beta", etc.
		for i, c := range part {
			if c < '0' || c > '9' {
				part = part[:i]
				break
			}
		}

		num, err := strconv.Atoi(part)
		if err != nil {
			num = 0
		}
		result = append(result, num)
	}

	// Ensure we have at least 3 elements (major, minor, patch)
	for len(result) < 3 {
		result = append(result, 0)
	}

	return result
}

// isOlderVersion returns true if v1 is older than v2.
func isOlderVersion(v1, v2 []int) bool {
	minLen := min(len(v1), len(v2))

	for i := 0; i < minLen; i++ {
		if v1[i] < v2[i] {
			return true
		} else if v1[i] > v2[i] {
			return false
		}
	}

	// If all compared parts are equal, check if v2 has more specific version
	return len(v1) < len(v2)
}

func (c goSmee) clientSetup() error {
	version := strings.TrimSpace(string(Version))
	s := fmt.Sprintf("%sStarting gosmee client version: %s", emoji("⇉", "green+b", c.replayDataOpts.decorate), version)
	c.logger.InfoContext(context.Background(), s)

	// Check server version compatibility
	if err := checkServerVersion(c.replayDataOpts.smeeURL, version, c.logger, c.replayDataOpts.decorate); err != nil {
		c.logger.WarnContext(context.Background(), fmt.Sprintf("%sCould not get server version: %s", emoji("⚠", "yellow+b", c.replayDataOpts.decorate), err.Error()))
	}

	// Extract the base URL and channel from the smeeURL
	channel := filepath.Base(c.replayDataOpts.smeeURL)
	baseURL := strings.TrimSuffix(c.replayDataOpts.smeeURL, "/"+channel)

	// Special case for smee.io
	var sseURL string
	if strings.HasPrefix(c.replayDataOpts.smeeURL, "https://smee.io") {
		channel = smeeChannel
		sseURL = c.replayDataOpts.smeeURL
	} else {
		// For our own server, connect to the /events/{channel} endpoint
		sseURL = fmt.Sprintf("%s/events/%s", baseURL, channel)
	}

	client := sse.NewClient(sseURL, sse.ClientMaxBufferSize(1<<20))
	// Set up a custom exponential backoff strategy that never stops retrying
	// By default, ExponentialBackOff gives up after 15 minutes, which can cause
	// the client to get stuck. Setting MaxElapsedTime to 0 makes it retry forever.
	expBackoff := backoff.NewExponentialBackOff()
	expBackoff.MaxElapsedTime = 0 // Setting this to 0 means it will retry forever
	client.ReconnectStrategy = expBackoff
	c.logger.InfoContext(context.Background(), fmt.Sprintf("%sConfigured reconnection strategy to retry indefinitely", emoji("⇉", "blue+b", c.replayDataOpts.decorate)))
	client.Headers["User-Agent"] = fmt.Sprintf("gosmee/%s", version)
	client.Headers["X-Accel-Buffering"] = "no"

	return client.Subscribe(channel, func(msg *sse.Event) {
		now := time.Now().UTC()
		nowStr := now.Format(tsFormat)

		// Check for explicit ready messages from SSE events
		if string(msg.Event) == "ready" || string(msg.Data) == "ready" {
			s := fmt.Sprintf("%s %sForwarding %s to %s", nowStr, emoji("✓", "yellow+b", c.replayDataOpts.decorate), ansi.Color(c.replayDataOpts.smeeURL, "green+u"), ansi.Color(c.replayDataOpts.targetURL, "green+u"))
			c.logger.InfoContext(context.Background(), s)
			return
		}

		// Skip ping events
		if string(msg.Event) == "ping" {
			return
		}

		// Check for empty data
		if len(msg.Data) == 0 || string(msg.Data) == "{}" {
			return
		}

		// Check if the message data contains a ready indicator or connected message
		if strings.Contains(strings.ToLower(string(msg.Data)), "ready") ||
			strings.Contains(strings.ToLower(string(msg.Data)), "\"message\"") &&
				strings.Contains(strings.ToLower(string(msg.Data)), "\"connected\"") {
			c.logger.DebugContext(context.Background(), fmt.Sprintf("%s Skipping connection message", nowStr))
			return
		}

		pm, err := c.parse(now, msg.Data)
		if err != nil {
			s := fmt.Sprintf("%s %s parsing message %s", nowStr, ansi.Color("ERROR", "red+b"), err.Error())
			c.logger.ErrorContext(context.Background(), s)
			return
		}

		// Check if this looks like a ready message or connected message based on specific patterns
		if pm.eventType == "ready" || ((len(pm.body) > 0) && strings.ToLower(string(pm.body)) == "ready") {
			c.logger.DebugContext(context.Background(), fmt.Sprintf("%s Skipping message with 'ready' in event type or body", nowStr))
			return
		}

		// Check for empty body messages with "Message: connected" header
		if len(pm.body) == 0 {
			for k, v := range pm.headers {
				if strings.EqualFold(k, "Message") && strings.EqualFold(v, "connected") {
					c.logger.DebugContext(context.Background(), fmt.Sprintf("%s Skipping empty message with Message: connected header", nowStr))
					return
				}
			}
		}

		if len(pm.headers) == 0 {
			s := fmt.Sprintf("%s %s no headers found in message", nowStr, ansi.Color("ERROR", "red+b"))
			c.logger.ErrorContext(context.Background(), s)
			return
		}

		headers := buildHeaders(pm.headers)
		if c.replayDataOpts.saveDir != "" {
			if err := saveData(c.replayDataOpts, c.logger, pm); err != nil {
				s := fmt.Sprintf("%s %s saving message with headers '%s' - %s", nowStr, ansi.Color("ERROR", "red+b"), headers, err.Error())
				c.logger.ErrorContext(context.Background(), s)
				return
			}
		}

		if !c.replayDataOpts.noReplay {
			if err := replayData(c.replayDataOpts, c.logger, pm); err != nil {
				s := fmt.Sprintf("%s %s forwarding message with headers '%s' - %s", nowStr, ansi.Color("ERROR", "red+b"), headers, err.Error())
				c.logger.ErrorContext(context.Background(), s)
				return
			}
		}
	})
}

func serveHealthEndpoint(port int, logger *slog.Logger, decorate bool) {
	if port <= 0 {
		return // Health server disabled
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", retVersion)

	addr := fmt.Sprintf(":%d", port)
	server := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	logger.InfoContext(context.Background(), fmt.Sprintf("%sStarting health server on %s", emoji("✓", "green+b", decorate), addr))

	// Run the health server in a separate goroutine
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.ErrorContext(context.Background(), fmt.Sprintf("%sHealth server error: %s", emoji("⛔", "red+b", decorate), err.Error()))
		}
	}()
}
