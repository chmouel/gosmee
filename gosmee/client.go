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

const defaultTimeout = 5

const smeeChannel = "messages"

const defaultLocalDebugURL = "http://localhost:8080"

const tsFormat = "2006-01-02T15.04.01.000"

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
	var message interface{}
	_ = json.Unmarshal(data, &message)
	var payload map[string]interface{}
	err := mapstructure.Decode(message, &payload)
	if err != nil {
		return pm, err
	}
	for payloadKey, payloadValue := range payload {
		if payloadKey == "x-github-event" || payloadKey == "x-gitlab-event" || payloadKey == "x-event-key" {
			if pv, ok := payloadValue.(string); ok {
				pm.headers[title(payloadKey)] = pv
				// github action don't like it
				replace := strings.NewReplacer(":", "-", " ", "_", "/", "_")
				pv = replace.Replace(strings.ToLower(pv))
				// remove all non-alphanumeric characters and don't let directory straversal
				pv = pmEventRe.FindString(pv)
				pm.eventType = pv
			}
			continue
		}
		if payloadKey == "x-github-delivery" {
			if pv, ok := payloadValue.(string); ok {
				pm.headers[title(payloadKey)] = pv
				pm.eventID = pv
			}
			continue
		}
		if strings.HasPrefix(payloadKey, "x-") || payloadKey == "user-agent" {
			if pv, ok := payloadValue.(string); ok {
				/* Remove port number from x-forwarded-for header
					X-Forwarded-For header is added to the outgoing request as
					expected, but it includes the port number, for example:

					X-Forwarded-For: 127.0.0.1:1234

					This is incorrect according to the specification:
					developer.mozilla.org/en-US/docs/Web/HTTP/Headers/X-Forwarded-For

					and since this header is critical for security and spoofing many endpoints
					reject any invalid x-forwarded-for header in the request with "400 bad request"
					as expected.

				  https://github.com/chmouel/gosmee/issues/135
				*/
				if strings.ToLower(payloadKey) == "x-forwarded-for" {
					pv = strings.Split(pv, ":")[0]
				}
				pm.headers[title(payloadKey)] = pv
			}
			continue
		}
		switch payloadKey {
		case "bodyB":
			mb := &messageBody{}
			err := json.NewDecoder(strings.NewReader(string(data))).Decode(mb)
			if err != nil {
				return pm, err
			}
			decoded, err := base64.StdEncoding.DecodeString(string(mb.BodyB))
			if err != nil {
				return pm, err
			}
			pm.body = decoded
		case "body":
			mb := &messageBody{}
			err := json.NewDecoder(strings.NewReader(string(data))).Decode(mb)
			if err != nil {
				return pm, err
			}
			pm.body = mb.Body
		case "content-type":
			if pv, ok := payloadValue.(string); ok {
				pm.contentType = pv
			}
		case "timestamp":
			if pv, ok := payloadValue.(string); ok {
				// timestamp payload value is in milliseconds since the Epoch
				tsInt, err := strconv.ParseInt(pv, 10, 64)
				if err != nil {
					s := fmt.Sprintf("%s cannot convert timestamp to int64, %s", ansi.Color("ERROR", "red+b"), err.Error())
					c.logger.Error(s)
				} else {
					dt = time.Unix(tsInt/int64(1000), (tsInt%int64(1000))*int64(1000000)).UTC()
				}
			}
		}
	}

	pm.timestamp = dt.Format(tsFormat)

	if len(c.replayDataOpts.ignoreEvents) > 0 && pm.eventType != "" {
		for _, v := range c.replayDataOpts.ignoreEvents {
			if v == pm.eventType {
				s := fmt.Sprintf("%sskipping event %s as requested", emoji("!", "blue+b", c.replayDataOpts.decorate), pm.eventType)
				c.logger.Error(s)
				return payloadMsg{}, nil
			}
		}
	}

	return pm, nil
}

func emoji(emoji, color string, decorate bool) string {
	if !decorate {
		return ""
	}
	return ansi.Color(emoji, color) + " "
}

func saveData(rd *replayDataOpts, logger *slog.Logger, pm payloadMsg) error {
	// check if saveDir is created
	if _, err := os.Stat(rd.saveDir); os.IsNotExist(err) {
		if err := os.MkdirAll(rd.saveDir, 0o755); err != nil {
			return err
		}
	}

	var fbasepath string
	if pm.eventType != "" {
		fbasepath = fmt.Sprintf("%s-%s", pm.eventType, pm.timestamp)
	} else {
		fbasepath = pm.timestamp
	}

	jsonfile := fmt.Sprintf("%s/%s.json", rd.saveDir, fbasepath)
	f, err := os.Create(jsonfile)
	if err != nil {
		return err
	}
	defer f.Close()
	// write data
	_, err = f.Write(pm.body)
	if err != nil {
		return err
	}

	shscript := fmt.Sprintf("%s/%s.sh", rd.saveDir, fbasepath)

	logger.Info(fmt.Sprintf("%s%s and %s has been saved", emoji("⌁", "yellow+b", rd.decorate), shscript, jsonfile))
	s, err := os.Create(shscript)
	if err != nil {
		return err
	}
	defer s.Close()
	headers := ""
	for k, v := range pm.headers {
		headers += fmt.Sprintf("-H '%s: %s' ", k, v)
	}

	// parse shellScriptTmpl as template with arguments
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

	// set permission
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
	//nolint: gosec
	client := http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: !ropts.insecureTLSVerify}}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ropts.targetURL, strings.NewReader(string(pm.body)))
	if err != nil {
		return err
	}
	for k, v := range pm.headers {
		req.Header.Add(k, v)
	}
	// add content-type if it's not already set
	if _, ok := pm.headers["Content-Type"]; !ok {
		req.Header.Add("Content-Type", pm.contentType)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	// read resp.Body
	defer resp.Body.Close()

	var msg string
	if pm.eventType != "" {
		msg = fmt.Sprintf("%s event", pm.eventType)
	} else {
		msg = "request"
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
	client := sse.NewClient(c.replayDataOpts.smeeURL, sse.ClientMaxBufferSize(1<<20))
	client.Headers["User-Agent"] = fmt.Sprintf("gosmee/%s", version)
	// this is to get nginx to work
	client.Headers["X-Accel-Buffering"] = "no"
	channel := filepath.Base(c.replayDataOpts.smeeURL)
	if strings.HasPrefix(c.replayDataOpts.smeeURL, "https://smee.io") {
		channel = smeeChannel
	}
	err := client.Subscribe(channel, func(msg *sse.Event) {
		now := time.Now().UTC()
		nowStr := now.Format(tsFormat)

		if string(msg.Event) == "ready" || string(msg.Data) == "ready" {
			s := fmt.Sprintf("%s %sForwarding %s to %s", nowStr, emoji("✓", "yellow+b", c.replayDataOpts.decorate), ansi.Color(c.replayDataOpts.smeeURL, "green+u"), ansi.Color(c.replayDataOpts.targetURL, "green+u"))
			c.logger.Info(s)
			return
		}

		if string(msg.Event) == "ping" {
			return
		}

		pm, err := c.parse(now, msg.Data)
		if err != nil {
			s := fmt.Sprintf("%s %s parsing message %s",
				nowStr,
				ansi.Color("ERROR", "red+b"),
				err.Error())
			c.logger.Error(s)
			return
		}
		if len(pm.headers) == 0 {
			s := fmt.Sprintf("%s %s no headers found in message",
				nowStr,
				ansi.Color("ERROR", "red+b"))
			c.logger.Error(s)
			return
		}
		headers := ""
		for k, v := range pm.headers {
			headers += fmt.Sprintf("%s=%s ", k, v)
		}

		if string(msg.Data) != "{}" {
			if c.replayDataOpts.saveDir != "" {
				err := saveData(c.replayDataOpts, c.logger, pm)
				if err != nil {
					s := fmt.Sprintf("%s %s saving message with headers '%s' - %s",
						nowStr,
						ansi.Color("ERROR", "red+b"),
						headers,
						err.Error())
					c.logger.Error(s)
					return
				}
			}
			if !c.replayDataOpts.noReplay {
				if err := replayData(c.replayDataOpts, c.logger, pm); err != nil {
					s := fmt.Sprintf("%s %s forwarding message with headers '%s' - %s",
						nowStr,
						ansi.Color("ERROR", "red+b"),
						headers,
						err.Error())
					c.logger.Error(s)
					return
				}
			}
		}
	})
	return err
}
