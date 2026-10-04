package hold

import "testing"

func rec(k int64, payload string) Record {
	return Record{K: k, Payload: payload}
}

func TestBufferReleaseAndExamined(t *testing.T) {
	tests := []struct {
		name     string
		records  []Record
		limit    int64
		wantIDs  []string
		examined int
	}{
		{name: "release three from one hundred", limit: 30, wantIDs: []string{"a1", "a2", "a3"}, examined: 4},
		{name: "release three from ten thousand", limit: 30, wantIDs: []string{"a1", "a2", "a3"}, examined: 4},
		{name: "same k keeps arrival order", records: []Record{rec(5, "b"), rec(5, "a")}, limit: 5, wantIDs: []string{"b", "a"}, examined: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := NewBuffer()
			if tt.name == "same k keeps arrival order" {
				for _, r := range tt.records {
					b.Add(r)
				}
			} else {
				total := 100
				if tt.name == "release three from ten thousand" {
					total = 10_000
				}
				for i := 1; i <= total; i++ {
					id := "tail"
					if i <= 3 {
						id = []string{"a1", "a2", "a3"}[i-1]
					}
					b.Add(rec(int64(i*10), id))
				}
			}
			got := b.ReleaseLE(tt.limit)
			if len(got) != len(tt.wantIDs) {
				t.Fatalf("released %d, want %d", len(got), len(tt.wantIDs))
			}
			for i, want := range tt.wantIDs {
				if got[i].Payload != want {
					t.Fatalf("got[%d] = %s, want %s", i, got[i].Payload, want)
				}
			}
			if b.examined != tt.examined || b.examined > len(got)+1 {
				t.Fatalf("examined = %d, released = %d, want %d", b.examined, len(got), tt.examined)
			}
		})
	}
}

func TestBufferReleaseAllAndMaxK(t *testing.T) {
	b := NewBuffer()
	b.Add(rec(9, "nine"))
	b.Add(rec(3, "three"))
	b.Add(rec(20, "twenty"))
	maxK, ok := b.MaxK()
	if !ok || maxK != 20 || b.Len() != 3 {
		t.Fatalf("max = (%d,%v), len = %d", maxK, ok, b.Len())
	}
	got := b.ReleaseAll()
	want := []string{"three", "nine", "twenty"}
	for i := range want {
		if got[i].Payload != want[i] {
			t.Fatalf("got[%d] = %s, want %s", i, got[i].Payload, want[i])
		}
	}
	if b.Len() != 0 {
		t.Fatalf("len after release = %d", b.Len())
	}
}

func TestExaminedAcrossPendingSizes(t *testing.T) {
	for _, total := range []int{100, 10_000} {
		b := NewBuffer()
		for i := 1; i <= total; i++ {
			b.Add(rec(int64(i), "r"))
		}
		got := b.ReleaseLE(3)
		if len(got) != 3 || b.Examined() != 4 || b.Examined() > len(got)+1 {
			t.Fatalf("total=%d released=%d examined=%d", total, len(got), b.Examined())
		}
	}
}
