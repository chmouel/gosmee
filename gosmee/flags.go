package gosmee

import (
	"github.com/urfave/cli/v2"
)

var commonFlags = []cli.Flag{
	&cli.StringFlag{
		Name:    "output",
		Usage:   `Output format, one of "json", "pretty"`,
		Value:   "pretty",
		Aliases: []string{"o"},
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
}

var replayFlags = []cli.Flag{
	&cli.StringFlag{
		Name:     "github-token",
		Usage:    "GitHub token to use to replay payloads",
		Required: true,
		Aliases:  []string{"t"},
	},
	&cli.BoolFlag{
		Name:    "list-hooks",
		Usage:   "List hooks and its IDs on a repository",
		Aliases: []string{"L"},
	},
	&cli.BoolFlag{
		Name:    "list-deliveries",
		Usage:   "List deliveries from on hook ID",
		Aliases: []string{"D"},
	},
	&cli.StringFlag{
		Name:    "time-since",
		Aliases: []string{"T"},
		Usage:   "Replay events from this time",
	},
}

var clientFlags = []cli.Flag{
	&cli.StringFlag{
		Name:    "channel",
		Aliases: []string{"c"},
		Usage:   "gosmee channel to listen, only useful when you are not use smee.io",
		Value:   smeeChannel,
	},
	&cli.StringFlag{
		Name:  "local-debug-url",
		Usage: "Local URL when debugging the payloads",
		Value: defaultLocalDebugURL,
	},
}

var serverFlags = []cli.Flag{
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
		Name:  "footer-file",
		Usage: "An HTML file to show in footer for copyright and author",
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
	&cli.StringSliceFlag{
		Name:    "webhook-signature",
		Usage:   "Secret tokens to validate webhook signatures (GitHub, GitLab and many others). Can be specified multiple times",
		EnvVars: []string{"GOSMEE_WEBHOOK_SIGNATURE"},
	},
}
