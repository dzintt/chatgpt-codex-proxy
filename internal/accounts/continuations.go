package accounts

import (
	"slices"
	"strings"
	"sync"
	"time"
)

type ContinuationManager struct {
	ttl     time.Duration
	mu      sync.RWMutex
	records map[string]ContinuationRecord
}

func NewContinuationManager(ttl time.Duration) *ContinuationManager {
	return &ContinuationManager{
		ttl:     ttl,
		records: make(map[string]ContinuationRecord),
	}
}

func (m *ContinuationManager) Put(record ContinuationRecord) {
	m.mu.Lock()
	defer m.mu.Unlock()
	record.CreatedAt = time.Now().UTC()
	record.ExpiresAt = time.Now().Add(m.ttl)
	m.records[record.ResponseID] = record
}

func (m *ContinuationManager) Get(responseID string) (ContinuationRecord, bool) {
	m.mu.RLock()
	record, ok := m.records[responseID]
	m.mu.RUnlock()
	if !ok {
		return ContinuationRecord{}, false
	}
	if time.Now().After(record.ExpiresAt) {
		m.mu.Lock()
		delete(m.records, responseID)
		m.mu.Unlock()
		return ContinuationRecord{}, false
	}
	return record, true
}

func (m *ContinuationManager) Sweep() {
	now := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()
	for key, record := range m.records {
		if now.After(record.ExpiresAt) {
			delete(m.records, key)
		}
	}
}

func (m *ContinuationManager) GetLatestByConversation(key string) (ContinuationRecord, bool) {
	records := m.ListByConversation(key)
	if len(records) == 0 {
		return ContinuationRecord{}, false
	}
	return records[0], true
}

func (m *ContinuationManager) ListByConversation(key string) []ContinuationRecord {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil
	}

	now := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()

	records := make([]ContinuationRecord, 0)
	for responseID, record := range m.records {
		if now.After(record.ExpiresAt) {
			delete(m.records, responseID)
			continue
		}
		if strings.TrimSpace(record.ConversationKey) != key {
			continue
		}
		records = append(records, record)
	}
	slices.SortFunc(records, func(a, b ContinuationRecord) int {
		switch {
		case a.CreatedAt.After(b.CreatedAt):
			return -1
		case a.CreatedAt.Before(b.CreatedAt):
			return 1
		default:
			return strings.Compare(a.ResponseID, b.ResponseID)
		}
	})
	return records
}
