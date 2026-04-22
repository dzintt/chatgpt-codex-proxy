package models

import (
	"context"
	"log/slog"
	"sort"
	"strings"
	"time"

	"chatgpt-codex-proxy/internal/accounts"
	"chatgpt-codex-proxy/internal/codex"
	"chatgpt-codex-proxy/internal/config"
)

const (
	initialFetchDelay = time.Second
	retryDelay        = 10 * time.Second
	refreshInterval   = time.Hour
)

type Fetcher struct {
	cfg        config.Config
	logger     *slog.Logger
	accounts   *accounts.Service
	accountMgr *codex.AccountManager
	http       *codex.HTTPClient
	catalog    *Catalog
}

func NewFetcher(cfg config.Config, logger *slog.Logger, accountsSvc *accounts.Service, accountMgr *codex.AccountManager, httpClient *codex.HTTPClient, catalog *Catalog) *Fetcher {
	return &Fetcher{
		cfg:        cfg,
		logger:     logger,
		accounts:   accountsSvc,
		accountMgr: accountMgr,
		http:       httpClient,
		catalog:    catalog,
	}
}

func (f *Fetcher) Start(ctx context.Context) {
	go f.run(ctx)
}

func (f *Fetcher) run(ctx context.Context) {
	timer := time.NewTimer(initialFetchDelay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return
	case <-timer.C:
	}

	fetched := false
	for !fetched {
		if ctx.Err() != nil {
			return
		}
		fetched = f.refreshOnce(ctx)
		if fetched {
			break
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(retryDelay):
		}
	}

	ticker := time.NewTicker(refreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			f.refreshOnce(ctx)
		}
	}
}

func (f *Fetcher) refreshOnce(ctx context.Context) bool {
	routes, err := f.routeAccounts()
	if err != nil {
		f.logger.Warn("route account discovery failed", "error", err.Error())
		return false
	}
	if len(routes) == 0 {
		return false
	}

	keys := make([]string, 0, len(routes))
	for key := range routes {
		f.catalog.RegisterRoute(key)
		keys = append(keys, key)
	}
	sort.Strings(keys)

	anySuccess := false
	fetchedAt := time.Now().UTC()
	for _, key := range keys {
		record := routes[key]
		ready, err := f.accountMgr.EnsureReady(ctx, record.ID)
		if err != nil {
			f.logger.Warn("codex model fetch ensure-ready failed", "route_key", key, "account_id", record.ID, "error", err.Error())
			continue
		}
		entries, err := f.http.GetCodexModels(ctx, ready)
		if err != nil {
			f.logger.Warn("codex model fetch failed", "route_key", key, "account_id", ready.ID, "error", err.Error())
			continue
		}
		normalized := NormalizeBackendEntries(entries)
		if len(normalized) == 0 {
			continue
		}
		f.catalog.ApplyRouteModels(key, normalized, fetchedAt)
		anySuccess = true
	}

	if anySuccess {
		if err := SaveCache(f.cfg.DataDir, f.catalog.Snapshot()); err != nil {
			f.logger.Warn("save models cache failed", "error", err.Error())
		}
	}
	return anySuccess
}

func (f *Fetcher) routeAccounts() (map[string]accounts.Record, error) {
	items, err := f.accounts.List()
	if err != nil {
		return nil, err
	}
	out := make(map[string]accounts.Record)
	for _, record := range items {
		if record.Status != accounts.StatusActive {
			continue
		}
		if strings.TrimSpace(record.Token.AccessToken) == "" {
			continue
		}
		eligible, err := f.accounts.EligibleNow(record.ID)
		if err != nil {
			return nil, err
		}
		if !eligible {
			continue
		}
		key := RoutingKeyForRecord(record)
		if _, exists := out[key]; exists {
			continue
		}
		out[key] = record
	}
	return out, nil
}
