package models

import (
	"sort"
	"strings"
	"sync"
	"time"

	"chatgpt-codex-proxy/internal/accounts"
)

type Catalog struct {
	mu          sync.RWMutex
	bootstrap   []Entry
	visible     []Entry
	entriesByID map[string]Entry
	support     map[string]map[string]struct{}
	knownRoutes map[string]struct{}
	fetchedAt   time.Time
}

func NewCatalog(bootstrap []Entry) *Catalog {
	c := &Catalog{
		bootstrap:   cloneEntries(bootstrap),
		entriesByID: make(map[string]Entry),
		support:     make(map[string]map[string]struct{}),
		knownRoutes: make(map[string]struct{}),
	}
	for _, entry := range c.bootstrap {
		c.entriesByID[entry.ID] = entry
	}
	c.rebuildVisibleLocked()
	return c
}

func (c *Catalog) Has(modelID string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	_, ok := c.entriesByID[strings.TrimSpace(modelID)]
	return ok && c.visibleContainsLocked(strings.TrimSpace(modelID))
}

func (c *Catalog) Get(modelID string) (Entry, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	modelID = strings.TrimSpace(modelID)
	if !c.visibleContainsLocked(modelID) {
		return Entry{}, false
	}
	entry, ok := c.entriesByID[modelID]
	if !ok {
		return Entry{}, false
	}
	return cloneEntry(entry), true
}

func (c *Catalog) List() []Entry {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return cloneEntries(c.visible)
}

func (c *Catalog) ResolveDefault(configured string) string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.resolveDefaultLocked(accounts.Record{}, strings.TrimSpace(configured), false)
}

func (c *Catalog) ResolveDefaultForRecord(record accounts.Record, configured string) string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.resolveDefaultLocked(record, strings.TrimSpace(configured), true)
}

func (c *Catalog) SupportsRecord(record accounts.Record, modelID string) bool {
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		return false
	}

	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.visibleContainsLocked(modelID) {
		return false
	}
	return c.supportsRecordLocked(record, modelID)
}

func (c *Catalog) LoadCache(snapshot CacheSnapshot) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !snapshot.FetchedAt.IsZero() {
		c.fetchedAt = snapshot.FetchedAt.UTC()
	}
	for _, entry := range snapshot.Models {
		if strings.TrimSpace(entry.ID) == "" {
			continue
		}
		entry.Source = SourceCache
		c.entriesByID[entry.ID] = cloneEntry(entry)
	}
	c.support = make(map[string]map[string]struct{}, len(snapshot.Support))
	for key, ids := range snapshot.Support {
		c.knownRoutes[key] = struct{}{}
		c.support[key] = makeSet(ids)
	}
	c.rebuildVisibleLocked()
}

func (c *Catalog) RegisterRoute(routeKey string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	routeKey = strings.TrimSpace(routeKey)
	if routeKey == "" {
		return
	}
	c.knownRoutes[routeKey] = struct{}{}
	c.rebuildVisibleLocked()
}

func (c *Catalog) ApplyRouteModels(routeKey string, entries []Entry, fetchedAt time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()

	routeKey = strings.TrimSpace(routeKey)
	if routeKey == "" {
		return
	}
	c.knownRoutes[routeKey] = struct{}{}

	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		if strings.TrimSpace(entry.ID) == "" {
			continue
		}
		entry.Source = SourceUpstream
		c.entriesByID[entry.ID] = cloneEntry(entry)
		ids = append(ids, entry.ID)
	}
	c.support[routeKey] = makeSet(ids)
	if !fetchedAt.IsZero() && fetchedAt.After(c.fetchedAt) {
		c.fetchedAt = fetchedAt.UTC()
	}
	c.rebuildVisibleLocked()
}

func (c *Catalog) Snapshot() CacheSnapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()

	support := make(map[string][]string, len(c.support))
	for key, set := range c.support {
		ids := make([]string, 0, len(set))
		for id := range set {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		support[key] = ids
	}
	return CacheSnapshot{
		FetchedAt: c.fetchedAt,
		Models:    cloneEntries(c.visible),
		Support:   support,
	}
}

func RoutingKeyForRecord(record accounts.Record) string {
	planType := strings.TrimSpace(record.PlanType)
	if planType != "" && !strings.EqualFold(planType, "unknown") {
		return "plan:" + planType
	}
	return "acct:" + strings.TrimSpace(record.ID)
}

func (c *Catalog) visibleContainsLocked(modelID string) bool {
	for _, entry := range c.visible {
		if entry.ID == modelID {
			return true
		}
	}
	return false
}

func (c *Catalog) rebuildVisibleLocked() {
	visibleIDs := make(map[string]struct{})
	for _, set := range c.support {
		for id := range set {
			visibleIDs[id] = struct{}{}
		}
	}

	if c.hasUnrefreshedRoutesLocked() {
		for _, entry := range c.bootstrap {
			visibleIDs[entry.ID] = struct{}{}
			c.entriesByID[entry.ID] = entry
		}
	}

	if len(visibleIDs) == 0 {
		c.visible = cloneEntries(c.bootstrap)
		for _, entry := range c.bootstrap {
			c.entriesByID[entry.ID] = entry
		}
		return
	}

	ids := make([]string, 0, len(visibleIDs))
	for id := range visibleIDs {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	visible := make([]Entry, 0, len(ids))
	for _, id := range ids {
		entry, ok := c.entriesByID[id]
		if !ok {
			continue
		}
		visible = append(visible, cloneEntry(entry))
	}
	c.visible = visible
}

func (c *Catalog) hasUnrefreshedRoutesLocked() bool {
	for key := range c.knownRoutes {
		if _, ok := c.support[key]; !ok {
			return true
		}
	}
	return false
}

func (c *Catalog) bootstrapContainsLocked(modelID string) bool {
	for _, entry := range c.bootstrap {
		if entry.ID == modelID {
			return true
		}
	}
	return false
}

func (c *Catalog) supportsRecordLocked(record accounts.Record, modelID string) bool {
	if len(c.support) == 0 {
		return c.bootstrapContainsLocked(modelID)
	}
	key := RoutingKeyForRecord(record)
	if set, ok := c.support[key]; ok && len(set) > 0 {
		_, ok = set[modelID]
		return ok
	}
	if _, ok := c.knownRoutes[key]; ok {
		return c.bootstrapContainsLocked(modelID)
	}
	return false
}

func (c *Catalog) resolveDefaultLocked(record accounts.Record, configured string, scoped bool) string {
	if configured != "" {
		if !scoped && c.visibleContainsLocked(configured) {
			return configured
		}
		if scoped && c.supportsRecordLocked(record, configured) {
			return configured
		}
	}
	for _, entry := range c.visible {
		if !entry.IsDefault {
			continue
		}
		if !scoped || c.supportsRecordLocked(record, entry.ID) {
			return entry.ID
		}
	}
	for _, entry := range c.visible {
		if !scoped || c.supportsRecordLocked(record, entry.ID) {
			return entry.ID
		}
	}
	for _, entry := range c.bootstrap {
		if !scoped || c.supportsRecordLocked(record, entry.ID) {
			return entry.ID
		}
	}
	return configured
}

func makeSet(ids []string) map[string]struct{} {
	out := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		out[id] = struct{}{}
	}
	return out
}

func cloneEntries(entries []Entry) []Entry {
	out := make([]Entry, 0, len(entries))
	for _, entry := range entries {
		out = append(out, cloneEntry(entry))
	}
	return out
}

func cloneEntry(entry Entry) Entry {
	cloned := entry
	if len(entry.SupportedReasoningEfforts) > 0 {
		cloned.SupportedReasoningEfforts = append([]ReasoningEffort(nil), entry.SupportedReasoningEfforts...)
	}
	return cloned
}
