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
	&cli.StringFlag{
		Name:    "exec",
		Usage:   "Shell command to execute on each incoming webhook event. The JSON payload is passed via stdin. Security warning: do not use this with untrusted webhook sources without proper input validation",
		EnvVars: []string{"GOSMEE_EXEC"},
	},
	&cli.StringSliceFlag{
		Name:    "exec-on-events",
		Aliases: []string{"E"},
		Usage:   "Only run --exec on these event types (e.g., push, pull_request). If not set, --exec runs on all events",
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
	&cli.BoolFlag{
		Name:    "new-url",
		Aliases: []string{"u"},
		Usage:   "Generate a new URL from https://hook.pipelinesascode.com",
		Value:   false,
	},
	&cli.BoolFlag{
		Name:  "httpie",
		Usage: "Use httpie instead of curl in replay scripts (requires httpie installed)",
		Value: false,
	},
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
	&cli.IntFlag{
		Name:    "health-port",
		Usage:   "Port to expose health endpoint for Kubernetes liveness/readiness probes",
		Value:   0,
		EnvVars: []string{"GOSMEE_HEALTH_PORT"},
	},
	&cli.IntFlag{
		Name:    "sse-buffer-size",
		Usage:   "SSE client buffer size in bytes",
		Value:   1048576, // 1MB
		EnvVars: []string{"GOSMEE_SSE_BUFFER_SIZE"},
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
	&cli.StringSliceFlag{
		Name:    "allowed-ips",
		Usage:   "CIDR ranges or IP addresses to allow webhook requests from. Can be specified multiple times. If not specified, all IPs are allowed",
		EnvVars: []string{"GOSMEE_ALLOWED_IPS"},
	},
	&cli.BoolFlag{
		Name:    "trust-proxy",
		Usage:   "Trust X-Forwarded-For and X-Real-IP headers for client IP",
		Value:   false,
		EnvVars: []string{"GOSMEE_TRUST_PROXY"},
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
	&cli.IntFlag{
		Name:    "max-body-size",
		Usage:   "Maximum body size in bytes for incoming webhooks",
		Value:   26214400, // 25MB
		EnvVars: []string{"GOSMEE_MAX_BODY_SIZE"},
	},
}
