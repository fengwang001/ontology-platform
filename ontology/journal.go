package ontology

import (
	"fmt"
	"io"
	"sort"
	"sync"
)

// ChangeRecord 记录一次原子变更单元的输入、受影响下游集合与判定依据。
type ChangeRecord struct {
	Operation string
	Seq       int
	Detail    map[string]any
}

type Journal struct {
	mu      sync.Mutex
	out     io.Writer
	records []ChangeRecord
}

// NewJournal 创建内存日志；SetOutput 可同时把每条记录打印到给定 writer。
func NewJournal() *Journal { return &Journal{} }

func (j *Journal) SetOutput(w io.Writer) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.out = w
}

func (j *Journal) append(op string, detail map[string]any) ChangeRecord {
	j.mu.Lock()
	defer j.mu.Unlock()
	rec := ChangeRecord{Seq: len(j.records) + 1, Operation: op, Detail: detail}
	j.records = append(j.records, rec)
	if j.out != nil {
		fmt.Fprintln(j.out, rec.String())
	}
	return rec
}

func (j *Journal) Records() []ChangeRecord {
	j.mu.Lock()
	defer j.mu.Unlock()
	out := make([]ChangeRecord, len(j.records))
	copy(out, j.records)
	return out
}

// String 以稳定的顺序渲染：操作输入 -> 受影响下游集合 -> 判定依据。
func (r ChangeRecord) String() string {
	keys := make([]string, 0, len(r.Detail))
	for k := range r.Detail {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	s := fmt.Sprintf("#%d %s", r.Seq, r.Operation)
	for _, k := range keys {
		s += fmt.Sprintf(" | %s=%v", k, r.Detail[k])
	}
	return s
}
