package gosmee

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
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
	"golang.org/x/exp/slog"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

//go:embed templates/version
var Version []byte

//go:embed templates/replay_script.tmpl.bash
var shellScriptTmpl []byte

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
		return pm, err
	}

	// Debug: Log the raw payload keys we received
	keys := make([]string, 0, len(payload)) // pre-allocate for performance (prealloc)
	for k := range payload {
		keys = append(keys, k)
	}
	c.logger.Debug(fmt.Sprintf("Received payload with keys: %v", keys))

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
					c.logger.Error(s)
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
		c.logger.Info(s) // Changed to Info since this is not an error
		return payloadMsg{}, nil
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
	logger.Info(fmt.Sprintf("%s%s and %s has been saved", emoji("⌁", "yellow+b", rd.decorate), shscript, jsonfile))
	s, err := os.Create(shscript)
	if err != nil {
		return err
	}
	defer s.Close()
	headers := buildCurlHeaders(pm.headers)
	t := template.Must(template.New("shellScriptTmpl").Parse(string(shellScriptTmpl)))
	if err := t.Execute(s, struct {
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

type replayDataOpts struct {
	insecureTLSVerify           bool
	targetCnxTimeout            int
	decorate, noReplay          bool
	saveDir, smeeURL, targetURL string
	localDebugURL               string
	ignoreEvents                []string
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
	logger.Info(s)
	return nil
}

func (c goSmee) clientSetup() error {
	version := strings.TrimSpace(string(Version))
	s := fmt.Sprintf("%sStarting gosmee version: %s\n", emoji("⇉", "green+b", c.replayDataOpts.decorate), version)
	c.logger.Info(s)

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
	client.Headers["User-Agent"] = fmt.Sprintf("gosmee/%s", version)
	client.Headers["X-Accel-Buffering"] = "no"

	return client.Subscribe(channel, func(msg *sse.Event) {
		now := time.Now().UTC()
		nowStr := now.Format(tsFormat)

		// Check for explicit ready messages from SSE events
		if string(msg.Event) == "ready" || string(msg.Data) == "ready" {
			s := fmt.Sprintf("%s %sForwarding %s to %s", nowStr, emoji("✓", "yellow+b", c.replayDataOpts.decorate), ansi.Color(c.replayDataOpts.smeeURL, "green+u"), ansi.Color(c.replayDataOpts.targetURL, "green+u"))
			c.logger.Info(s)
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
			c.logger.Debug(fmt.Sprintf("%s Skipping connection message", nowStr))
			return
		}

		pm, err := c.parse(now, msg.Data)
		if err != nil {
			s := fmt.Sprintf("%s %s parsing message %s", nowStr, ansi.Color("ERROR", "red+b"), err.Error())
			c.logger.Error(s)
			return
		}

		// Check if this looks like a ready message or connected message based on specific patterns
		if pm.eventType == "ready" || (len(pm.body) > 0 && strings.Contains(strings.ToLower(string(pm.body)), "ready")) {
			c.logger.Debug(fmt.Sprintf("%s Skipping message with 'ready' in event type or body", nowStr))
			return
		}

		// Check for empty body messages with "Message: connected" header
		if len(pm.body) == 0 {
			for k, v := range pm.headers {
				if strings.EqualFold(k, "Message") && strings.EqualFold(v, "connected") {
					c.logger.Debug(fmt.Sprintf("%s Skipping empty message with Message: connected header", nowStr))
					return
				}
			}
		}

		if len(pm.headers) == 0 {
			s := fmt.Sprintf("%s %s no headers found in message", nowStr, ansi.Color("ERROR", "red+b"))
			c.logger.Error(s)
			return
		}

		headers := buildHeaders(pm.headers)
		if c.replayDataOpts.saveDir != "" {
			if err := saveData(c.replayDataOpts, c.logger, pm); err != nil {
				s := fmt.Sprintf("%s %s saving message with headers '%s' - %s", nowStr, ansi.Color("ERROR", "red+b"), headers, err.Error())
				c.logger.Error(s)
				return
			}
		}

		if !c.replayDataOpts.noReplay {
			if err := replayData(c.replayDataOpts, c.logger, pm); err != nil {
				s := fmt.Sprintf("%s %s forwarding message with headers '%s' - %s", nowStr, ansi.Color("ERROR", "red+b"), headers, err.Error())
				c.logger.Error(s)
				return
			}
		}
	})
}
