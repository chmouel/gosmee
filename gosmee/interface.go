package gosmee

import (
	"context"

	"github.com/google/go-github/v57/github"
	"golang.org/x/exp/slog"
)

type GHOp interface {
	ListHooks(ctx context.Context, org, repo string, opt *github.ListOptions) ([]*github.Hook, *github.Response, error)
	ListHookDeliveries(ctx context.Context, org, repo string, hookID int64, opt *github.ListCursorOptions) ([]*github.HookDelivery, *github.Response, error)
	GetHookDelivery(ctx context.Context, org, repo string, hookID, deliveryID int64) (*github.HookDelivery, *github.Response, error)
	Starting()
}

var (
	_ GHOp = (*RepoOP)(nil)
	_ GHOp = (*OrgOP)(nil)
)

type RepoOP struct {
	client    *github.Client
	logger    *slog.Logger
	org, repo string
}

func NewRepoLister(client *github.Client, logger *slog.Logger, org, repo string) *RepoOP {
	return &RepoOP{client: client, logger: logger, org: repo, repo: repo}
}

func (r *RepoOP) Starting() {
	r.logger.Info("watching deliveries on", "org", r.org, "repo", r.repo)
}

func (r *RepoOP) ListHooks(ctx context.Context, org, repo string, opt *github.ListOptions) ([]*github.Hook, *github.Response, error) {
	return r.client.Repositories.ListHooks(ctx, org, repo, opt)
}

func (r *RepoOP) ListHookDeliveries(ctx context.Context, org, repo string, hookID int64, opt *github.ListCursorOptions) ([]*github.HookDelivery, *github.Response, error) {
	return r.client.Repositories.ListHookDeliveries(ctx, org, repo, hookID, opt)
}

func (r *RepoOP) GetHookDelivery(ctx context.Context, org, repo string, hookID, deliveryID int64) (*github.HookDelivery, *github.Response, error) {
	return r.client.Repositories.GetHookDelivery(ctx, org, repo, hookID, deliveryID)
}

var _ GHOp = (*RepoOP)(nil)

type OrgOP struct {
	client    *github.Client
	logger    *slog.Logger
	org, repo string
}

func NewOrgLister(client *github.Client, logger *slog.Logger, org, repo string) *OrgOP {
	return &OrgOP{client: client, logger: logger, org: org, repo: repo}
}

func (r *OrgOP) ListHooks(ctx context.Context, org, _ string, opt *github.ListOptions) ([]*github.Hook, *github.Response, error) {
	return r.client.Organizations.ListHooks(ctx, org, opt)
}

func (r *OrgOP) ListHookDeliveries(ctx context.Context, org, _ string, hookID int64, opt *github.ListCursorOptions) ([]*github.HookDelivery, *github.Response, error) {
	return r.client.Organizations.ListHookDeliveries(ctx, org, hookID, opt)
}

func (r *OrgOP) GetHookDelivery(ctx context.Context, org, _ string, hookID, deliveryID int64) (*github.HookDelivery, *github.Response, error) {
	return r.client.Organizations.GetHookDelivery(ctx, org, hookID, deliveryID)
}

func (r *OrgOP) Starting() {
	r.logger.Info("watching deliveries on", "org", r.org)
}
