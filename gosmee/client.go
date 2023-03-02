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

type goSmee struct {
	saveDir, smeeURL, targetURL string
	decorate, noReplay          bool
	ignoreEvents                []string
	channel                     string
	targetCnxTimeout            int
	insecureTLSVerify           bool
}

type payloadMsg struct {
	headers     map[string]string
	body        []byte
	timestamp   string
	contentType string
	eventType   string
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

func (c goSmee) parse(data []byte) (payloadMsg, error) {
	pm := payloadMsg{
		headers: make(map[string]string),
	}
	var message interface{}
	_ = json.Unmarshal(data, &message)
	var payload map[string]interface{}
	err := mapstructure.Decode(message, &payload)
	if err != nil {
		return pm, err
	}
	for payloadKey, payloadValue := range payload {
		if strings.HasPrefix(payloadKey, "x-") || payloadKey == "user-agent" {
			if pv, ok := payloadValue.(string); ok {
				pm.headers[title(payloadKey)] = pv
			}
		}
		if payloadKey == "bodyB" {
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
		} else if payloadKey == "body" {
			mb := &messageBody{}
			err := json.NewDecoder(strings.NewReader(string(data))).Decode(mb)
			if err != nil {
				return pm, err
			}
			pm.body = mb.Body
		}
		if payloadKey == "content-type" {
			if pv, ok := payloadValue.(string); ok {
				pm.contentType = pv
			}
		}
		if payloadKey == "timestamp" {
			var ts string
			if pv, ok := payloadValue.(float64); ok {
				ts = fmt.Sprintf("%.f", pv)
				ts = ts[:len(ts)-3]
			}
			tsInt, err := strconv.ParseInt(ts, 10, 64)
			if err != nil {
				return payloadMsg{}, fmt.Errorf("cannot convert timestamp to int64")
			}
			dt := time.Unix(tsInt, 0)

			pm.timestamp = dt.Format("20060102T15h04")
		}

		if payloadKey == "x-github-event" || payloadKey == "x-gitlab-event" || payloadKey == "x-event-key" {
			if pv, ok := payloadValue.(string); ok {
				// github action don't like it
				replace := strings.NewReplacer(":", "-", " ", "_", "/", "_")
				pv = replace.Replace(strings.ToLower(pv))
				// remove all non-alphanumeric characters and don't let directory straversal
				pv = pmEventRe.FindString(pv)
				pm.eventType = pv
			}
		}
	}

	if len(c.ignoreEvents) > 0 && pm.eventType != "" {
		for _, v := range c.ignoreEvents {
			if v == pm.eventType {
				fmt.Fprintf(os.Stdout, "%sskipping event %s as requested\n", c.emoji("!", "blue+b"), pm.eventType)
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

func (c goSmee) saveData(b []byte) error {
	pm, err := c.parse(b)
	if err != nil {
		return err
	}
	if len(pm.headers) == 0 {
		return nil
	}

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
	// // write data
	_, err = f.Write(pm.body)
	if err != nil {
		return err
	}

	shscript := fmt.Sprintf("%s/%s.sh", c.saveDir, fbasepath)

	fmt.Fprintf(os.Stdout, "%s%s and %s has been saved\n", c.emoji("⌁", "yellow+b"), shscript, jsonfile)
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
	if err := os.Chmod(shscript, 0o755); err != nil {
		return err
	}
	return nil
}

func (c goSmee) replayData(b []byte) error {
	// replay data to targetURL
	pm, err := c.parse(b)
	if err != nil {
		return err
	}
	if len(pm.headers) == 0 {
		return nil
	}

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

	msg = fmt.Sprintf("%s replayed to %s, status: %s", msg, ansi.Color(c.targetURL, "green+ub"), ansi.Color(fmt.Sprintf("%d", resp.StatusCode), "blue+b"))
	if resp.StatusCode > 299 {
		msg = fmt.Sprintf("%s, error: %s", msg, resp.Status)
	}
	fmt.Fprintf(os.Stdout, "%s%s\n", c.emoji("•", "magenta+b"), msg)
	return nil
}

func (c goSmee) clientSetup() error {
	version := strings.TrimSpace(string(Version))
	fmt.Fprintf(os.Stdout, "%sStarting gosmee version: %s\n", c.emoji("⇉", "green+b"), version)
	client := sse.NewClient(c.smeeURL)
	client.Headers["User-Agent"] = fmt.Sprintf("gosmee/%s", version)
	// this is to get nginx to work
	client.Headers["X-Accel-Buffering"] = "no"
	channel := filepath.Base(c.smeeURL)
	if strings.HasPrefix(c.smeeURL, "https://smee.io") {
		channel = smeeChannel
	}
	err := client.Subscribe(channel, func(msg *sse.Event) {
		if string(msg.Event) == "ready" || string(msg.Data) == "ready" {
			fmt.Fprintf(os.Stdout,
				"%sForwarding %s to %s\n", c.emoji("✓", "yellow+b"), ansi.Color(c.smeeURL, "green+u"),
				ansi.Color(c.targetURL, "green+u"))
			return
		}
		if string(msg.Data) != "{}" {
			if c.saveDir != "" {
				err := c.saveData(msg.Data)
				if err != nil {
					fmt.Fprintf(os.Stdout, "%s Forwarding %s\n", ansi.Color("ERROR", "red+b"), err.Error())
					return
				}
			}
			if !c.noReplay {
				if err := c.replayData(msg.Data); err != nil {
					fmt.Fprintf(os.Stdout, "%s Forwarding %s\n", ansi.Color("ERROR", "red+b"), err.Error())
					return
				}
			}
		}
	})
	return err
}
