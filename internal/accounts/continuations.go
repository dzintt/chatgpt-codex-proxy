package accounts

import (
	"slices"
	"strings"
	"sync"
	"time"

	"chatgpt-codex-proxy/internal/conversation"
)

type ContinuationInputItem struct {
	Role             string                    `json:"role,omitempty"`
	Type             string                    `json:"type,omitempty"`
	Phase            string                    `json:"phase,omitempty"`
	Content          []ContinuationContentPart `json:"content,omitempty"`
	CallID           string                    `json:"call_id,omitempty"`
	Name             string                    `json:"name,omitempty"`
	Input            string                    `json:"input,omitempty"`
	Arguments        string                    `json:"arguments,omitempty"`
	OutputText       string                    `json:"output,omitempty"`
	OutputContent    []ContinuationContentPart `json:"output_content,omitempty"`
	ID               string                    `json:"id,omitempty"`
	Status           string                    `json:"status,omitempty"`
	Summary          []ContinuationSummaryPart `json:"summary,omitempty"`
	EncryptedContent string                    `json:"encrypted_content,omitempty"`
}

type ContinuationContentPart = conversation.ContentPart

type ContinuationSummaryPart = conversation.ReasoningPart

type ContinuationRecord struct {
	ResponseID      string
	AccountID       string
	UpstreamID      string
	ConversationKey string
	TurnState       string
	Instructions    string
	Model           string
	InputHistory    []ContinuationInputItem
	FunctionCallIDs []string
	CreatedAt       time.Time
	ExpiresAt       time.Time
}

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

func (m *ContinuationManager) ListAll() []ContinuationRecord {
	now := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()

	records := make([]ContinuationRecord, 0, len(m.records))
	for responseID, record := range m.records {
		if now.After(record.ExpiresAt) {
			delete(m.records, responseID)
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
