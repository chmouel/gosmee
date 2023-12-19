package gosmee

import (
	"os"

	"github.com/urfave/cli/v2"
)

// getCachePath returns the cache path for the application.
// It first checks if the GOSMEE_CACHE_PATH environment variable is set.
// If not, it checks the XDG_CACHE_HOME environment variable.
// If neither is set, it defaults to using the HOME environment variable.
// If none of the above environment variables are set, it defaults to "/tmp/gosmee".
// It also ensures that the cache directory exists, creating it if necessary.
func getCachePath() string {
	var cachePath string
	switch {
	case os.Getenv("GOSMEE_CACHE_PATH") != "":
		cachePath = os.Getenv("GOSMEE_CACHE_PATH")
	case os.Getenv("XDG_CACHE_HOME") != "":
		cachePath = os.Getenv("XDG_CACHE_HOME") + "/gosmee"
	case os.Getenv("HOME") != "":
		cachePath = os.Getenv("HOME") + "/.cache/gosmee"
	default:
		cachePath = "/tmp/gosmee"
	}

	// create base dir if not exists
	if _, err := os.Stat(cachePath); os.IsNotExist(err) {
		err := os.MkdirAll(cachePath, 0o755)
		if err != nil {
			panic(err)
		}
	}
	return cachePath
}

var cachePath = getCachePath()

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
		Usage:   "List hooks and its a IDs from a repository",
		Aliases: []string{"L"},
	},
	&cli.StringFlag{
		Name:    "time-since",
		Aliases: []string{"T"},
		Usage:   "Replay events from this time",
		Value:   cachePath,
	},
}

var clientFlags = []cli.Flag{
	&cli.StringFlag{
		Name:    "channel",
		Aliases: []string{"c"},
		Usage:   "gosmee channel to listen, only useful when you are not use smee.io",
		Value:   smeeChannel,
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
}
