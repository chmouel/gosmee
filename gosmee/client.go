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
)

//go:embed templates/version
var Version []byte

//go:embed templates/replay_script.tmpl.bash
var shellScriptTmpl []byte

var pmEventRe = regexp.MustCompile(`(\w+|\d+|_|-|:)`)

const defaultTimeout = 5

const smeeChannel = "messages"

const tsFormat = "2006-01-02T15.04.01.000"

type goSmee struct {
	saveDir, smeeURL, targetURL string
	decorate, noReplay          bool
	ignoreEvents                []string
	channel                     string
	targetCnxTimeout            int
	insecureTLSVerify           bool
	logger                      *slog.Logger
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

	if len(c.ignoreEvents) > 0 && pm.eventType != "" {
		for _, v := range c.ignoreEvents {
			if v == pm.eventType {
				s := fmt.Sprintf("%sskipping event %s as requested", c.emoji("!", "blue+b"), pm.eventType)
				c.logger.Error(s)
				return payloadMsg{}, nil
			}
		}
	}

	return pm, nil
}

func (c goSmee) emoji(emoji, color string) string {
	if !c.decorate {
		return ""
	}
	return ansi.Color(emoji, color) + " "
}

func (c goSmee) saveData(pm payloadMsg) error {
	// check if saveDir is created
	if _, err := os.Stat(c.saveDir); os.IsNotExist(err) {
		if err := os.MkdirAll(c.saveDir, 0o755); err != nil {
			return err
		}
	}

	var fbasepath string
	if pm.eventType != "" {
		fbasepath = fmt.Sprintf("%s-%s", pm.eventType, pm.timestamp)
	} else {
		fbasepath = pm.timestamp
	}

	jsonfile := fmt.Sprintf("%s/%s.json", c.saveDir, fbasepath)
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

	shscript := fmt.Sprintf("%s/%s.sh", c.saveDir, fbasepath)

	c.logger.Info(fmt.Sprintf("%s%s and %s has been saved", c.emoji("⌁", "yellow+b"), shscript, jsonfile))
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
		Headers     string
		TargetURL   string
		ContentType string
		FileBase    string
	}{
		Headers:     headers,
		TargetURL:   c.targetURL,
		ContentType: pm.contentType,
		FileBase:    fbasepath,
	}); err != nil {
		return err
	}

	// set permission
	return os.Chmod(shscript, 0o755)
}

func (c goSmee) replayData(pm payloadMsg) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(c.targetCnxTimeout)*time.Second)
	defer cancel()
	//nolint: gosec
	client := http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: !c.insecureTLSVerify}}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.targetURL, strings.NewReader(string(pm.body)))
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

	msg = fmt.Sprintf("%s %s replayed to %s, status: %s", pm.timestamp, msg, ansi.Color(c.targetURL, "green+ub"), ansi.Color(fmt.Sprintf("%d", resp.StatusCode), "blue+b"))
	if resp.StatusCode > 299 {
		msg = fmt.Sprintf("%s, error: %s", msg, resp.Status)
	}
	s := fmt.Sprintf("%s%s", c.emoji("•", "magenta+b"), msg)
	c.logger.Info(s)
	return nil
}

func (c goSmee) clientSetup() error {
	version := strings.TrimSpace(string(Version))
	s := fmt.Sprintf("%sStarting gosmee version: %s", c.emoji("⇉", "green+b"), version)
	c.logger.Info(s)
	client := sse.NewClient(c.smeeURL, sse.ClientMaxBufferSize(1<<20))
	client.Headers["User-Agent"] = fmt.Sprintf("gosmee/%s", version)
	// this is to get nginx to work
	client.Headers["X-Accel-Buffering"] = "no"
	channel := filepath.Base(c.smeeURL)
	if strings.HasPrefix(c.smeeURL, "https://smee.io") {
		channel = smeeChannel
	}
	err := client.Subscribe(channel, func(msg *sse.Event) {
		now := time.Now().UTC()
		nowStr := now.Format(tsFormat)

		if string(msg.Event) == "ready" || string(msg.Data) == "ready" {
			s := fmt.Sprintf("%s %sForwarding %s to %s", nowStr, c.emoji("✓", "yellow+b"), ansi.Color(c.smeeURL, "green+u"), ansi.Color(c.targetURL, "green+u"))
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
			if c.saveDir != "" {
				err := c.saveData(pm)
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
			if !c.noReplay {
				if err := c.replayData(pm); err != nil {
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
