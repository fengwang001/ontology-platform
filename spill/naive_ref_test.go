package spill

import (
	"fmt"
	"sync"
	"testing"
)

// naiveRef 是无上限朴素参照：每个事务保存全部追加行，
// 提交时整体输出；与溢写管理器执行同一脚本，结果必须一致。
type naiveRef struct {
	mu      sync.Mutex
	txns    map[uint64][]string
	commits []string
}

func newNaiveRef() *naiveRef {
	return &naiveRef{txns: make(map[uint64][]string)}
}

func (n *naiveRef) begin(id uint64) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.txns[id] = nil
}

func (n *naiveRef) append(id uint64, rows []string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.txns[id] = append(n.txns[id], rows...)
}

func (n *naiveRef) commit(id uint64) []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := n.txns[id]
	delete(n.txns, id)
	n.commits = append(n.commits, out...)
	return out
}

func (n *naiveRef) rollback(id uint64) {
	n.mu.Lock()
	defer n.mu.Unlock()
	delete(n.txns, id)
}

func (n *naiveRef) log() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]string(nil), n.commits...)
}

type op struct {
	kind string
	txn  uint64
	n    int
}

func TestNaiveReferenceEquivalence(t *testing.T) {
	m, err := NewManager(Config{MemRowLimit: 4, BlockLimit: 1000}, NewEventLog())
	if err != nil {
		t.Fatal(err)
	}
	ref := newNaiveRef()

	script := []op{
		{"begin", 1, 0}, {"begin", 2, 0}, {"begin", 3, 0},
		{"append", 1, 6}, {"append", 2, 3},
		{"append", 3, 5}, {"rollback", 3, 0},
		{"append", 1, 4}, {"append", 2, 7},
		{"begin", 4, 0}, {"append", 4, 2},
		{"commit", 1, 0},
		{"append", 4, 9},
		{"commit", 4, 0},
		{"rollback", 2, 0},
		{"begin", 5, 0}, {"append", 5, 13}, {"commit", 5, 0},
	}

	tag := func(id uint64, i int) string { return fmt.Sprintf("t%d.r%d", id, i) }
	for _, s := range script {
		switch s.kind {
		case "begin":
			m.Begin(s.txn)
			ref.begin(s.txn)
		case "append":
			ss := make([]string, s.n)
			for i := range ss {
				ss[i] = tag(s.txn, i)
			}
			if _, _, err := m.Append(s.txn, strRows(ss)); err != nil {
				t.Fatalf("append txn %d n %d: %v", s.txn, s.n, err)
			}
			ref.append(s.txn, ss)
		case "commit":
			got, err := m.Commit(s.txn)
			if err != nil {
				t.Fatalf("commit txn %d: %v", s.txn, err)
			}
			want := ref.commit(s.txn)
			if !equalStrings(dataOf(got), want) {
				t.Fatalf("txn %d replay mismatch:\n got=%v\nwant=%v", s.txn, dataOf(got), want)
			}
		case "rollback":
			if err := m.Rollback(s.txn); err != nil {
				t.Fatalf("rollback txn %d: %v", s.txn, err)
			}
			ref.rollback(s.txn)
		}
		if err := m.CheckInvariants(); err != nil {
			t.Fatalf("invariant after %+v: %v", s, err)
		}
	}

	if got, want := dataOf(m.CommittedLog()), ref.log(); !equalStrings(got, want) {
		t.Fatalf("downstream log mismatch:\n got=%v\nwant=%v", got, want)
	}
	if st := m.Stats(); st.OpenTxnCount != 0 || st.BlockCount != 0 || st.MemRows != 0 {
		t.Fatalf("non-empty state after script: %+v", st)
	}
}
