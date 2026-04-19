package accounts

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

type Service struct {
	mu               sync.RWMutex
	store            Store
	records          map[string]*Record
	rotationStrategy RotationStrategy
	roundRobinIndex  int
}

type Summary struct {
	Total             int   `json:"total"`
	Active            int   `json:"active"`
	Disabled          int   `json:"disabled"`
	Expired           int   `json:"expired"`
	RateLimited       int   `json:"rate_limited"`
	QuotaExhausted    int   `json:"quota_exhausted"`
	TotalInputTokens  int64 `json:"total_input_tokens"`
	TotalOutputTokens int64 `json:"total_output_tokens"`
	TotalRequests     int64 `json:"total_requests"`
}

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
	for _, record := range state.Records {
		cloned := cloneRecord(record)
		svc.records[cloned.ID] = &cloned
	}
	return svc, nil
}

func (s *Service) List() []Record {
	s.mu.RLock()
	defer s.mu.RUnlock()

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
	s.mu.RLock()
	defer s.mu.RUnlock()
	record, ok := s.records[id]
	if !ok {
		return Record{}, false
	}
	cloned := cloneRecord(record)
	return cloned, true
}

func (s *Service) UpsertFromToken(accountID string, token OAuthToken) (Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	metadata := metadataFromToken(token)

	for _, existing := range s.records {
		if existing.AccountID == accountID {
			existing.Token = token
			existing.Email = metadata.Email
			existing.PlanType = metadata.PlanType
			existing.Status = StatusActive
			existing.LastError = ""
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
	}
	record.UpdatedAt = time.Now().UTC()
	if err := s.persistLocked(); err != nil {
		return Record{}, err
	}
	return cloneRecord(record), nil
}

func (s *Service) SetCookies(id string, cookies map[string]string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	record, ok := s.records[id]
	if !ok {
		return fmt.Errorf("account not found")
	}
	record.Cookies = cloneCookies(cookies)
	record.UpdatedAt = time.Now().UTC()
	return s.persistLocked()
}

func (s *Service) UpdateQuota(id string, quota *QuotaSnapshot) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.records[id]
	if !ok {
		return fmt.Errorf("account not found")
	}
	if quota == nil {
		record.CachedQuota = nil
	} else {
		cloned := *quota
		record.CachedQuota = &cloned
	}
	record.UpdatedAt = time.Now().UTC()
	return s.persistLocked()
}

func (s *Service) UpdateToken(id string, token OAuthToken) error {
	return s.UpdateAuth(id, "", token)
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
	record.UpdatedAt = time.Now().UTC()
	record.Status = StatusActive
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
	record.LocalUsage.InputTokens += inputTokens
	record.LocalUsage.OutputTokens += outputTokens
	record.LocalUsage.RequestCount++
	record.LocalUsage.LastUsedAt = &now
	record.UpdatedAt = now
	record.Status = StatusActive
	record.LastError = ""
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
	record.LastError = message
	record.UpdatedAt = time.Now().UTC()
	return s.persistLocked()
}

func (s *Service) Acquire(preferredID string) (Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if preferredID != "" {
		if record, ok := s.records[preferredID]; ok && record.Status == StatusActive {
			return cloneRecord(record), nil
		}
	}

	candidates := make([]*Record, 0, len(s.records))
	for _, record := range s.records {
		if record.Status == StatusActive {
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
			return compareLeastUsed(candidates[i], candidates[j]) < 0
		})
		return cloneRecord(candidates[0]), nil
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

func (s *Service) Summary() Summary {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var summary Summary
	for _, record := range s.records {
		summary.Total++
		summary.TotalInputTokens += record.LocalUsage.InputTokens
		summary.TotalOutputTokens += record.LocalUsage.OutputTokens
		summary.TotalRequests += record.LocalUsage.RequestCount
		switch record.Status {
		case StatusActive:
			summary.Active++
		case StatusDisabled:
			summary.Disabled++
		case StatusExpired:
			summary.Expired++
		case StatusRateLimited:
			summary.RateLimited++
		case StatusQuotaExhausted:
			summary.QuotaExhausted++
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

func compareLeastUsed(a, b *Record) int {
	aUsed := quotaPressure(a)
	bUsed := quotaPressure(b)
	switch {
	case aUsed < bUsed:
		return -1
	case aUsed > bUsed:
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
		return strings.Compare(a.ID, b.ID)
	}
}

func quotaPressure(record *Record) float64 {
	if record.CachedQuota == nil || record.CachedQuota.RateLimit.UsedPercent == nil {
		return 0
	}
	return *record.CachedQuota.RateLimit.UsedPercent
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
		quota := *record.CachedQuota
		cloned.CachedQuota = &quota
	}
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
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}
