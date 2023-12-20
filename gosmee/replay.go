package gosmee

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/go-github/v57/github"
	"github.com/mattn/go-isatty"
	"github.com/mgutz/ansi"
	"github.com/urfave/cli/v2"
	"golang.org/x/exp/slog"
)

const userTSFormat = "2006-01-02T15:04:05"

type replayOpts struct {
	replayDataOpts *replayDataOpts
	cliCtx         *cli.Context
	client         *github.Client
	repo           string
	org            string
	logger         *slog.Logger
	sinceTime      time.Time
	ghop           GHOp
}

// chooseDeliveries reverses the deliveries slice and only show the deliveries since the last date we parsed.
func (r *replayOpts) chooseDeliveries(dlvs []*github.HookDelivery) []*github.HookDelivery {
	retdeliveries := make([]*github.HookDelivery, 0)
	for _, d := range dlvs {
		if d.DeliveredAt.Before(r.sinceTime) {
			break
		}
		retdeliveries = append(retdeliveries, d)
	}
	// finally reverse the slice to make sure we get the oldest first
	for i := len(retdeliveries)/2 - 1; i >= 0; i-- {
		opp := len(retdeliveries) - 1 - i
		retdeliveries[i], retdeliveries[opp] = retdeliveries[opp], retdeliveries[i]
	}
	return retdeliveries
}

func (r *replayOpts) replayHooks(ctx context.Context, hookid int64) error {
	r.ghop.Starting()
	for {
		opt := &github.ListCursorOptions{PerPage: 100}
		deliveries, _, err := r.ghop.ListHookDeliveries(ctx, r.org, r.repo, hookid, opt)
		if err != nil {
			return fmt.Errorf("cannot list deliveries: %w", err)
		}
		// reverse deliveries to replay from oldest to newest
		deliveries = r.chooseDeliveries(deliveries)
		for _, hd := range deliveries {
			var delivery *github.HookDelivery
			// There can be a race between the time listhookdeliveries show the
			// id and Gethookdelivery is created on the API, so wait for it for a bit
			for range []int{1, 2, 3} {
				var resp *github.Response
				var err error
				delivery, resp, err = r.ghop.GetHookDelivery(ctx, r.org, r.repo, hookid, hd.GetID())
				if resp.StatusCode == http.StatusNotFound {
					time.Sleep(1 * time.Second)
					continue
				}
				if err != nil {
					return fmt.Errorf("cannot get delivery: %w", err)
				}
			}
			pm := payloadMsg{}
			var ok bool
			if pm.contentType, ok = delivery.Request.Headers["Content-Type"]; !ok {
				pm.contentType = "application/json"
			}
			pm.body = delivery.Request.GetRawPayload()
			pm.headers = delivery.Request.GetHeaders()
			pm.eventID = hd.GetGUID()

			// get the event type
			if pv, ok := pm.headers["X-GitHub-Event"]; ok {
				// github action don't like it
				replace := strings.NewReplacer(":", "-", " ", "_", "/", "_")
				pv = replace.Replace(strings.ToLower(pv))
				// remove all non-alphanumeric characters and don't let directory traversal
				pv = pmEventRe.FindString(pv)
				pm.eventType = pv
			}

			dt := delivery.DeliveredAt.GetTime()
			pm.timestamp = dt.Format(tsFormat)

			if err := replayData(r.replayDataOpts, r.logger, pm); err != nil {
				s := fmt.Sprintf(
					"%s forwarding message with headers '%s' - %s\n",
					ansi.Color("ERROR", "red+b"),
					pm.headers,
					err.Error())
				r.logger.Error(s)
				continue
			}
			if r.replayDataOpts.saveDir != "" {
				err := saveData(r.replayDataOpts, r.logger, pm)
				if err != nil {
					s := fmt.Sprintf("%s saving payload to local directory %s - %s\n",
						ansi.Color("ERROR", "red+b"),
						r.replayDataOpts.saveDir,
						err.Error())
					r.logger.Error(s)
					continue
				}
			}
		}

		if len(deliveries) != 0 {
			r.sinceTime = deliveries[len(deliveries)-1].DeliveredAt.GetTime().Add(1 * time.Second)
		}
		time.Sleep(5 * time.Second)
	}
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
		cliCtx: c,
		client: client,
		logger: logger,
	}

	orgRepo := c.Args().Get(0)
	if strings.Contains(orgRepo, "/") {
		spt := strings.Split(orgRepo, "/")
		ropt.org = spt[0]
		ropt.repo = spt[1]
	} else {
		ropt.org = orgRepo
	}

	if ropt.org == "" {
		return fmt.Errorf("at least an org is required or an org/repo")
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

	if c.IsSet("list-deliveries") {
		return ropt.listDeliveries(ctx, hookID)
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

	sinceTime := c.String("time-since")
	if sinceTime == "" {
		// start from now
		ropt.sinceTime = time.Now()
	}
	if sinceTime != "" {
		since, err := time.Parse(userTSFormat, sinceTime)
		if err != nil {
			return fmt.Errorf("cannot parse time-since: %w", err)
		}
		ropt.sinceTime = since
	}
	if ropt.repo == "" {
		ropt.ghop = NewOrgLister(client, logger, ropt.org, ropt.repo)
	} else {
		ropt.ghop = NewRepoLister(client, logger, ropt.org, ropt.repo)
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
