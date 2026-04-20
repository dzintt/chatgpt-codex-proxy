package accounts

import (
	"testing"
	"time"
)

type memoryStore struct {
	state State
}

func (m *memoryStore) Load() (State, error) {
	return m.state, nil
}

func (m *memoryStore) Save(state State) error {
	m.state = state
	return nil
}

func TestAcquireUsesConfiguredRotationStrategy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		setup  func() State
		verify func(*testing.T, *Service)
	}{
		{
			name: "round robin",
			setup: func() State {
				return State{
					Records: []*Record{
						recordWithID("acct_a"),
						recordWithID("acct_b"),
					},
					RotationStrategy: RotationRoundRobin,
				}
			},
			verify: func(t *testing.T, svc *Service) {
				t.Helper()

				first, _ := svc.Acquire("")
				second, _ := svc.Acquire("")
				third, _ := svc.Acquire("")

				if first.ID != "acct_a" || second.ID != "acct_b" || third.ID != "acct_a" {
					t.Fatalf("round robin order = %q, %q, %q", first.ID, second.ID, third.ID)
				}
			},
		},
		{
			name: "least used",
			setup: func() State {
				usedPercent := 75.0
				lastUsed := time.Now().UTC()
				return State{
					Records: []*Record{
						{
							ID:        "acct_busy",
							AccountID: "chatgpt_busy",
							Status:    StatusActive,
							CachedQuota: &QuotaSnapshot{
								RateLimit: RateLimitWindow{UsedPercent: &usedPercent},
							},
							LocalUsage: LocalUsage{RequestCount: 10, LastUsedAt: &lastUsed},
						},
						recordWithID("acct_free"),
					},
					RotationStrategy: RotationLeastUsed,
				}
			},
			verify: func(t *testing.T, svc *Service) {
				t.Helper()

				record, err := svc.Acquire("")
				if err != nil {
					t.Fatalf("Acquire() error = %v", err)
				}
				if record.ID != "acct_free" {
					t.Fatalf("Acquire() = %q, want acct_free", record.ID)
				}
			},
		},
		{
			name: "sticky",
			setup: func() State {
				now := time.Now().UTC()
				earlier := now.Add(-time.Hour)
				return State{
					Records: []*Record{
						{
							ID:         "acct_old",
							AccountID:  "chatgpt_old",
							Status:     StatusActive,
							LocalUsage: LocalUsage{LastUsedAt: &earlier},
						},
						{
							ID:         "acct_recent",
							AccountID:  "chatgpt_recent",
							Status:     StatusActive,
							LocalUsage: LocalUsage{LastUsedAt: &now},
						},
					},
					RotationStrategy: RotationSticky,
				}
			},
			verify: func(t *testing.T, svc *Service) {
				t.Helper()

				record, err := svc.Acquire("")
				if err != nil {
					t.Fatalf("Acquire() error = %v", err)
				}
				if record.ID != "acct_recent" {
					t.Fatalf("Acquire() = %q, want acct_recent", record.ID)
				}
			},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			svc, err := NewService(&memoryStore{state: tc.setup()}, RotationRoundRobin)
			if err != nil {
				t.Fatalf("NewService() error = %v", err)
			}
			tc.verify(t, svc)
		})
	}
}

func recordWithID(id string) *Record {
	now := time.Now().UTC()
	return &Record{
		ID:        id,
		AccountID: id + "_upstream",
		Status:    StatusActive,
		CreatedAt: now,
		UpdatedAt: now,
	}
}
