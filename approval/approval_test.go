package approval

import (
	"errors"
	"testing"
)

type addStep struct {
	user    string
	at      int64
	wantErr error
}

type countStep struct {
	at   int64
	want int
}

func TestTicket(t *testing.T) {
	cases := []struct {
		name   string
		ttl    int64
		adds   []addStep
		counts []countStep
	}{
		{
			name: "validity boundary is strict",
			ttl:  10,
			adds: []addStep{{user: "alice", at: 5}},
			counts: []countStep{
				{at: 5, want: 1},
				{at: 14, want: 1},
				{at: 15, want: 0}, // exactly at+TTL: expired
				{at: 100, want: 0},
			},
		},
		{
			name: "duplicate while valid, replace after expiry",
			ttl:  10,
			adds: []addStep{
				{user: "alice", at: 5},
				{user: "bob", at: 7},
				{user: "alice", at: 10, wantErr: ErrDuplicateApproval},
				{user: "alice", at: 14, wantErr: ErrDuplicateApproval},
				{user: "alice", at: 15}, // old one expired at 15: replaced
			},
			counts: []countStep{
				{at: 15, want: 2}, // alice@15, bob@7 (15 < 17)
				{at: 17, want: 1}, // bob expired
				{at: 24, want: 1},
				{at: 25, want: 0}, // alice expired
			},
		},
		{
			name: "ttl of one second",
			ttl:  1,
			adds: []addStep{{user: "alice", at: 0}},
			counts: []countStep{
				{at: 0, want: 1},
				{at: 1, want: 0},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ticket := NewTicket(tc.ttl)
			for i, add := range tc.adds {
				err := ticket.Add(add.user, add.at)
				if !errors.Is(err, add.wantErr) {
					t.Fatalf("add %d: got err %v, want %v", i, err, add.wantErr)
				}
			}
			for _, count := range tc.counts {
				if got := ticket.ValidCount(count.at); got != count.want {
					t.Fatalf("ValidCount(%d) = %d, want %d", count.at, got, count.want)
				}
			}
		})
	}
}
