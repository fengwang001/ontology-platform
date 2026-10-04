package meter

import (
	"errors"
	"testing"
)

func TestMergeTableDriven(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		quota      int
		user       []Record
		device     []Record
		wantUsed   int
		wantKind   map[string]Kind
		wantAbsent []string
		wantTouch  int
	}{
		{
			name:  "example union keeps earliest three",
			quota: 3,
			user: []Record{
				{Article: "a3", FirstUnlock: 5, Kind: Quota},
				{Article: "a4", FirstUnlock: 15, Kind: Quota},
			},
			device: []Record{
				{Article: "a1", FirstUnlock: 10, Kind: Quota},
				{Article: "a2", FirstUnlock: 20, Kind: Quota},
			},
			wantUsed:   3,
			wantKind:   map[string]Kind{"a1": Quota, "a3": Quota, "a4": Quota},
			wantAbsent: []string{"a2"},
			wantTouch:  4,
		},
		{
			name:  "same time sorted by article bytes",
			quota: 2,
			device: []Record{
				{Article: "b", FirstUnlock: 1, Kind: Quota},
				{Article: "a", FirstUnlock: 1, Kind: Quota},
			},
			wantUsed:  2,
			wantKind:  map[string]Kind{"a": Quota, "b": Quota},
			wantTouch: 2,
		},
		{
			name:  "gift does not occupy quota",
			quota: 1,
			user: []Record{
				{Article: "g", FirstUnlock: 1, Kind: Gift},
				{Article: "q", FirstUnlock: 2, Kind: Quota},
			},
			device: []Record{
				{Article: "x", FirstUnlock: 3, Kind: Quota},
			},
			wantUsed:   1,
			wantKind:   map[string]Kind{"g": Gift, "q": Quota},
			wantAbsent: []string{"x"},
			wantTouch:  3,
		},
		{
			name:  "duplicate gift side wins and earliest time wins",
			quota: 1,
			user: []Record{
				{Article: "a", FirstUnlock: 9, Kind: Quota},
			},
			device: []Record{
				{Article: "a", FirstUnlock: 3, Kind: Gift},
				{Article: "b", FirstUnlock: 4, Kind: Quota},
			},
			wantUsed:  1,
			wantKind:  map[string]Kind{"a": Gift, "b": Quota},
			wantTouch: 3,
		},
		{
			name:       "zero quota removes every quota record",
			quota:      0,
			device:     []Record{{Article: "a", FirstUnlock: 1, Kind: Quota}},
			user:       []Record{{Article: "g", FirstUnlock: 2, Kind: Gift}},
			wantUsed:   0,
			wantKind:   map[string]Kind{"g": Gift},
			wantAbsent: []string{"a"},
			wantTouch:  2,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			l := New(tt.quota, 1000)
			put := func(subject string, records []Record) {
				for _, record := range records {
					if record.Kind == Gift {
						l.AddGift(subject, record.FirstUnlock, record.Article)
					} else {
						l.AddQuota(subject, record.FirstUnlock, record.Article)
					}
				}
			}
			put("u", tt.user)
			put("d", tt.device)

			if got := l.Merge(30, "u", "d"); got != tt.wantUsed {
				t.Fatalf("used = %d, want %d", got, tt.wantUsed)
			}
			if got := l.Touched("", 30); got != tt.wantTouch {
				t.Fatalf("merge touched = %d, want %d", got, tt.wantTouch)
			}
			for article, kind := range tt.wantKind {
				record, ok := l.Lookup("u", 30, article)
				if !ok || record.Kind != kind {
					t.Fatalf("article %s = (%v, %v), want kind %v", article, ok, record.Kind, kind)
				}
			}
			for _, article := range tt.wantAbsent {
				if _, ok := l.Lookup("u", 30, article); ok {
					t.Fatalf("article %s should be revoked", article)
				}
			}
		})
	}
}

func TestMonthsAndReadTouchBound(t *testing.T) {
	t.Parallel()
	l := New(1, 100)
	for i := int64(0); i < 100; i++ {
		article := "old-" + string(rune('a'+i/26)) + string(rune('a'+i%26))
		if i == 0 {
			l.AddQuota("s", i, article)
		} else {
			l.AddGift("s", i, article)
		}
	}
	if _, ok := l.Lookup("s", 1000, "new"); ok {
		t.Fatal("new month unexpectedly has old record")
	}
	if got := l.Touched("s", 1000); got != 0 {
		t.Fatalf("miss touch = %d, want 0 without a current-month book", got)
	}
	l.AddQuota("s", 1000, "new")
	if got := l.Touched("s", 1000); got > 2 {
		t.Fatalf("write touched %d records, want <= 2", got)
	}
}

func TestClockAndArguments(t *testing.T) {
	t.Parallel()
	l := New(1, 100)
	if err := l.Advance(10); err != nil {
		t.Fatal(err)
	}
	if err := l.Check(9); !errors.Is(err, ErrClockSkew) {
		t.Fatalf("Check error = %v, want clock skew", err)
	}
	if err := l.Advance(9); !errors.Is(err, ErrClockSkew) {
		t.Fatalf("Advance error = %v, want clock skew", err)
	}
	if l.Month(10) != 0 || l.Month(100) != 1 {
		t.Fatal("month boundary is floor(now/M)")
	}
}
