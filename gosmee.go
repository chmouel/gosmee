package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mattn/go-isatty"
	"github.com/mgutz/ansi"
	"github.com/mitchellh/mapstructure"
	"github.com/r3labs/sse"
	"github.com/urfave/cli/v2"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

type goSmee struct {
	saveDir, smeeURL, targetURL string
	decorate, noReplay          bool
}

type payloadMsg struct {
	headers     map[string]string
	body        []byte
	timestamp   string
	contentType string
}

type messageBody struct {
	Body json.RawMessage `json:"body"`
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
		if payloadKey == "body" {
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
			// convert ts to int64
			tsInt, err := strconv.ParseInt(ts, 10, 64)
			if err != nil {
				return payloadMsg{}, fmt.Errorf("cannot convert timestamp to int64")
			}
			dt := time.Unix(tsInt, 0)
			pm.timestamp = dt.Format("20060102T15h04")
		}
	}
	return pm, nil
}

func (c goSmee) emoji(emoji, color string) string {
	if !c.decorate {
		return ""
	}
	return ansi.Color(emoji, color)
}

func (c goSmee) saveData(b []byte) error {
	pm, err := c.parse(b)
	if err != nil {
		return err
	}

	// check if saveDir is created
	if _, err := os.Stat(c.saveDir); os.IsNotExist(err) {
		if err := os.MkdirAll(c.saveDir, 0o755); err != nil {
			return err
		}
	}

	jsonfile := fmt.Sprintf("%s.json", filepath.Join(c.saveDir, pm.timestamp))
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

	shscript := fmt.Sprintf("%s.sh", filepath.Join(c.saveDir, pm.timestamp))
	os.Stdout.WriteString(fmt.Sprintf("%s %s and %s has been saved\n", c.emoji("⌁", "yellow+b"), shscript, jsonfile))
	s, err := os.Create(shscript)
	if err != nil {
		return err
	}
	defer s.Close()
	_, _ = s.WriteString(fmt.Sprintf("#!/bin/bash\n\nset -euxf\ncurl -H 'Content-Type: %s' -X POST -d @%s.json ", pm.contentType, filepath.Join(c.saveDir, pm.timestamp)))
	for k, v := range pm.headers {
		_, _ = s.WriteString(fmt.Sprintf("-H '%s: %s' ", k, v))
	}
	_, _ = s.WriteString(fmt.Sprintf("%s\n", c.targetURL))
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
	client := http.Client{Timeout: time.Duration(1) * time.Second}
	ctx := context.Background()
	req, err := http.NewRequestWithContext(ctx, "POST", c.targetURL, strings.NewReader(string(pm.body)))
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
	defer resp.Body.Close()
	msg := fmt.Sprintf("request replayed to %s, status: %s", ansi.Color(c.targetURL, "green+ub"), ansi.Color(fmt.Sprintf("%d", resp.StatusCode), "blue+b"))
	if resp.StatusCode > 299 {
		msg = fmt.Sprintf("%s, error: %s", msg, resp.Status)
	}
	os.Stdout.WriteString(fmt.Sprintf("%s %s\n", c.emoji("•", "magenta+b"), msg))
	return nil
}

func (c goSmee) setup() error {
	client := sse.NewClient(c.smeeURL)
	err := client.Subscribe("messages", func(msg *sse.Event) {
		if string(msg.Event) == "ready" {
			// print to stdout
			os.Stdout.WriteString(fmt.Sprintf("%s Forwarding %s to %s\n", c.emoji("✓", "green+b"), ansi.Color(c.smeeURL, "green+u"), ansi.Color(c.targetURL, "green+u")))
			return
		}
		if string(msg.Data) != "{}" {
			if c.saveDir != "" {
				err := c.saveData(msg.Data)
				if err != nil {
					os.Stdout.WriteString(fmt.Sprintf("%s Forwarding %s\n", ansi.Color("ERROR", "red+b"), err.Error()))
					return
				}
			}
			if !c.noReplay {
				if err := c.replayData(msg.Data); err != nil {
					os.Stdout.WriteString(fmt.Sprintf("%s Forwarding %s\n", ansi.Color("ERROR", "red+b"), err.Error()))
					return
				}
			}
		}
	})
	return err
}

func main() {
	app := &cli.App{
		Name:  "gosmee",
		Usage: "forward smee url to local",
		Action: func(c *cli.Context) error {
			if c.NArg() != 2 {
				err := cli.ShowCommandHelp(c, c.Command.Name)
				if err != nil {
					return err
				}
				return fmt.Errorf("need at least a smeeurl and a targeturl as arguments, ie: gosmee https://smee.io/aBcDeF http://localhost:8080")
			}
			smeeURL := c.Args().Get(0)
			targetURL := c.Args().Get(1)
			if !strings.HasPrefix(smeeURL, "https://smee.io") {
				return fmt.Errorf("smeeURL does not seem to be a smee url")
			}
			if !strings.HasPrefix(targetURL, "http") {
				return fmt.Errorf("targetURL should start with http")
			}
			decorate := true
			if !isatty.IsTerminal(os.Stdout.Fd()) {
				ansi.DisableColors(true)
				decorate = false
			}
			if c.Bool("nocolor") {
				ansi.DisableColors(true)
				decorate = false
			}
			cfg := goSmee{
				smeeURL:   smeeURL,
				targetURL: targetURL,
				saveDir:   c.String("saveDir"),
				noReplay:  c.Bool("noReplay"),
				decorate:  decorate,
			}
			return cfg.setup()
		},
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "saveDir",
				Usage:   "Save payloads to this dir",
				Aliases: []string{"s"},
			},
			&cli.BoolFlag{
				Name:    "noReplay",
				Usage:   "Do not replay payloads",
				Aliases: []string{"n"},
				Value:   false,
			},
			&cli.BoolFlag{
				Name:  "nocolor",
				Usage: "Disable color output",
			},
		},
	}

	if err := app.Run(os.Args); err != nil {
		os.Stdout.WriteString(fmt.Sprintf("%s Forwarding %s\n", ansi.Color("ERROR", "red+b"), err.Error()))
	}
}
