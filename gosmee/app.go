package gosmee

import (
	"fmt"
	"os"
	"strings"

	"github.com/mattn/go-isatty"
	"github.com/mgutz/ansi"
	"github.com/urfave/cli/v2"
)

func makeapp() *cli.App {
	return &cli.App{
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
					&cli.IntFlag{
						Name:    "port",
						Aliases: []string{"p"},
						Value:   defaultServerPort,
						Usage:   "Port to listen on",
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
					if c.NArg() != 2 {
						return fmt.Errorf("need at least a serverURL and a targetURL as arguments, ie: gosmee client https://smee.io/aBcDeF http://localhost:8080")
					}
					smeeURL := c.Args().Get(0)
					targetURL := c.Args().Get(1)
					if !strings.HasPrefix(targetURL, "http") {
						return fmt.Errorf("targetURL should start with http(s)")
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
						channel:      c.String("channel"),
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
						Usage:   "How long to wait for the connection timeout",
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
				},
			},
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
	}
}

func Run(args []string) error {
	return makeapp().Run(args)
}
