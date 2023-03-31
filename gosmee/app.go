package gosmee

import (
	"fmt"
	"net/url"
	"os"
	"strings"

	_ "embed"

	"github.com/mattn/go-isatty"
	"github.com/mgutz/ansi"
	"github.com/urfave/cli/v2"
)

//go:embed templates/zsh_completion.zsh
var zshCompletion []byte

//go:embed templates/bash_completion.bash
var bashCompletion []byte

func makeapp() *cli.App {
	app := &cli.App{
		Name:  "gosmee",
		Usage: "Forward SMEE url from an external endpoint to a local service",
		UsageText: `gosmee can forward webhook from https://smee.io or from
itself to a local service. The server is the one from where the webhook
points to. The client runs on your laptop or behind a non publically
accessible endpoint and forward request to your local service`,
		EnableBashCompletion: true,
		Version:              strings.TrimSpace(string(Version)),
		Commands: []*cli.Command{
			{
				Name:  "server",
				Usage: "Make gosmee a relay server from your external webhook",
				Action: func(c *cli.Context) error {
					if !isatty.IsTerminal(os.Stdout.Fd()) {
						ansi.DisableColors(true)
					}
					return serve(c)
				},
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:  "public-url",
						Usage: "Public URL to show to user, useful when you are behind a proxy.",
					},
					&cli.IntFlag{
						Name:    "port",
						Aliases: []string{"p"},
						Value:   defaultServerPort,
						Usage:   "Port to listen on",
					},
					&cli.BoolFlag{
						Name:  "auto-cert",
						Value: false,
						Usage: "Automatically generate letsencrypt certs",
					},
					&cli.StringFlag{
						Name:  "footer",
						Usage: "An HTML string to show in footer for copyright and author",
					},
					&cli.StringFlag{
						Name:    "address",
						Aliases: []string{"a"},
						Value:   defaultServerAddress,
						Usage:   "Address to listen on",
					},
					&cli.StringFlag{
						Name:    "tls-cert",
						Usage:   "TLS certificate file",
						EnvVars: []string{"GOSMEE_TLS_CERT"},
					},
					&cli.StringFlag{
						Name:    "tls-key",
						Usage:   "TLS key file",
						EnvVars: []string{"GOSMEE_TLS_KEY"},
					},
				},
			},
			{
				Name:      "client",
				UsageText: "gosmee [command options] SMEE_URL LOCAL_SERVICE_URL",
				Usage:     "Make a client from the relay server to your local service",
				Action: func(c *cli.Context) error {
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
					if c.Bool("nocolor") {
						ansi.DisableColors(true)
						decorate = false
					}
					cfg := goSmee{
						smeeURL:           smeeURL,
						targetURL:         targetURL,
						saveDir:           c.String("saveDir"),
						noReplay:          c.Bool("noReplay"),
						decorate:          decorate,
						ignoreEvents:      c.StringSlice("ignore-event"),
						channel:           c.String("channel"),
						targetCnxTimeout:  c.Int("target-connection-timeout"),
						insecureTLSVerify: c.Bool("insecure-skip-tls-verify"),
					}
					err := cfg.clientSetup()
					return err
				},
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:    "channel",
						Aliases: []string{"c"},
						Usage:   "gosmee channel to listen, only useful when you are not use smee.io",
						Value:   smeeChannel,
					},
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
					&cli.IntFlag{
						Name:    "target-connection-timeout",
						Usage:   "How long to wait when forwarding the request to the service",
						EnvVars: []string{"GOSMEE_TARGET_TIMEOUT"},
						Value:   defaultTimeout,
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
					&cli.BoolFlag{
						Name:  "insecure-skip-tls-verify",
						Value: false,
						Usage: "If true, the target server's certificate will not be checked for validity. This will make your HTTPS connections insecure",
					},
				},
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
