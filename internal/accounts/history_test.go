package accounts

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestUsageHistoryHistoryReturnsRawAndHourlyBuckets(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store, err := NewUsageHistoryStore(dir, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("NewUsageHistoryStore() error = %v", err)
	}

	base := time.Date(2026, 4, 19, 10, 0, 0, 0, time.UTC)
	summaries := []Summary{
		{Total: 2, Active: 2, TotalInputTokens: 100, TotalOutputTokens: 20, TotalRequests: 1, TotalEmptyResponses: 1},
		{Total: 2, Active: 2, TotalInputTokens: 130, TotalOutputTokens: 30, TotalRequests: 3, TotalEmptyResponses: 1},
		{Total: 2, Active: 2, TotalInputTokens: 145, TotalOutputTokens: 36, TotalRequests: 4, TotalEmptyResponses: 2},
	}
	for i, summary := range summaries {
		if err := store.RecordSummary(summary, base.Add(time.Duration(i)*30*time.Minute)); err != nil {
			t.Fatalf("RecordSummary(%d) error = %v", i, err)
		}
	}

	raw := store.History(4, "raw", base.Add(2*time.Hour))
	if len(raw) != 2 {
		t.Fatalf("len(raw) = %d, want 2", len(raw))
	}
	if raw[0].InputTokens != 30 || raw[0].RequestCount != 2 {
		t.Fatalf("raw[0] = %#v, want input=30 requests=2", raw[0])
	}
	if raw[0].EmptyResponseCount != 0 || raw[1].EmptyResponseCount != 1 {
		t.Fatalf("raw empty responses = %#v, want [0,1]", raw)
	}

	hourly := store.History(4, "hourly", base.Add(2*time.Hour))
	if len(hourly) != 2 {
		t.Fatalf("len(hourly) = %d, want 2", len(hourly))
	}
	if hourly[0].InputTokens != 30 || hourly[1].InputTokens != 15 {
		t.Fatalf("hourly = %#v", hourly)
	}
}

func TestUsageHistoryUsesBaselineWhenLiveCountersDrop(t *testing.T) {
	t.Parallel()

	store, err := NewUsageHistoryStore(t.TempDir(), 7*24*time.Hour)
	if err != nil {
		t.Fatalf("NewUsageHistoryStore() error = %v", err)
	}

	base := time.Date(2026, 4, 19, 10, 0, 0, 0, time.UTC)
	if err := store.RecordSummary(Summary{TotalInputTokens: 100, TotalOutputTokens: 50, TotalRequests: 10, TotalEmptyResponses: 4}, base); err != nil {
		t.Fatalf("RecordSummary(initial) error = %v", err)
	}
	if err := store.RecordSummary(Summary{TotalInputTokens: 20, TotalOutputTokens: 5, TotalRequests: 2, TotalEmptyResponses: 1}, base.Add(time.Hour)); err != nil {
		t.Fatalf("RecordSummary(reset) error = %v", err)
	}
	if err := store.RecordSummary(Summary{TotalInputTokens: 30, TotalOutputTokens: 9, TotalRequests: 4, TotalEmptyResponses: 2}, base.Add(2*time.Hour)); err != nil {
		t.Fatalf("RecordSummary(after reset) error = %v", err)
	}

	raw := store.History(4, "raw", base.Add(3*time.Hour))
	if len(raw) != 2 {
		t.Fatalf("len(raw) = %d, want 2", len(raw))
	}
	if raw[0].InputTokens != 0 || raw[0].OutputTokens != 0 || raw[0].RequestCount != 0 {
		t.Fatalf("raw[0] = %#v, want 0/0/0 after counter reset", raw[0])
	}
	if raw[0].EmptyResponseCount != 0 {
		t.Fatalf("raw[0] empty_response_count = %d, want 0 after counter reset", raw[0].EmptyResponseCount)
	}
	if raw[1].InputTokens != 10 || raw[1].OutputTokens != 4 || raw[1].RequestCount != 2 {
		t.Fatalf("raw[1] = %#v, want 10/4/2 after baseline adjustment", raw[1])
	}
	if raw[1].EmptyResponseCount != 1 {
		t.Fatalf("raw[1] empty_response_count = %d, want 1 after baseline adjustment", raw[1].EmptyResponseCount)
	}
}

func TestUsageHistoryCumulativeSummaryAppliesBaselineImmediately(t *testing.T) {
	t.Parallel()

	store, err := NewUsageHistoryStore(t.TempDir(), 7*24*time.Hour)
	if err != nil {
		t.Fatalf("NewUsageHistoryStore() error = %v", err)
	}

	base := time.Date(2026, 4, 19, 10, 0, 0, 0, time.UTC)
	if err := store.RecordSummary(Summary{
		Total:               2,
		Active:              2,
		TotalInputTokens:    100,
		TotalOutputTokens:   50,
		TotalRequests:       10,
		TotalEmptyResponses: 4,
	}, base); err != nil {
		t.Fatalf("RecordSummary() error = %v", err)
	}

	summary, err := store.CumulativeSummary(Summary{
		Total:               1,
		Active:              1,
		Available:           1,
		TotalInputTokens:    20,
		TotalOutputTokens:   5,
		TotalRequests:       2,
		TotalEmptyResponses: 1,
	})
	if err != nil {
		t.Fatalf("CumulativeSummary() error = %v", err)
	}

	if summary.Total != 1 || summary.Active != 1 || summary.Available != 1 {
		t.Fatalf("live counts changed unexpectedly: %#v", summary)
	}
	if summary.TotalInputTokens != 100 || summary.TotalOutputTokens != 50 || summary.TotalRequests != 10 {
		t.Fatalf("cumulative totals = %#v, want original cumulative totals preserved", summary)
	}
	if summary.TotalEmptyResponses != 4 {
		t.Fatalf("empty responses = %d, want 4", summary.TotalEmptyResponses)
	}

	next, err := store.CumulativeSummary(Summary{
		Total:               1,
		Active:              1,
		Available:           1,
		TotalInputTokens:    30,
		TotalOutputTokens:   9,
		TotalRequests:       4,
		TotalEmptyResponses: 2,
	})
	if err != nil {
		t.Fatalf("CumulativeSummary(next) error = %v", err)
	}
	if next.TotalInputTokens != 110 || next.TotalOutputTokens != 54 || next.TotalRequests != 12 {
		t.Fatalf("next cumulative totals = %#v, want 110/54/12", next)
	}
	if next.TotalEmptyResponses != 5 {
		t.Fatalf("next cumulative empty responses = %d, want 5", next.TotalEmptyResponses)
	}
}

func TestUsageHistoryRecoverBaselinePersistsAcrossRestart(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	base := time.Date(2026, 4, 19, 10, 0, 0, 0, time.UTC)

	store, err := NewUsageHistoryStore(dir, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("NewUsageHistoryStore() error = %v", err)
	}
	if err := store.RecordSummary(Summary{
		Total:               2,
		Active:              2,
		TotalInputTokens:    100,
		TotalOutputTokens:   50,
		TotalRequests:       10,
		TotalEmptyResponses: 4,
	}, base); err != nil {
		t.Fatalf("RecordSummary() error = %v", err)
	}

	restarted, err := NewUsageHistoryStore(dir, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("NewUsageHistoryStore(restarted) error = %v", err)
	}
	if err := restarted.RecoverBaseline(Summary{
		Total:               1,
		Active:              1,
		Available:           1,
		TotalInputTokens:    20,
		TotalOutputTokens:   5,
		TotalRequests:       2,
		TotalEmptyResponses: 1,
	}); err != nil {
		t.Fatalf("RecoverBaseline() error = %v", err)
	}

	reloaded, err := NewUsageHistoryStore(dir, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("NewUsageHistoryStore(reloaded) error = %v", err)
	}
	summary, err := reloaded.CumulativeSummary(Summary{
		Total:               1,
		Active:              1,
		Available:           1,
		TotalInputTokens:    20,
		TotalOutputTokens:   5,
		TotalRequests:       2,
		TotalEmptyResponses: 1,
	})
	if err != nil {
		t.Fatalf("CumulativeSummary() error = %v", err)
	}
	if summary.TotalInputTokens != 100 || summary.TotalOutputTokens != 50 || summary.TotalRequests != 10 {
		t.Fatalf("summary after restart = %#v, want 100/50/10 preserved", summary)
	}
	if summary.TotalEmptyResponses != 4 {
		t.Fatalf("summary empty responses after restart = %d, want 4", summary.TotalEmptyResponses)
	}
}

func TestUsageHistoryStoreIgnoresMalformedFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "usage-history.json")
	if err := os.WriteFile(path, []byte("{not valid json"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	store, err := NewUsageHistoryStore(dir, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("NewUsageHistoryStore() error = %v", err)
	}

	if points := store.History(24, "raw", time.Now().UTC()); len(points) != 0 {
		t.Fatalf("len(History()) = %d, want 0 for malformed file recovery", len(points))
	}
}
