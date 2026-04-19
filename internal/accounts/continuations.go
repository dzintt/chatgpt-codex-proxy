package accounts

import (
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
