package accounts

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"
)

type ServiceOptions struct {
	RateLimitFallback time.Duration
	QuotaFallback     time.Duration
}

type Service struct {
	mu                sync.RWMutex
	store             Store
	records           map[string]*Record
	rotationStrategy  RotationStrategy
	roundRobinIndex   int
	rateLimitFallback time.Duration
	quotaFallback     time.Duration
}

type Summary struct {
	Total                 int   `json:"total"`
	Active                int   `json:"active"`
	Disabled              int   `json:"disabled"`
	Expired               int   `json:"expired"`
	Banned                int   `json:"banned"`
	Available             int   `json:"available"`
	Blocked               int   `json:"blocked"`
	RateLimited           int   `json:"rate_limited"`
	QuotaPrimaryBlocked   int   `json:"quota_primary_blocked"`
	QuotaSecondaryBlocked int   `json:"quota_secondary_blocked"`
	CodeReviewBlocked     int   `json:"code_review_blocked"`
	TotalInputTokens      int64 `json:"total_input_tokens"`
	TotalOutputTokens     int64 `json:"total_output_tokens"`
	TotalRequests         int64 `json:"total_requests"`
	TotalEmptyResponses   int64 `json:"total_empty_responses"`
}

func NewService(accountsStore Store, defaultStrategy RotationStrategy, opts ServiceOptions) (*Service, error) {
	state, err := accountsStore.Load()
	if err != nil {
		return nil, err
	}

	rateLimitFallback := opts.RateLimitFallback
	if rateLimitFallback <= 0 {
		rateLimitFallback = 60 * time.Second
	}
	quotaFallback := opts.QuotaFallback
	if quotaFallback <= 0 {
		quotaFallback = 5 * time.Minute
	}

	svc := &Service{
		store:             accountsStore,
		records:           make(map[string]*Record),
		rateLimitFallback: rateLimitFallback,
		quotaFallback:     quotaFallback,
	}
	if state.RotationStrategy != "" {
		svc.rotationStrategy = state.RotationStrategy
	} else {
		svc.rotationStrategy = defaultStrategy
	}

	now := time.Now().UTC()
	needsPersist := false
	for _, record := range state.Records {
		cloned := cloneRecord(record)
		original := cloned
		svc.normalizeLoadedRecord(&cloned, now)
		if !reflect.DeepEqual(original, cloned) {
			needsPersist = true
		}
		svc.records[cloned.ID] = &cloned
	}
	if needsPersist {
		if err := svc.persistLocked(); err != nil {
			return nil, err
		}
	}
	return svc, nil
}

func (s *Service) List() []Record {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.refreshAllLocked(time.Now().UTC()) {
		_ = s.persistLocked()
	}
	items := make([]Record, 0, len(s.records))
	for _, record := range s.records {
		items = append(items, cloneRecord(record))
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].CreatedAt.Before(items[j].CreatedAt)
	})
	return items
}

func (s *Service) Get(id string) (Record, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.refreshAllLocked(time.Now().UTC()) {
		_ = s.persistLocked()
	}
	record, ok := s.records[id]
	if !ok {
		return Record{}, false
	}
	return cloneRecord(record), true
}

func (s *Service) UpsertFromToken(accountID string, token OAuthToken) (Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	metadata := metadataFromToken(token)

	for _, existing := range s.records {
		if existing.AccountID == accountID && existing.UserID == metadata.UserID {
			existing.Token = token
			existing.Email = metadata.Email
			existing.PlanType = metadata.PlanType
			existing.UserID = metadata.UserID
			existing.Status = StatusActive
			existing.LastError = ""
			existing.BlockState = BlockState{Reason: BlockNone}
			existing.UpdatedAt = time.Now().UTC()
			if err := s.persistLocked(); err != nil {
				return Record{}, err
			}
			return cloneRecord(existing), nil
		}
	}

	now := time.Now().UTC()
	record := &Record{
		ID:        "acct_" + randomHex(8),
		AccountID: accountID,
		UserID:    metadata.UserID,
		Email:     metadata.Email,
		PlanType:  metadata.PlanType,
		Status:    StatusActive,
		Token:     token,
		Cookies:   map[string]string{},
		BlockState: BlockState{
			Reason: BlockNone,
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
	s.records[record.ID] = record
	if err := s.persistLocked(); err != nil {
		return Record{}, err
	}
	return cloneRecord(record), nil
}

func (s *Service) Remove(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.records[id]; !ok {
		return fmt.Errorf("account not found")
	}
	delete(s.records, id)
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
		if *status == StatusActive {
			record.BlockState = BlockState{Reason: BlockNone}
			record.CachedQuota = nil
			record.LastError = ""
		} else {
			record.BlockState = BlockState{Reason: BlockNone}
		}
	}
	record.UpdatedAt = time.Now().UTC()
	if err := s.persistLocked(); err != nil {
		return Record{}, err
	}
	return cloneRecord(record), nil
}

func (s *Service) UpdateQuota(id string, quota *QuotaSnapshot) error {
	return s.ObserveQuota(id, quota)
}

func (s *Service) ObserveQuota(id string, quota *QuotaSnapshot) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	record, ok := s.records[id]
	if !ok {
		return fmt.Errorf("account not found")
	}
	if quota == nil {
		record.CachedQuota = nil
		record.UpdatedAt = time.Now().UTC()
		return s.persistLocked()
	}

	now := time.Now().UTC()
	cloned := cloneQuotaSnapshot(quota)
	normalizeQuotaSnapshot(&cloned, now)
	record.CachedQuota = &cloned
	if cloned.PlanType != "" && cloned.PlanType != "unknown" {
		record.PlanType = cloned.PlanType
	}
	record.UpdatedAt = now
	s.syncPrimaryWindowLocked(record, cloned.RateLimit.ResetAt, cloned.RateLimit.LimitWindowSeconds, now)
	s.syncBlockFromQuotaLocked(record, now)
	return s.persistLocked()
}

func (s *Service) SyncPrimaryWindow(id string, resetAt *time.Time, limitWindowSeconds *int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	record, ok := s.records[id]
	if !ok {
		return fmt.Errorf("account not found")
	}
	s.syncPrimaryWindowLocked(record, resetAt, limitWindowSeconds, time.Now().UTC())
	record.UpdatedAt = time.Now().UTC()
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
	if strings.TrimSpace(accountID) != "" {
		record.AccountID = strings.TrimSpace(accountID)
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
	record.UpdatedAt = time.Now().UTC()
	if record.Status != StatusDisabled && record.Status != StatusBanned {
		record.Status = StatusActive
	}
	record.LastError = ""
	return s.persistLocked()
}

func (s *Service) RecordUsage(id string, inputTokens, outputTokens int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	record, ok := s.records[id]
	if !ok {
		return fmt.Errorf("account not found")
	}
	now := time.Now().UTC()
	s.refreshUsageWindowLocked(record, now)
	record.LocalUsage.InputTokens += inputTokens
	record.LocalUsage.OutputTokens += outputTokens
	record.LocalUsage.RequestCount++
	record.LocalUsage.LastUsedAt = &now
	record.LocalUsage.WindowRequestCount++
	record.LocalUsage.WindowInputTokens += inputTokens
	record.LocalUsage.WindowOutputTokens += outputTokens
	record.UpdatedAt = now
	record.LastError = ""
	return s.persistLocked()
}

func (s *Service) RecordEmptyResponse(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	record, ok := s.records[id]
	if !ok {
		return fmt.Errorf("account not found")
	}
	record.LocalUsage.EmptyResponseCount++
	record.UpdatedAt = time.Now().UTC()
	return s.persistLocked()
}

func (s *Service) MarkBlocked(id string, reason BlockReason, until *time.Time, message string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	record, ok := s.records[id]
	if !ok {
		return fmt.Errorf("account not found")
	}
	now := time.Now().UTC()
	s.setBlockLocked(record, reason, until, message, now)
	record.UpdatedAt = now
	if message != "" {
		record.LastError = message
	}
	return s.persistLocked()
}

func (s *Service) ClearBlockIfExpired(id string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	record, ok := s.records[id]
	if !ok {
		return fmt.Errorf("account not found")
	}
	if s.clearExpiredBlockLocked(record, now) {
		record.UpdatedAt = now
		return s.persistLocked()
	}
	return nil
}

func (s *Service) MarkError(id string, status Status, message string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	record, ok := s.records[id]
	if !ok {
		return fmt.Errorf("account not found")
	}
	now := time.Now().UTC()
	switch status {
	case StatusActive, StatusDisabled, StatusExpired, StatusBanned:
		record.Status = status
		if status != StatusActive {
			record.BlockState = BlockState{Reason: BlockNone}
		}
	case Status("rate_limited"):
		s.setBlockLocked(record, BlockRateLimit, s.fallbackBlockUntil(BlockRateLimit, now), message, now)
	case Status("quota_exhausted"):
		until := blockUntilFromQuota(record.CachedQuota, BlockQuotaPrimary)
		if until == nil {
			until = s.fallbackBlockUntil(BlockQuotaPrimary, now)
		}
		s.setBlockLocked(record, BlockQuotaPrimary, until, message, now)
	default:
		record.Status = status
	}
	record.LastError = message
	record.UpdatedAt = now
	return s.persistLocked()
}

func (s *Service) Acquire(preferredID string) (Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	s.refreshAllLocked(now)

	if preferredID != "" {
		if record, ok := s.records[preferredID]; ok && isEligible(record, now) {
			return cloneRecord(record), nil
		}
	}

	candidates := make([]*Record, 0, len(s.records))
	for _, record := range s.records {
		if isEligible(record, now) {
			candidates = append(candidates, record)
		}
	}
	if len(candidates) == 0 {
		return Record{}, fmt.Errorf("no active accounts")
	}

	switch s.rotationStrategy {
	case RotationRoundRobin:
		sort.Slice(candidates, func(i, j int) bool { return candidates[i].ID < candidates[j].ID })
		record := candidates[s.roundRobinIndex%len(candidates)]
		s.roundRobinIndex++
		return cloneRecord(record), nil
	case RotationSticky:
		sort.Slice(candidates, func(i, j int) bool {
			return usageTime(candidates[i]).After(usageTime(candidates[j]))
		})
		return cloneRecord(candidates[0]), nil
	default:
		sort.Slice(candidates, func(i, j int) bool {
			return compareLeastUsed(candidates[i], candidates[j], now) < 0
		})
		tiedCount := 1
		for tiedCount < len(candidates) && compareLeastUsedTie(candidates[0], candidates[tiedCount], now) == 0 {
			tiedCount++
		}
		record := candidates[s.roundRobinIndex%tiedCount]
		s.roundRobinIndex++
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

func (s *Service) RefreshAvailability(now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refreshAllLocked(now)
	return s.persistLocked()
}

func (s *Service) Summary() Summary {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	if s.refreshAllLocked(now) {
		_ = s.persistLocked()
	}

	var summary Summary
	for _, record := range s.records {
		summary.Total++
		summary.TotalInputTokens += record.LocalUsage.InputTokens
		summary.TotalOutputTokens += record.LocalUsage.OutputTokens
		summary.TotalRequests += record.LocalUsage.RequestCount
		summary.TotalEmptyResponses += record.LocalUsage.EmptyResponseCount
		switch record.Status {
		case StatusActive:
			summary.Active++
			if isBlocked(record, now) {
				summary.Blocked++
				switch record.BlockState.Reason {
				case BlockRateLimit:
					summary.RateLimited++
				case BlockQuotaPrimary:
					summary.QuotaPrimaryBlocked++
				case BlockQuotaSecondary:
					summary.QuotaSecondaryBlocked++
				case BlockCodeReview:
					summary.CodeReviewBlocked++
				}
			} else {
				summary.Available++
			}
		case StatusDisabled:
			summary.Disabled++
		case StatusExpired:
			summary.Expired++
		case StatusBanned:
			summary.Banned++
		}
	}
	return summary
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
	if record.BlockState.Reason == "" {
		record.BlockState.Reason = BlockNone
	}

	switch record.Status {
	case Status("rate_limited"):
		record.Status = StatusActive
		record.BlockState = migratedBlockState(record, BlockRateLimit, s.rateLimitFallback, now)
	case Status("quota_exhausted"):
		record.Status = StatusActive
		reason := BlockQuotaPrimary
		if quotaWindowActive(record.CachedQuota, func(q *QuotaSnapshot) *RateLimitWindow { return q.SecondaryRateLimit }, now) {
			reason = BlockQuotaSecondary
		}
		record.BlockState = migratedBlockState(record, reason, s.quotaFallback, now)
	case StatusActive, StatusDisabled, StatusExpired, StatusBanned:
	default:
		record.Status = StatusActive
	}

	if record.CachedQuota != nil {
		normalizeQuotaSnapshot(record.CachedQuota, now)
	}
	s.refreshUsageWindowLocked(record, now)
	s.clearExpiredBlockLocked(record, now)
	s.syncBlockFromQuotaLocked(record, now)
}

func migratedBlockState(record *Record, reason BlockReason, fallback time.Duration, now time.Time) BlockState {
	block := BlockState{
		Reason:     reason,
		ObservedAt: timePtrValue(now),
		Message:    record.LastError,
	}
	if until := blockUntilFromQuota(record.CachedQuota, reason); until != nil {
		block.Until = until
		return block
	}
	if fallback > 0 {
		block.Until = timePtrValue(now.Add(fallback))
	}
	return block
}

func (s *Service) refreshAllLocked(now time.Time) bool {
	changed := false
	for _, record := range s.records {
		recordChanged := false
		if record.CachedQuota != nil {
			if normalizeQuotaSnapshot(record.CachedQuota, now) {
				recordChanged = true
			}
		}
		if s.refreshUsageWindowLocked(record, now) {
			recordChanged = true
		}
		if s.clearExpiredBlockLocked(record, now) {
			recordChanged = true
		}
		if s.syncBlockFromQuotaLocked(record, now) {
			recordChanged = true
		}
		if recordChanged {
			record.UpdatedAt = now
			changed = true
		}
	}
	return changed
}

func (s *Service) refreshUsageWindowLocked(record *Record, now time.Time) bool {
	if record == nil || record.LocalUsage.WindowResetAt == nil {
		return false
	}
	if now.Before(*record.LocalUsage.WindowResetAt) {
		return false
	}
	record.LocalUsage.WindowRequestCount = 0
	record.LocalUsage.WindowInputTokens = 0
	record.LocalUsage.WindowOutputTokens = 0
	record.LocalUsage.WindowCountersResetAt = timePtrValue(now)

	if record.LocalUsage.LimitWindowSeconds != nil && *record.LocalUsage.LimitWindowSeconds > 0 {
		next := record.LocalUsage.WindowResetAt.Add(time.Duration(*record.LocalUsage.LimitWindowSeconds) * time.Second)
		for !next.After(now) {
			next = next.Add(time.Duration(*record.LocalUsage.LimitWindowSeconds) * time.Second)
		}
		record.LocalUsage.WindowResetAt = &next
	} else {
		record.LocalUsage.WindowResetAt = nil
	}
	return true
}

func (s *Service) syncPrimaryWindowLocked(record *Record, resetAt *time.Time, limitWindowSeconds *int, now time.Time) {
	if record == nil {
		return
	}
	s.refreshUsageWindowLocked(record, now)
	if shouldResetWindowCounters(record.LocalUsage, resetAt, limitWindowSeconds) {
		record.LocalUsage.WindowRequestCount = 0
		record.LocalUsage.WindowInputTokens = 0
		record.LocalUsage.WindowOutputTokens = 0
		record.LocalUsage.WindowCountersResetAt = timePtrValue(now)
	}
	if resetAt != nil {
		ts := resetAt.UTC()
		record.LocalUsage.WindowResetAt = &ts
	}
	if limitWindowSeconds != nil {
		seconds := *limitWindowSeconds
		record.LocalUsage.LimitWindowSeconds = &seconds
	}
	if record.LocalUsage.WindowCountersResetAt == nil {
		record.LocalUsage.WindowCountersResetAt = timePtrValue(now)
	}
}

func (s *Service) syncBlockFromQuotaLocked(record *Record, now time.Time) bool {
	if record == nil || record.Status != StatusActive {
		return false
	}
	if reason, until := activeBlockFromQuota(record.CachedQuota, now); reason != BlockNone {
		if record.BlockState.Reason == reason &&
			timesEqual(record.BlockState.Until, until) &&
			strings.TrimSpace(record.BlockState.Message) == strings.TrimSpace(record.LastError) {
			return false
		}
		if until == nil {
			until = s.fallbackBlockUntil(reason, now)
		}
		s.setBlockLocked(record, reason, until, record.LastError, now)
		return true
	}
	if isSnapshotManagedBlockReason(record.BlockState.Reason) && shouldClearBlockFromSnapshot(record) {
		record.BlockState = BlockState{Reason: BlockNone}
		return true
	}
	return false
}

func (s *Service) setBlockLocked(record *Record, reason BlockReason, until *time.Time, message string, now time.Time) {
	if record == nil {
		return
	}
	if until != nil {
		value := until.UTC()
		until = &value
	}
	record.BlockState = BlockState{
		Reason:     reason,
		Until:      until,
		ObservedAt: timePtrValue(now),
		Message:    strings.TrimSpace(message),
	}
}

func (s *Service) clearExpiredBlockLocked(record *Record, now time.Time) bool {
	if record == nil || record.BlockState.Reason == BlockNone || record.BlockState.Until == nil {
		return false
	}
	if now.Before(*record.BlockState.Until) {
		return false
	}
	record.BlockState = BlockState{Reason: BlockNone}
	return true
}

func compareLeastUsed(a, b *Record, now time.Time) int {
	if cmp := compareLeastUsedTie(a, b, now); cmp != 0 {
		return cmp
	}
	return strings.Compare(a.ID, b.ID)
}

func compareLeastUsedTie(a, b *Record, now time.Time) int {
	aState := effectiveQuotaState(a, now)
	bState := effectiveQuotaState(b, now)

	switch {
	case aState.exhaustionRank < bState.exhaustionRank:
		return -1
	case aState.exhaustionRank > bState.exhaustionRank:
		return 1
	}

	if aState.nextReset != nil && bState.nextReset != nil && !aState.nextReset.Equal(*bState.nextReset) {
		if aState.nextReset.Before(*bState.nextReset) {
			return -1
		}
		return 1
	}

	switch {
	case aState.pressure < bState.pressure:
		return -1
	case aState.pressure > bState.pressure:
		return 1
	}

	switch {
	case a.LocalUsage.RequestCount < b.LocalUsage.RequestCount:
		return -1
	case a.LocalUsage.RequestCount > b.LocalUsage.RequestCount:
		return 1
	}

	aTime := usageTime(a)
	bTime := usageTime(b)
	switch {
	case aTime.Before(bTime):
		return -1
	case aTime.After(bTime):
		return 1
	default:
		return 0
	}
}

type quotaState struct {
	exhaustionRank int
	pressure       float64
	nextReset      *time.Time
}

func effectiveQuotaState(record *Record, now time.Time) quotaState {
	state := quotaState{}
	if record == nil || record.CachedQuota == nil {
		return state
	}
	windows := []*RateLimitWindow{
		&record.CachedQuota.RateLimit,
		record.CachedQuota.SecondaryRateLimit,
		record.CachedQuota.CodeReviewRateLimit,
	}
	for _, window := range windows {
		if window == nil || window.UsedPercent == nil {
			continue
		}
		if window.ResetAt != nil && !window.ResetAt.After(now) {
			continue
		}
		if *window.UsedPercent > state.pressure {
			state.pressure = *window.UsedPercent
		}
		if window.LimitReached {
			state.exhaustionRank++
		}
		if window.ResetAt != nil {
			if state.nextReset == nil || window.ResetAt.Before(*state.nextReset) {
				reset := window.ResetAt.UTC()
				state.nextReset = &reset
			}
		}
	}
	return state
}

func activeBlockFromQuota(snapshot *QuotaSnapshot, now time.Time) (BlockReason, *time.Time) {
	if snapshot == nil {
		return BlockNone, nil
	}
	if snapshot.SecondaryRateLimit != nil && windowLimitActive(snapshot.SecondaryRateLimit, now) {
		return BlockQuotaSecondary, cloneTime(snapshot.SecondaryRateLimit.ResetAt)
	}
	if snapshot.CodeReviewRateLimit != nil && windowLimitActive(snapshot.CodeReviewRateLimit, now) {
		return BlockCodeReview, cloneTime(snapshot.CodeReviewRateLimit.ResetAt)
	}
	if windowLimitActive(&snapshot.RateLimit, now) {
		return BlockQuotaPrimary, cloneTime(snapshot.RateLimit.ResetAt)
	}
	return BlockNone, nil
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

func isQuotaBlockReason(reason BlockReason) bool {
	switch reason {
	case BlockQuotaPrimary, BlockQuotaSecondary, BlockCodeReview:
		return true
	default:
		return false
	}
}

func isSnapshotManagedBlockReason(reason BlockReason) bool {
	return isQuotaBlockReason(reason)
}

func shouldClearBlockFromSnapshot(record *Record) bool {
	if record == nil || !isSnapshotManagedBlockReason(record.BlockState.Reason) {
		return false
	}
	if record.CachedQuota == nil || record.CachedQuota.FetchedAt.IsZero() {
		return false
	}
	if record.BlockState.ObservedAt == nil {
		return true
	}
	return !record.CachedQuota.FetchedAt.Before(*record.BlockState.ObservedAt)
}

func blockUntilFromQuota(snapshot *QuotaSnapshot, reason BlockReason) *time.Time {
	if snapshot == nil {
		return nil
	}
	switch reason {
	case BlockQuotaSecondary:
		return cloneTimePtr(snapshot.SecondaryRateLimit)
	case BlockCodeReview:
		return cloneTimePtr(snapshot.CodeReviewRateLimit)
	case BlockQuotaPrimary:
		return cloneTime(snapshot.RateLimit.ResetAt)
	default:
		return nil
	}
}

func quotaWindowActive(snapshot *QuotaSnapshot, getter func(*QuotaSnapshot) *RateLimitWindow, now time.Time) bool {
	if snapshot == nil {
		return false
	}
	window := getter(snapshot)
	return windowLimitActive(window, now)
}

func cloneTimePtr(window *RateLimitWindow) *time.Time {
	if window == nil {
		return nil
	}
	return cloneTime(window.ResetAt)
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	ts := value.UTC()
	return &ts
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
	if window == nil || window.ResetAt == nil {
		return false
	}
	if window.ResetAt.After(now) {
		return false
	}
	window.LimitReached = false
	window.ResetAt = nil
	window.UsedPercent = nil
	return true
}

func (s *Service) fallbackBlockUntil(reason BlockReason, now time.Time) *time.Time {
	duration := s.quotaFallback
	if reason == BlockRateLimit {
		duration = s.rateLimitFallback
	}
	if duration <= 0 {
		return nil
	}
	return timePtrValue(now.Add(duration))
}

func shouldResetWindowCounters(usage LocalUsage, resetAt *time.Time, limitWindowSeconds *int) bool {
	if resetAt == nil || usage.WindowResetAt == nil {
		return false
	}
	newResetAt := resetAt.UTC()
	oldResetAt := usage.WindowResetAt.UTC()
	if newResetAt.Equal(oldResetAt) {
		return false
	}

	windowSeconds := 0
	switch {
	case limitWindowSeconds != nil && *limitWindowSeconds > 0:
		windowSeconds = *limitWindowSeconds
	case usage.LimitWindowSeconds != nil && *usage.LimitWindowSeconds > 0:
		windowSeconds = *usage.LimitWindowSeconds
	}

	threshold := 3600 * time.Second
	if windowSeconds > 0 {
		threshold = time.Duration(windowSeconds) * time.Second / 2
	}

	drift := newResetAt.Sub(oldResetAt)
	if drift < 0 {
		drift = -drift
	}
	return drift >= threshold
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

func isEligible(record *Record, now time.Time) bool {
	return record != nil && record.Status == StatusActive && !isBlocked(record, now)
}

func isBlocked(record *Record, now time.Time) bool {
	if record == nil || record.BlockState.Reason == BlockNone {
		return false
	}
	if record.BlockState.Until == nil {
		return true
	}
	return record.BlockState.Until.After(now)
}

func usageTime(record *Record) time.Time {
	if record.LocalUsage.LastUsedAt != nil {
		return *record.LocalUsage.LastUsedAt
	}
	return time.Time{}
}

func cloneRecord(record *Record) Record {
	cloned := *record
	cloned.Cookies = cloneCookies(record.Cookies)
	if record.CachedQuota != nil {
		quota := cloneQuotaSnapshot(record.CachedQuota)
		cloned.CachedQuota = &quota
	}
	cloned.BlockState = cloneBlockState(record.BlockState)
	cloned.LocalUsage = cloneLocalUsage(record.LocalUsage)
	return cloned
}

func cloneLocalUsage(usage LocalUsage) LocalUsage {
	cloned := usage
	if usage.LastUsedAt != nil {
		ts := usage.LastUsedAt.UTC()
		cloned.LastUsedAt = &ts
	}
	if usage.WindowResetAt != nil {
		ts := usage.WindowResetAt.UTC()
		cloned.WindowResetAt = &ts
	}
	if usage.LimitWindowSeconds != nil {
		value := *usage.LimitWindowSeconds
		cloned.LimitWindowSeconds = &value
	}
	if usage.WindowCountersResetAt != nil {
		ts := usage.WindowCountersResetAt.UTC()
		cloned.WindowCountersResetAt = &ts
	}
	return cloned
}

func cloneBlockState(state BlockState) BlockState {
	cloned := state
	cloned.Until = cloneTime(state.Until)
	cloned.ObservedAt = cloneTime(state.ObservedAt)
	return cloned
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

func randomHex(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		panic(fmt.Errorf("crypto/rand failed: %w", err))
	}
	return hex.EncodeToString(buf)
}

func timePtrValue(value time.Time) *time.Time {
	ts := value.UTC()
	return &ts
}

func timesEqual(a, b *time.Time) bool {
	switch {
	case a == nil && b == nil:
		return true
	case a == nil || b == nil:
		return false
	default:
		return a.UTC().Equal(b.UTC())
	}
}
