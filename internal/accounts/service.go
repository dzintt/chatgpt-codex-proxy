package accounts

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	DefaultRateLimitFallback = 60 * time.Second
	DefaultQuotaFallback     = 5 * time.Minute
)

type RotationStrategy string

const (
	RotationLeastUsed  RotationStrategy = "least_used"
	RotationRoundRobin RotationStrategy = "round_robin"
	RotationSticky     RotationStrategy = "sticky"
)

type Service struct {
	mu               sync.RWMutex
	store            Store
	records          map[string]*Record
	rotationStrategy RotationStrategy
	roundRobinIndex  int
	stickyAccountID  string
}

var accountIDSequence uint64

func NewService(accountsStore Store, defaultStrategy RotationStrategy) (*Service, error) {
	state, err := accountsStore.Load()
	if err != nil {
		return nil, err
	}

	svc := &Service{
		store:   accountsStore,
		records: make(map[string]*Record),
	}
	if state.RotationStrategy != "" {
		svc.rotationStrategy = state.RotationStrategy
	} else {
		svc.rotationStrategy = defaultStrategy
	}

	now := time.Now().UTC()
	needsPersist := false
	for _, stored := range state.Records {
		record := cloneRecord(stored)
		original := cloneRecord(&record)
		svc.normalizeLoadedRecord(&record, now)
		if !reflect.DeepEqual(original, record) {
			needsPersist = true
		}
		svc.records[record.ID] = &record
	}
	if needsPersist {
		if err := svc.persistLocked(); err != nil {
			return nil, err
		}
	}

	return svc, nil
}

func (s *Service) List() ([]Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.refreshAllLocked(time.Now().UTC()); err != nil {
		return nil, err
	}

	items := make([]Record, 0, len(s.records))
	for _, record := range s.records {
		items = append(items, cloneRecord(record))
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].ID < items[j].ID
		}
		return items[i].CreatedAt.Before(items[j].CreatedAt)
	})
	return items, nil
}

func (s *Service) Get(id string) (Record, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.refreshAllLocked(time.Now().UTC()); err != nil {
		return Record{}, false, err
	}

	record, ok := s.records[id]
	if !ok {
		return Record{}, false, nil
	}
	return cloneRecord(record), true, nil
}

func (s *Service) EligibleNow(id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	if err := s.refreshAllLocked(now); err != nil {
		return false, err
	}

	record, ok := s.records[id]
	return ok && isEligible(record, now), nil
}

func (s *Service) UpsertFromToken(accountID string, token OAuthToken) (Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	metadata := metadataFromToken(token)
	for _, existing := range s.records {
		if sameAccount(existing, accountID, metadata.UserID) {
			existing.Token = token
			existing.Email = metadata.Email
			existing.PlanType = metadata.PlanType
			existing.UserID = metadata.UserID
			existing.Status = StatusActive
			existing.LastError = ""
			existing.CooldownUntil = nil
			existing.UpdatedAt = time.Now().UTC()
			if err := s.persistLocked(); err != nil {
				return Record{}, err
			}
			return cloneRecord(existing), nil
		}
	}

	now := time.Now().UTC()
	record := &Record{
		ID:        "acct_" + nextAccountID(),
		AccountID: accountID,
		UserID:    metadata.UserID,
		Email:     metadata.Email,
		PlanType:  metadata.PlanType,
		Status:    StatusActive,
		Token:     token,
		Cookies:   map[string]string{},
		CreatedAt: now,
		UpdatedAt: now,
	}
	s.records[record.ID] = record
	if err := s.persistLocked(); err != nil {
		return Record{}, err
	}
	return cloneRecord(record), nil
}

func sameAccount(existing *Record, accountID, userID string) bool {
	if existing == nil || existing.AccountID != accountID {
		return false
	}
	existingUserID := strings.TrimSpace(existing.UserID)
	newUserID := strings.TrimSpace(userID)
	if existingUserID == "" || newUserID == "" {
		return true
	}
	return existingUserID == newUserID
}

func (s *Service) Remove(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.records[id]; !ok {
		return fmt.Errorf("account not found")
	}
	delete(s.records, id)
	if s.stickyAccountID == id {
		s.stickyAccountID = ""
	}
	return s.persistLocked()
}

func (s *Service) Patch(id string, label *string, status *Status) (Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	record, ok := s.records[id]
	if !ok {
		return Record{}, fmt.Errorf("account not found")
	}
	if label != nil {
		record.Label = strings.TrimSpace(*label)
	}
	if status != nil {
		record.Status = *status
		switch *status {
		case StatusActive:
			record.CooldownUntil = nil
			record.LastError = ""
			record.CachedQuota = nil
		case StatusDisabled:
			record.CooldownUntil = nil
			record.LastError = ""
		}
	}
	record.UpdatedAt = time.Now().UTC()
	if err := s.persistLocked(); err != nil {
		return Record{}, err
	}
	return cloneRecord(record), nil
}

func (s *Service) ObserveQuota(id string, quota *QuotaSnapshot) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	record, ok := s.records[id]
	if !ok {
		return fmt.Errorf("account not found")
	}

	now := time.Now().UTC()
	if quota == nil {
		record.CachedQuota = nil
		record.UpdatedAt = now
		return s.persistLocked()
	}

	cloned := cloneQuotaSnapshot(quota)
	normalizeQuotaSnapshot(&cloned, now)
	record.CachedQuota = &cloned
	if plan := strings.TrimSpace(cloned.PlanType); plan != "" && plan != "unknown" {
		record.PlanType = plan
	}
	if !quotaBlocksGeneralRouting(record.CachedQuota, now) {
		record.CooldownUntil = nil
	}
	record.UpdatedAt = now
	return s.persistLocked()
}

func (s *Service) UpdateAuth(id, accountID string, token OAuthToken) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	record, ok := s.records[id]
	if !ok {
		return fmt.Errorf("account not found")
	}

	metadata := metadataFromToken(token)
	if trimmed := strings.TrimSpace(accountID); trimmed != "" {
		record.AccountID = trimmed
	}
	record.Token = token
	if metadata.Email != "" {
		record.Email = metadata.Email
	}
	if metadata.PlanType != "" {
		record.PlanType = metadata.PlanType
	}
	if metadata.UserID != "" {
		record.UserID = metadata.UserID
	}
	if record.Status != StatusDisabled && record.Status != StatusBanned {
		record.Status = StatusActive
	}
	record.LastError = ""
	record.UpdatedAt = time.Now().UTC()
	return s.persistLocked()
}

func (s *Service) MarkError(id string, status Status, message string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	record, ok := s.records[id]
	if !ok {
		return fmt.Errorf("account not found")
	}
	record.Status = status
	record.LastError = strings.TrimSpace(message)
	if status != StatusActive {
		record.CooldownUntil = nil
	}
	record.UpdatedAt = time.Now().UTC()
	return s.persistLocked()
}

func (s *Service) SetCooldown(id string, until *time.Time, message string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	record, ok := s.records[id]
	if !ok {
		return fmt.Errorf("account not found")
	}
	if until != nil {
		value := until.UTC()
		until = &value
	}
	record.CooldownUntil = until
	record.LastError = strings.TrimSpace(message)
	record.UpdatedAt = time.Now().UTC()
	return s.persistLocked()
}

func (s *Service) NoteSuccess(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.records[id]; !ok {
		return
	}
	s.stickyAccountID = id
}

func (s *Service) Acquire(preferredID string) (Record, error) {
	return s.AcquireMatching(preferredID, nil)
}

func (s *Service) AcquireMatching(preferredID string, allow func(Record) bool) (Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	if err := s.refreshAllLocked(now); err != nil {
		return Record{}, err
	}

	if preferredID != "" {
		if record, ok := s.records[preferredID]; ok && isEligible(record, now) {
			candidate := cloneRecord(record)
			if allow == nil || allow(candidate) {
				return candidate, nil
			}
		}
	}

	candidates := make([]*Record, 0, len(s.records))
	for _, record := range s.records {
		if isEligible(record, now) {
			candidate := cloneRecord(record)
			if allow != nil && !allow(candidate) {
				continue
			}
			recordCopy := candidate
			candidates = append(candidates, &recordCopy)
		}
	}
	if len(candidates) == 0 {
		return Record{}, fmt.Errorf("no active accounts")
	}

	switch s.rotationStrategy {
	case RotationRoundRobin:
		record := selectRoundRobin(candidates, &s.roundRobinIndex)
		return cloneRecord(record), nil
	case RotationSticky:
		if sticky := s.selectStickyLocked(candidates, now); sticky != nil {
			return cloneRecord(sticky), nil
		}
		record := selectLeastUsed(candidates, &s.roundRobinIndex)
		return cloneRecord(record), nil
	default:
		record := selectLeastUsed(candidates, &s.roundRobinIndex)
		return cloneRecord(record), nil
	}
}

func (s *Service) RotationStrategy() RotationStrategy {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.rotationStrategy
}

func (s *Service) SetRotationStrategy(strategy RotationStrategy) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rotationStrategy = strategy
	return s.persistLocked()
}

func (s *Service) selectStickyLocked(candidates []*Record, now time.Time) *Record {
	if s.stickyAccountID == "" {
		return nil
	}
	for _, candidate := range candidates {
		if candidate.ID == s.stickyAccountID && isEligible(candidate, now) {
			return candidate
		}
	}
	return nil
}

func (s *Service) persistLocked() error {
	records := make([]*Record, 0, len(s.records))
	for _, record := range s.records {
		cloned := cloneRecord(record)
		records = append(records, &cloned)
	}
	return s.store.Save(State{
		Records:          records,
		RotationStrategy: s.rotationStrategy,
	})
}

func (s *Service) normalizeLoadedRecord(record *Record, now time.Time) {
	if record == nil {
		return
	}

	metadata := metadataFromToken(record.Token)
	if record.Status == "" {
		record.Status = StatusActive
	}
	switch record.Status {
	case StatusActive, StatusDisabled, StatusExpired, StatusBanned:
	default:
		record.Status = StatusActive
	}
	if record.CreatedAt.IsZero() {
		record.CreatedAt = now
	}
	if record.UpdatedAt.IsZero() {
		record.UpdatedAt = record.CreatedAt
	}
	if record.Cookies == nil {
		record.Cookies = map[string]string{}
	}
	if record.UserID == "" && strings.TrimSpace(metadata.UserID) != "" {
		record.UserID = strings.TrimSpace(metadata.UserID)
	}
	if record.Email == "" && strings.TrimSpace(metadata.Email) != "" {
		record.Email = strings.TrimSpace(metadata.Email)
	}
	if record.PlanType == "" && strings.TrimSpace(metadata.PlanType) != "" {
		record.PlanType = strings.TrimSpace(metadata.PlanType)
	}
	if record.CachedQuota != nil {
		normalizeQuotaSnapshot(record.CachedQuota, now)
	}
	clearExpiredCooldownLocked(record, now)
}

func (s *Service) refreshAllLocked(now time.Time) error {
	changed := false
	for _, record := range s.records {
		recordChanged := false
		if record.CachedQuota != nil && normalizeQuotaSnapshot(record.CachedQuota, now) {
			recordChanged = true
		}
		if clearExpiredCooldownLocked(record, now) {
			recordChanged = true
		}
		if recordChanged {
			record.UpdatedAt = now
			changed = true
		}
	}
	if !changed {
		return nil
	}
	return s.persistLocked()
}

func clearExpiredCooldownLocked(record *Record, now time.Time) bool {
	if record == nil || record.CooldownUntil == nil {
		return false
	}
	if record.CooldownUntil.After(now) {
		return false
	}
	record.CooldownUntil = nil
	return true
}

func selectRoundRobin(candidates []*Record, index *int) *Record {
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].ID < candidates[j].ID })
	selected := candidates[*index%len(candidates)]
	*index = *index + 1
	return selected
}

func selectLeastUsed(candidates []*Record, index *int) *Record {
	withQuota := make([]*Record, 0, len(candidates))
	for _, candidate := range candidates {
		if hasUsableQuota(candidate) {
			withQuota = append(withQuota, candidate)
			continue
		}
	}

	if len(withQuota) == 0 {
		return selectRoundRobin(candidates, index)
	}

	sort.Slice(withQuota, func(i, j int) bool {
		if cmp := compareLeastUsedQuota(withQuota[i], withQuota[j]); cmp != 0 {
			return cmp < 0
		}
		return withQuota[i].ID < withQuota[j].ID
	})

	tiedCount := 1
	for tiedCount < len(withQuota) && compareLeastUsedQuota(withQuota[0], withQuota[tiedCount]) == 0 {
		tiedCount++
	}
	selected := withQuota[*index%tiedCount]
	*index = *index + 1
	return selected
}

func compareLeastUsedQuota(a, b *Record) int {
	aQuota := a.CachedQuota
	bQuota := b.CachedQuota

	aPrimary := quotaPercent(aQuota, primaryWindow)
	bPrimary := quotaPercent(bQuota, primaryWindow)
	switch {
	case aPrimary < bPrimary:
		return -1
	case aPrimary > bPrimary:
		return 1
	}

	aSecondary, aHasSecondary := secondaryPercent(aQuota)
	bSecondary, bHasSecondary := secondaryPercent(bQuota)
	if aHasSecondary && bHasSecondary {
		switch {
		case aSecondary < bSecondary:
			return -1
		case aSecondary > bSecondary:
			return 1
		}
	}

	aReset, aHasReset := primaryReset(aQuota)
	bReset, bHasReset := primaryReset(bQuota)
	if aHasReset && bHasReset && !aReset.Equal(bReset) {
		if aReset.Before(bReset) {
			return -1
		}
		return 1
	}

	return 0
}

func primaryWindow(snapshot *QuotaSnapshot) *RateLimitWindow {
	if snapshot == nil {
		return nil
	}
	return &snapshot.RateLimit
}

func quotaPercent(snapshot *QuotaSnapshot, getter func(*QuotaSnapshot) *RateLimitWindow) float64 {
	window := getter(snapshot)
	if window == nil || window.UsedPercent == nil {
		return 0
	}
	return *window.UsedPercent
}

func secondaryPercent(snapshot *QuotaSnapshot) (float64, bool) {
	if snapshot == nil || snapshot.SecondaryRateLimit == nil || snapshot.SecondaryRateLimit.UsedPercent == nil {
		return 0, false
	}
	return *snapshot.SecondaryRateLimit.UsedPercent, true
}

func primaryReset(snapshot *QuotaSnapshot) (time.Time, bool) {
	if snapshot == nil || snapshot.RateLimit.ResetAt == nil {
		return time.Time{}, false
	}
	return snapshot.RateLimit.ResetAt.UTC(), true
}

func hasUsableQuota(record *Record) bool {
	return record != nil && record.CachedQuota != nil && record.CachedQuota.RateLimit.UsedPercent != nil
}

func normalizeQuotaSnapshot(snapshot *QuotaSnapshot, now time.Time) bool {
	if snapshot == nil {
		return false
	}
	changed := false
	if normalizeRateLimitWindow(&snapshot.RateLimit, now) {
		changed = true
	}
	if normalizeRateLimitWindow(snapshot.SecondaryRateLimit, now) {
		changed = true
	}
	if normalizeRateLimitWindow(snapshot.CodeReviewRateLimit, now) {
		changed = true
	}
	return changed
}

func normalizeRateLimitWindow(window *RateLimitWindow, now time.Time) bool {
	if window == nil || window.ResetAt == nil || window.ResetAt.After(now) {
		return false
	}
	window.Allowed = true
	window.LimitReached = false
	window.UsedPercent = nil
	window.ResetAt = nil
	return true
}

func quotaBlocksGeneralRouting(snapshot *QuotaSnapshot, now time.Time) bool {
	if snapshot == nil {
		return false
	}
	if windowAvailabilityBlocked(&snapshot.RateLimit, now) {
		return true
	}
	if windowLimitActive(&snapshot.RateLimit, now) {
		return true
	}
	return windowLimitActive(snapshot.SecondaryRateLimit, now)
}

func windowAvailabilityBlocked(window *RateLimitWindow, now time.Time) bool {
	if window == nil || window.Allowed {
		return false
	}
	if window.ResetAt == nil {
		return true
	}
	return window.ResetAt.After(now)
}

func windowLimitActive(window *RateLimitWindow, now time.Time) bool {
	if window == nil || !window.LimitReached {
		return false
	}
	if window.ResetAt == nil {
		return true
	}
	return window.ResetAt.After(now)
}

func isEligible(record *Record, now time.Time) bool {
	if record == nil || record.Status != StatusActive {
		return false
	}
	if strings.TrimSpace(record.Token.AccessToken) == "" {
		return false
	}
	if record.CooldownUntil != nil && record.CooldownUntil.After(now) {
		return false
	}
	return !quotaBlocksGeneralRouting(record.CachedQuota, now)
}

func cloneRecord(record *Record) Record {
	cloned := *record
	cloned.Cookies = cloneCookies(record.Cookies)
	cloned.CooldownUntil = cloneTime(record.CooldownUntil)
	if record.CachedQuota != nil {
		quota := cloneQuotaSnapshot(record.CachedQuota)
		cloned.CachedQuota = &quota
	}
	return cloned
}

func cloneQuotaSnapshot(snapshot *QuotaSnapshot) QuotaSnapshot {
	cloned := *snapshot
	cloned.RateLimit = cloneRateLimitWindow(&snapshot.RateLimit)
	cloned.SecondaryRateLimit = cloneRateLimitWindowPtr(snapshot.SecondaryRateLimit)
	cloned.CodeReviewRateLimit = cloneRateLimitWindowPtr(snapshot.CodeReviewRateLimit)
	if snapshot.Credits != nil {
		credits := *snapshot.Credits
		if credits.Balance != nil {
			value := *credits.Balance
			credits.Balance = &value
		}
		cloned.Credits = &credits
	}
	return cloned
}

func cloneRateLimitWindowPtr(window *RateLimitWindow) *RateLimitWindow {
	if window == nil {
		return nil
	}
	cloned := cloneRateLimitWindow(window)
	return &cloned
}

func cloneRateLimitWindow(window *RateLimitWindow) RateLimitWindow {
	cloned := *window
	if window.UsedPercent != nil {
		value := *window.UsedPercent
		cloned.UsedPercent = &value
	}
	if window.ResetAt != nil {
		ts := window.ResetAt.UTC()
		cloned.ResetAt = &ts
	}
	if window.LimitWindowSeconds != nil {
		value := *window.LimitWindowSeconds
		cloned.LimitWindowSeconds = &value
	}
	return cloned
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	ts := value.UTC()
	return &ts
}

func cloneCookies(cookies map[string]string) map[string]string {
	if len(cookies) == 0 {
		return map[string]string{}
	}
	out := make(map[string]string, len(cookies))
	for key, value := range cookies {
		out[key] = value
	}
	return out
}

func nextAccountID() string {
	return fmt.Sprintf("%d_%08x", time.Now().UTC().UnixNano(), atomic.AddUint64(&accountIDSequence, 1))
}
