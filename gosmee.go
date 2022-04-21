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
	"text/template"
	"time"

	_ "embed"

	"github.com/mattn/go-isatty"
	"github.com/mgutz/ansi"
	"github.com/mitchellh/mapstructure"
	"github.com/r3labs/sse"
	"github.com/urfave/cli/v2"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

//go:embed misc/replay_script.tmpl.bash
var shellScriptTmpl []byte

//go:embed misc/zsh_completion.zsh
var zshCompletion []byte

//go:embed misc/version
var Version []byte

//go:embed misc/bash_completion.bash
var bashCompletion []byte

type goSmee struct {
	saveDir, smeeURL, targetURL string
	decorate, noReplay          bool
	ignoreEvents                []string
}

type payloadMsg struct {
	headers     map[string]string
	body        []byte
	timestamp   string
	contentType string
	eventType   string
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

		if payloadKey == "x-github-event" || payloadKey == "x-gitlab-event" || payloadKey == "x-event-key" {
			if pv, ok := payloadValue.(string); ok {
				// github action don't like it
				replace := strings.NewReplacer(":", "-", " ", "_")
				pv = replace.Replace(strings.ToLower(pv))
				pm.eventType = pv
			}
		}
	}

	if len(c.ignoreEvents) > 0 && pm.eventType != "" {
		for _, v := range c.ignoreEvents {
			if v == pm.eventType {
				os.Stdout.WriteString(fmt.Sprintf("%sskipping event %s as requested\n", c.emoji("!", "blue+b"), pm.eventType))
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

	var fprefix string
	if pm.eventType != "" {
		fprefix = filepath.Join(c.saveDir, fmt.Sprintf("%s-%s", pm.eventType, pm.timestamp))
	} else {
		fprefix = filepath.Join(c.saveDir, fmt.Sprintf("%s", pm.timestamp))
	}

	jsonfile := fmt.Sprintf("%s.json", fprefix)
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

	shscript := fmt.Sprintf("%s.sh", fprefix)
	os.Stdout.WriteString(fmt.Sprintf("%s%s and %s has been saved\n", c.emoji("⌁", "yellow+b"), shscript, jsonfile))
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
		FilePrefix  string
	}{
		Headers:     headers,
		TargetURL:   c.targetURL,
		ContentType: pm.contentType,
		FilePrefix:  fprefix,
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
	os.Stdout.WriteString(fmt.Sprintf("%s%s\n", c.emoji("•", "magenta+b"), msg))
	return nil
}

func (c goSmee) setup() error {
	version := strings.TrimSpace(string(Version))
	os.Stdout.WriteString(fmt.Sprintf("%sStarting gosmee version: %s\n", c.emoji("⇉", "green+b"), version))
	client := sse.NewClient(c.smeeURL)
	err := client.Subscribe("messages", func(msg *sse.Event) {
		if string(msg.Event) == "ready" {
			// print to stdout
			os.Stdout.WriteString(fmt.Sprintf("%sForwarding %s to %s\n", c.emoji("✓", "yellow+b"), ansi.Color(c.smeeURL, "green+u"), ansi.Color(c.targetURL, "green+u")))
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
		Name:                 "gosmee",
		Usage:                "forward smee url to a local service",
		UsageText:            "gosmee [command options] SMEE_URL LOCAL_SERVICE_URL",
		EnableBashCompletion: true,
		Version:              strings.TrimSpace(string(Version)),
		Commands: []*cli.Command{
			{
				Name:  "completion",
				Usage: "generate shell completion",
				Subcommands: []*cli.Command{
					{
						Name:  "zsh",
						Usage: "generate zsh completion",
						Action: func(c *cli.Context) error {
							os.Stdout.WriteString(string(zshCompletion))
							return nil
						},
					},
					{
						Name:  "bash",
						Usage: "generate bash completion",
						Action: func(c *cli.Context) error {
							os.Stdout.WriteString(string(bashCompletion))
							return nil
						},
					},
				},
			},
		},
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
				smeeURL:      smeeURL,
				targetURL:    targetURL,
				saveDir:      c.String("saveDir"),
				noReplay:     c.Bool("noReplay"),
				decorate:     decorate,
				ignoreEvents: c.StringSlice("ignore-event"),
			}
			return cfg.setup()
		},
		Flags: []cli.Flag{
			&cli.StringSliceFlag{
				Name:    "ignore-event",
				Aliases: []string{"I"},
				Usage:   "Ignore these events",
			},
			&cli.StringFlag{
				Name:    "saveDir",
				Usage:   "Save payloads to `DIR` populated with shell scripts to replay easily.",
				Aliases: []string{"s"},
				EnvVars: []string{"GOSMEE_SAVEDIR"},
			},
			&cli.BoolFlag{
				Name:    "noReplay",
				Usage:   "Do not replay payloads",
				Aliases: []string{"n"},
				Value:   false,
			},
			&cli.BoolFlag{
				Name:    "nocolor",
				Usage:   "Disable color output, automatically disabled when non tty",
				EnvVars: []string{"NO_COLOR"},
			},
		},
	}
	if err := app.Run(os.Args); err != nil {
		os.Stdout.WriteString(fmt.Sprintf("%s gosmee %s\n", ansi.Color("ERROR", "red+b"), err.Error()))
	}
}
