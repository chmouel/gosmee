package gosmee

import (
	_ "embed"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/chmouel/gosmee/tint"
	"github.com/mattn/go-isatty"
	"github.com/mgutz/ansi"
	"github.com/urfave/cli/v2"
	"golang.org/x/exp/slog"
)

//go:embed templates/zsh_completion.zsh
var zshCompletion []byte

//go:embed templates/bash_completion.bash
var bashCompletion []byte

func getLogger(c *cli.Context) (*slog.Logger, bool, error) {
	nocolor := c.Bool("nocolor")
	w := os.Stdout
	var logger *slog.Logger
	switch c.String("output") {
	case "json":
		logger = slog.New(slog.NewJSONHandler(os.Stdout, nil))
		nocolor = true
	case "pretty":
		logger = slog.New(tint.NewHandler(w, &tint.Options{
			TimeFormat: time.RFC1123,
			NoColor:    !isatty.IsTerminal(w.Fd()),
		}))
	default:
		return nil, false, fmt.Errorf("invalid output format %s", c.String("output"))
	}
	return logger, nocolor, nil
}

func makeapp() *cli.App {
	app := &cli.App{
		Name:  "gosmee",
		Usage: "Forward SMEE url from an external endpoint to a local service",
		UsageText: `Gosmee can help you reroute webhooks either from https://smee.io or its own server to a local service.
Where the server is the source of the webhook, and the client, which you run on your laptop or behind a
non-publicly accessible endpoint, forward those requests to your local service.`,
		EnableBashCompletion: true,
		Version:              strings.TrimSpace(string(Version)),
		Commands: []*cli.Command{
			{
				Name:  "replay",
				Usage: "Replay payloads from GitHub",
				Action: func(c *cli.Context) error {
					return replay(c)
				},
				Flags: append(commonFlags, replayFlags...),
			},
			{
				Name:  "server",
				Usage: "Make gosmee a relay server from your external webhook",
				Action: func(c *cli.Context) error {
					if !isatty.IsTerminal(os.Stdout.Fd()) {
						ansi.DisableColors(true)
					}
					return serve(c)
				},
				Flags: serverFlags,
			},
			{
				Name:      "client",
				UsageText: "gosmee [command options] SMEE_URL LOCAL_SERVICE_URL",
				Usage:     "Make a client from the relay server to your local service",
				Action: func(c *cli.Context) error {
					logger, nocolor, err := getLogger(c)
					if err != nil {
						return err
					}

					var smeeURL, targetURL string
					if os.Getenv("GOSMEE_URL") != "" && os.Getenv("GOSMEE_TARGET_URL") != "" {
						smeeURL = os.Getenv("GOSMEE_URL")
						targetURL = os.Getenv("GOSMEE_TARGET_URL")
					} else {
						if c.NArg() != 2 {
							return fmt.Errorf("need at least a smeeURL and a targetURL as arguments, ie: gosmee client https://server.smee.url/aBcdeFghijklmn http://localhost:8080")
						}
						smeeURL = c.Args().Get(0)
						targetURL = c.Args().Get(1)
					}
					if _, err := url.Parse(smeeURL); err != nil {
						return fmt.Errorf("smeeURL %s is not a valid url %w", smeeURL, err)
					}
					if _, err := url.Parse(targetURL); err != nil {
						return fmt.Errorf("target url %s is not a valid url %w", targetURL, err)
					}
					decorate := true
					if !isatty.IsTerminal(os.Stdout.Fd()) {
						ansi.DisableColors(true)
						decorate = false
					}
					if nocolor {
						ansi.DisableColors(true)
						decorate = false
					}
					localDebugURL := c.String("local-debug-url")
					if localDebugURL == "" {
						localDebugURL = defaultLocalDebugURL
					}
					cfg := goSmee{
						replayDataOpts: &replayDataOpts{
							smeeURL:           smeeURL,
							targetURL:         targetURL,
							localDebugURL:     localDebugURL,
							saveDir:           c.String("saveDir"),
							noReplay:          c.Bool("noReplay"),
							decorate:          decorate,
							ignoreEvents:      c.StringSlice("ignore-event"),
							targetCnxTimeout:  c.Int("target-connection-timeout"),
							insecureTLSVerify: c.Bool("insecure-skip-tls-verify"),
						},
						logger:  logger,
						channel: c.String("channel"),
					}
					return cfg.clientSetup()
				},
				Flags: append(commonFlags, clientFlags...),
			},
			{
				Name:  "completion",
				Usage: "generate shell completion",
				Subcommands: []*cli.Command{
					{
						Name:  "zsh",
						Usage: "generate zsh completion",
						Action: func(_ *cli.Context) error {
							os.Stdout.WriteString(string(zshCompletion))
							return nil
						},
					},
					{
						Name:  "bash",
						Usage: "generate bash completion",
						Action: func(_ *cli.Context) error {
							os.Stdout.WriteString(string(bashCompletion))
							return nil
						},
					},
					{
						Name:  "fish",
						Usage: "generate fish completion",
						Action: func(c *cli.Context) error {
							ret, err := c.App.ToFishCompletion()
							if err != nil {
								return err
							}
							fmt.Fprintln(os.Stdout, ret)
							return nil
						},
					},
				},
			},
		},
	}
	return app
}

func Run(args []string) error {
	return makeapp().Run(args)
}
