package gosmee

import (
	"context"
	"fmt"
	"os"

	"github.com/google/go-github/v57/github"
	"github.com/mgutz/ansi"
)

func (r *replayOpts) listHooks(ctx context.Context) error {
	var hooks []*github.Hook
	var err error
	if hooks, _, err = r.ghop.ListHooks(ctx, r.org, r.repo, nil); err != nil {
		return fmt.Errorf("cannot list hooks: %w", err)
	}

	fmt.Fprintf(os.Stdout, ansi.Color(fmt.Sprintf("%-20s %-20s %s\n", "ID", "Name", "URL"), "cyan+b"))
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

func (r *replayOpts) listDeliveries(ctx context.Context, hookID int64) error {
	var deliveries []*github.HookDelivery
	var err error
	opt := &github.ListCursorOptions{PerPage: 50}
	if deliveries, _, err = r.ghop.ListHookDeliveries(ctx, r.org, r.repo, hookID, opt); err != nil {
		return fmt.Errorf("cannot list deliveries: %w", err)
	}
	fmt.Fprintf(os.Stdout, ansi.Color(fmt.Sprintf("%-12s %-12s %s\n", "ID", "Event", "Delivered At"), "cyan+b"))
	for _, d := range deliveries {
		fmt.Fprintf(os.Stdout, "%-12d %-12s %s\n", d.GetID(), d.GetEvent(), d.GetDeliveredAt().Format(userTSFormat))
	}
	return nil
}
