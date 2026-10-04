package translog

import (
	"reflect"
	"testing"
)

func seqs(ops []Op) []int64 {
	out := make([]int64, len(ops))
	for i, op := range ops {
		out[i] = op.Seq
	}
	return out
}

func TestLogLifecycle(t *testing.T) {
	tests := []struct {
		name       string
		appends    int64 // 追加 seq 1..appends
		syncTo     int64
		crash      bool
		rollovers  int
		wantSeqs   []int64
		wantSynced int64
		wantGen    int64
	}{
		{name: "append only keeps all", appends: 3, wantSeqs: []int64{1, 2, 3}, wantSynced: 0, wantGen: 1},
		{name: "sync then crash keeps prefix", appends: 5, syncTo: 3, crash: true, wantSeqs: []int64{1, 2, 3}, wantSynced: 3, wantGen: 1},
		{name: "crash without sync drops all", appends: 4, crash: true, wantSeqs: []int64{}, wantSynced: 0, wantGen: 1},
		{name: "sync beyond max keeps all", appends: 2, syncTo: 10, crash: true, wantSeqs: []int64{1, 2}, wantSynced: 10, wantGen: 1},
		{name: "rollover drops entries and bumps generation", appends: 3, syncTo: 3, rollovers: 2, wantSeqs: []int64{}, wantSynced: 3, wantGen: 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := New(1)
			for seq := int64(1); seq <= tt.appends; seq++ {
				l.Append(Op{Seq: seq, Kind: KindIndex, ID: []byte{byte(seq)}})
			}
			l.SyncTo(tt.syncTo)
			l.SyncTo(0) // 水位只升不降
			if tt.crash {
				l.Crash()
			}
			for i := 0; i < tt.rollovers; i++ {
				l.Rollover()
			}
			if got := seqs(l.Entries()); !reflect.DeepEqual(got, tt.wantSeqs) {
				t.Fatalf("entries seq = %v, want %v", got, tt.wantSeqs)
			}
			if l.Synced() != tt.wantSynced {
				t.Fatalf("synced = %d, want %d", l.Synced(), tt.wantSynced)
			}
			if l.Generation() != tt.wantGen {
				t.Fatalf("generation = %d, want %d", l.Generation(), tt.wantGen)
			}
			if l.Len() != len(tt.wantSeqs) {
				t.Fatalf("len = %d, want %d", l.Len(), len(tt.wantSeqs))
			}
		})
	}
}
