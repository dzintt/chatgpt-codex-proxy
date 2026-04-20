package accounts

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

type UsageHistoryBaseline struct {
	InputTokens        int64 `json:"input_tokens"`
	OutputTokens       int64 `json:"output_tokens"`
	RequestCount       int64 `json:"request_count"`
	EmptyResponseCount int64 `json:"empty_response_count"`
}

type UsageSnapshot struct {
	Timestamp          time.Time `json:"timestamp"`
	InputTokens        int64     `json:"input_tokens"`
	OutputTokens       int64     `json:"output_tokens"`
	RequestCount       int64     `json:"request_count"`
	EmptyResponseCount int64     `json:"empty_response_count"`
	ActiveAccounts     int       `json:"active_accounts"`
	TotalAccounts      int       `json:"total_accounts"`
}

type UsageHistoryState struct {
	Snapshots []UsageSnapshot      `json:"snapshots"`
	Baseline  UsageHistoryBaseline `json:"baseline"`
}

type UsageHistoryPoint struct {
	Timestamp          time.Time `json:"timestamp"`
	InputTokens        int64     `json:"input_tokens"`
	OutputTokens       int64     `json:"output_tokens"`
	RequestCount       int64     `json:"request_count"`
	EmptyResponseCount int64     `json:"empty_response_count"`
}

type UsageHistoryStore struct {
	mu        sync.Mutex
	path      string
	retention time.Duration
	state     UsageHistoryState
}

func NewUsageHistoryStore(dataDir string, retention time.Duration) (*UsageHistoryStore, error) {
	store := &UsageHistoryStore{
		path:      filepath.Join(dataDir, "usage-history.json"),
		retention: retention,
		state: UsageHistoryState{
			Snapshots: []UsageSnapshot{},
		},
	}
	store.load()
	return store, nil
}

func (s *UsageHistoryStore) RecoverBaseline(live Summary) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	current := UsageSnapshot{
		Timestamp:          time.Now().UTC(),
		InputTokens:        live.TotalInputTokens,
		OutputTokens:       live.TotalOutputTokens,
		RequestCount:       live.TotalRequests,
		EmptyResponseCount: live.TotalEmptyResponses,
		ActiveAccounts:     live.Active,
		TotalAccounts:      live.Total,
	}

	if !s.reconcileBaselineLocked(current) {
		return nil
	}
	return s.saveLocked()
}

func (s *UsageHistoryStore) RecordSummary(summary Summary, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	live := UsageSnapshot{
		Timestamp:          now.UTC(),
		InputTokens:        summary.TotalInputTokens,
		OutputTokens:       summary.TotalOutputTokens,
		RequestCount:       summary.TotalRequests,
		EmptyResponseCount: summary.TotalEmptyResponses,
		ActiveAccounts:     summary.Active,
		TotalAccounts:      summary.Total,
	}
	s.reconcileBaselineLocked(live)

	live.InputTokens += s.state.Baseline.InputTokens
	live.OutputTokens += s.state.Baseline.OutputTokens
	live.RequestCount += s.state.Baseline.RequestCount
	live.EmptyResponseCount += s.state.Baseline.EmptyResponseCount

	s.state.Snapshots = append(s.state.Snapshots, live)
	s.pruneLocked(now)
	return s.saveLocked()
}

func (s *UsageHistoryStore) CumulativeSummary(live Summary) (Summary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	current := UsageSnapshot{
		Timestamp:          time.Now().UTC(),
		InputTokens:        live.TotalInputTokens,
		OutputTokens:       live.TotalOutputTokens,
		RequestCount:       live.TotalRequests,
		EmptyResponseCount: live.TotalEmptyResponses,
		ActiveAccounts:     live.Active,
		TotalAccounts:      live.Total,
	}

	var persistErr error
	if s.reconcileBaselineLocked(current) {
		persistErr = s.saveLocked()
	}

	out := live
	out.TotalInputTokens = s.state.Baseline.InputTokens + live.TotalInputTokens
	out.TotalOutputTokens = s.state.Baseline.OutputTokens + live.TotalOutputTokens
	out.TotalRequests = s.state.Baseline.RequestCount + live.TotalRequests
	out.TotalEmptyResponses = s.state.Baseline.EmptyResponseCount + live.TotalEmptyResponses
	return out, persistErr
}

func (s *UsageHistoryStore) History(hours int, granularity string, now time.Time) []UsageHistoryPoint {
	s.mu.Lock()
	defer s.mu.Unlock()

	if hours <= 0 {
		hours = 24
	}
	cutoff := now.Add(-time.Duration(hours) * time.Hour)
	filtered := make([]UsageSnapshot, 0, len(s.state.Snapshots))
	for _, snapshot := range s.state.Snapshots {
		if !snapshot.Timestamp.Before(cutoff) {
			filtered = append(filtered, snapshot)
		}
	}
	if len(filtered) < 2 {
		return []UsageHistoryPoint{}
	}

	deltas := make([]UsageHistoryPoint, 0, len(filtered)-1)
	for i := 1; i < len(filtered); i++ {
		prev := filtered[i-1]
		curr := filtered[i]
		deltas = append(deltas, UsageHistoryPoint{
			Timestamp:          curr.Timestamp,
			InputTokens:        maxInt64(0, curr.InputTokens-prev.InputTokens),
			OutputTokens:       maxInt64(0, curr.OutputTokens-prev.OutputTokens),
			RequestCount:       maxInt64(0, curr.RequestCount-prev.RequestCount),
			EmptyResponseCount: maxInt64(0, curr.EmptyResponseCount-prev.EmptyResponseCount),
		})
	}

	switch granularity {
	case "raw":
		return deltas
	case "daily":
		return bucketUsage(deltas, 24*time.Hour)
	default:
		return bucketUsage(deltas, time.Hour)
	}
}

func (s *UsageHistoryStore) load() {
	raw, err := os.ReadFile(s.path)
	if err != nil {
		return
	}
	var decoded UsageHistoryState
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return
	}
	s.state = decoded
}

func (s *UsageHistoryStore) pruneLocked(now time.Time) {
	if s.retention <= 0 {
		return
	}
	cutoff := now.Add(-s.retention)
	kept := s.state.Snapshots[:0]
	for _, snapshot := range s.state.Snapshots {
		if !snapshot.Timestamp.Before(cutoff) {
			kept = append(kept, snapshot)
		}
	}
	s.state.Snapshots = kept
}

func (s *UsageHistoryStore) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("create usage history dir: %w", err)
	}
	payload, err := json.MarshalIndent(s.state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode usage history: %w", err)
	}
	payload = append(payload, '\n')
	tmpPath := s.path + ".tmp"
	if err := os.WriteFile(tmpPath, payload, 0o600); err != nil {
		return fmt.Errorf("write tmp usage history: %w", err)
	}
	if err := os.Rename(tmpPath, s.path); err != nil {
		return fmt.Errorf("rename usage history: %w", err)
	}
	return nil
}

func (s *UsageHistoryStore) reconcileBaselineLocked(live UsageSnapshot) bool {
	if len(s.state.Snapshots) == 0 {
		return false
	}

	last := s.state.Snapshots[len(s.state.Snapshots)-1]
	prevLiveInput := last.InputTokens - s.state.Baseline.InputTokens
	prevLiveOutput := last.OutputTokens - s.state.Baseline.OutputTokens
	prevLiveRequests := last.RequestCount - s.state.Baseline.RequestCount
	prevLiveEmptyResponses := last.EmptyResponseCount - s.state.Baseline.EmptyResponseCount

	changed := false
	if live.InputTokens < prevLiveInput {
		s.state.Baseline.InputTokens += prevLiveInput - live.InputTokens
		changed = true
	}
	if live.OutputTokens < prevLiveOutput {
		s.state.Baseline.OutputTokens += prevLiveOutput - live.OutputTokens
		changed = true
	}
	if live.RequestCount < prevLiveRequests {
		s.state.Baseline.RequestCount += prevLiveRequests - live.RequestCount
		changed = true
	}
	if live.EmptyResponseCount < prevLiveEmptyResponses {
		s.state.Baseline.EmptyResponseCount += prevLiveEmptyResponses - live.EmptyResponseCount
		changed = true
	}
	return changed
}

func bucketUsage(points []UsageHistoryPoint, size time.Duration) []UsageHistoryPoint {
	if len(points) == 0 {
		return []UsageHistoryPoint{}
	}
	buckets := make(map[time.Time]*UsageHistoryPoint)
	for _, point := range points {
		key := point.Timestamp.UTC().Truncate(size)
		current := buckets[key]
		if current == nil {
			current = &UsageHistoryPoint{Timestamp: key}
			buckets[key] = current
		}
		current.InputTokens += point.InputTokens
		current.OutputTokens += point.OutputTokens
		current.RequestCount += point.RequestCount
		current.EmptyResponseCount += point.EmptyResponseCount
	}
	keys := make([]time.Time, 0, len(buckets))
	for key := range buckets {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].Before(keys[j]) })
	out := make([]UsageHistoryPoint, 0, len(keys))
	for _, key := range keys {
		out = append(out, *buckets[key])
	}
	return out
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
