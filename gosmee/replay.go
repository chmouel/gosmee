package gosmee

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/google/go-github/v57/github"
	"github.com/mattn/go-isatty"
	"github.com/mgutz/ansi"
	"github.com/urfave/cli/v2"
	"golang.org/x/exp/slog"
)

type replayOpts struct {
	replayDataOpts *replayDataOpts
	cliCtx         *cli.Context
	client         *github.Client
	repo           string
	org            string
	cacheDir       string
	logger         *slog.Logger
}

func saveCursor(cacheFile, cursor string) error {
	f, err := os.OpenFile(cacheFile, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return fmt.Errorf("cannot open cache file: %w", err)
	}
	defer f.Close()
	_, err = f.WriteString(cursor)
	if err != nil {
		return fmt.Errorf("cannot write to cache file: %w", err)
	}
	return nil
}

func readCursor(cacheFile string) (string, error) {
	f, err := os.OpenFile(cacheFile, os.O_RDONLY, 0o600)
	if err != nil {
		return "", fmt.Errorf("cannot open cache file: %w", err)
	}
	defer f.Close()
	buf := make([]byte, 1024)
	n, err := f.Read(buf)
	if err != nil {
		return "", fmt.Errorf("cannot read cache file: %w", err)
	}
	return strings.TrimSpace(string(buf[:n])), nil
}

// chooseDeliveries reverses the deliveries slice and only show the deliveries since the cache file id.
func chooseDeliveries(deliveries []*github.HookDelivery, cacheFileID string) []*github.HookDelivery {
	for i := len(deliveries)/2 - 1; i >= 0; i-- {
		opp := len(deliveries) - 1 - i
		deliveries[i], deliveries[opp] = deliveries[opp], deliveries[i]
	}
	if cacheFileID == "" {
		return deliveries
	}
	iCacheFileID, err := strconv.ParseInt(cacheFileID, 10, 64)
	if err != nil {
		return deliveries
	}

	retdeliveries := make([]*github.HookDelivery, 0)
	for _, d := range deliveries {
		if d.GetID() == iCacheFileID {
			retdeliveries = []*github.HookDelivery{}
			continue
		}
		retdeliveries = append(retdeliveries, d)
	}
	return retdeliveries
}

func (r *replayOpts) replayHooks(ctx context.Context, hookid int64) error {
	cacheFile := filepath.Join(r.cacheDir, fmt.Sprintf("%s-%s-%d", r.org, r.repo, hookid))
	opt := &github.ListCursorOptions{}
	cursor, _ := readCursor(cacheFile)
	var changed bool
	for {
		for {
			deliveries, resp, err := r.client.Repositories.ListHookDeliveries(ctx, r.org, r.repo, hookid, opt)
			if err != nil {
				return fmt.Errorf("cannot list deliveries: %w", err)
			}
			// reverse deliveries to replay from oldest to newest
			deliveries = chooseDeliveries(deliveries, cursor)
			for _, hd := range deliveries {
				cursor = fmt.Sprintf("%d", hd.GetID())
				changed = true
				delivery, _, err := r.client.Repositories.GetHookDelivery(ctx, r.org, r.repo, hookid, hd.GetID())
				if err != nil {
					return fmt.Errorf("cannot get delivery: %w", err)
				}
				pm := payloadMsg{}
				var ok bool
				if pm.contentType, ok = delivery.Request.Headers["Content-Type"]; !ok {
					pm.contentType = "application/json"
				}
				pm.body = delivery.Request.GetRawPayload()
				pm.headers = delivery.Request.GetHeaders()

				// get the event type
				if pv, ok := pm.headers["X-GitHub-Event"]; ok {
					// github action don't like it
					replace := strings.NewReplacer(":", "-", " ", "_", "/", "_")
					pv = replace.Replace(strings.ToLower(pv))
					// remove all non-alphanumeric characters and don't let directory traversal
					pv = pmEventRe.FindString(pv)
					pm.eventType = pv
				}
				if pd, ok := pm.headers["X-GitHub-Delivery"]; ok {
					pm.eventID = pd
				}

				dt := delivery.DeliveredAt.GetTime()
				pm.timestamp = dt.Format(tsFormat)

				if err := replayData(r.replayDataOpts, r.logger, pm); err != nil {
					fmt.Fprintf(os.Stdout,
						"%s forwarding message with headers '%s' - %s\n",
						ansi.Color("ERROR", "red+b"),
						pm.headers,
						err.Error())
					continue
				}
			}

			if resp.NextPage == 0 {
				break
			}
		}
		// save the cursor to cache file
		if changed {
			if err := saveCursor(cacheFile, cursor); err != nil {
				fmt.Fprintf(os.Stdout, "error saving cursor to cache file %s: %s\n", r.cacheDir, err.Error())
				break
			}
		}
		time.Sleep(5 * time.Second)
	}
	return nil
}

func (r *replayOpts) listHooks(ctx context.Context) error {
	hooks, _, err := r.client.Repositories.ListHooks(ctx, r.org, r.repo, nil)
	if err != nil {
		return fmt.Errorf("cannot list hooks: %w", err)
	}

	fmt.Fprintf(os.Stdout, "%-20s %-20s %s\n", "ID", "Name", "URL")
	for _, h := range hooks {
		url := ""
		if _url, here := h.Config["url"]; here {
			var ok bool
			if url, ok = _url.(string); !ok {
				url = ""
			}
		}
		fmt.Fprintf(os.Stdout, "%-20d %-20s %s\n", h.GetID(), h.GetName(), url)
	}
	return nil
}

func replay(c *cli.Context) error {
	ctx := context.Background()
	client := github.NewClient(nil)
	client = client.WithAuthToken(c.String("github-token"))

	logger, nocolor, err := getLogger(c)
	if err != nil {
		return err
	}

	ropt := &replayOpts{
		cliCtx:   c,
		client:   client,
		cacheDir: cachePath,
		logger:   logger,
	}
	if c.String("cache-dir") != "" {
		ropt.cacheDir = c.String("cache-dir")
	}

	orgRepo := c.Args().Get(0)
	if strings.Contains(orgRepo, "/") {
		ropt.org = strings.Split(orgRepo, "/")[0]
		ropt.repo = strings.Split(orgRepo, "/")[1]
	}
	if ropt.org == "" || ropt.repo == "" {
		return fmt.Errorf("org and repo are required, example: org/repo")
	}

	if c.IsSet("list-hooks") {
		return ropt.listHooks(ctx)
	}

	_hookID := c.Args().Get(1)
	_hookID = strings.TrimSpace(_hookID)
	if _hookID == "" {
		return fmt.Errorf("hook-id is required, use --list-hooks to get the hook id")
	}
	// parse _hookID string as int64
	hookID, err := strconv.ParseInt(_hookID, 10, 64)
	if err != nil {
		return fmt.Errorf("hook-id is required, use --list-hooks to get the hook id")
	}

	if hookID == 0 {
		return fmt.Errorf("hook-id is required, use --list-hooks to get the hook id")
	}

	// TODO: remove duplication from client and here
	var targetURL string
	if os.Getenv("GOSMEE_TARGET_URL") != "" {
		targetURL = os.Getenv("GOSMEE_TARGET_URL")
	} else {
		if c.NArg() != 3 {
			return fmt.Errorf("missing the target url where to forward the webhook, ie: http://localhost:8080")
		}
		targetURL = c.Args().Get(2)
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
	ropt.replayDataOpts = &replayDataOpts{
		targetURL:         targetURL,
		saveDir:           c.String("saveDir"),
		noReplay:          c.Bool("noReplay"),
		decorate:          decorate,
		ignoreEvents:      c.StringSlice("ignore-event"),
		targetCnxTimeout:  c.Int("target-connection-timeout"),
		insecureTLSVerify: c.Bool("insecure-skip-tls-verify"),
	}
	return ropt.replayHooks(ctx, hookID)
}
