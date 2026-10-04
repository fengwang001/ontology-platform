package history

import (
	"errors"
	"sort"
)

var (
	ErrInvalidArgument = errors.New("history: invalid argument")
	ErrClockBacktrack  = errors.New("history: clock backtrack")
	ErrDocNotFound     = errors.New("history: document not found")
)

type Kind int

const (
	KindIndex Kind = iota + 1
	KindDelete
)

type Op struct {
	Seq  int
	ID   string
	Kind Kind
}

type Doc struct {
	ID  string
	Seq int
}

type MergeResult struct {
	Purged  int
	Removed []string
}

// History 是主分片的操作历史。并发安全由 Primary 的单锁保证。
// 记录逻辑清除：ops 中被清除项置零（Kind==0），不压缩下标，seq 即下标。
type History struct {
	ops     []Op
	latest  map[string]int
	live    map[string]int
	h       int
	touched int
}

func New() *History {
	return &History{
		latest: make(map[string]int),
		live:   make(map[string]int),
		h:      1,
	}
}

func (hs *History) MaxSeq() int { return len(hs.ops) }

func (hs *History) H() int { return hs.h }

// Append 追加一条操作并返回其 seq。
func (hs *History) Append(id string, kind Kind) int {
	seq := len(hs.ops) + 1
	hs.ops = append(hs.ops, Op{Seq: seq, ID: id, Kind: kind})
	hs.latest[id] = seq
	if kind == KindIndex {
		hs.live[id] = seq
	} else {
		delete(hs.live, id)
	}
	return seq
}

// Live 报告 id 当前是否存活（最近一条操作是 Index）。
func (hs *History) Live(id string) bool {
	_, ok := hs.live[id]
	return ok
}

// Superseded 以 O(1) 判定 op 是否被同 id 的更大 seq 操作取代，不扫描历史。
func (hs *History) Superseded(op Op) bool { return hs.latest[op.ID] > op.Seq }

// Purge 清除 seq 落在 [h, floor) 内被取代者或 Delete；未被取代的 Index 保留。
// 只触碰区间内的记录。
func (hs *History) Purge(floor int) int {
	hs.touched = 0
	purged := 0
	for seq := hs.h; seq < floor; seq++ {
		hs.touched++
		op := hs.ops[seq-1]
		if op.Kind == 0 {
			continue
		}
		if op.Kind == KindDelete || hs.Superseded(op) {
			hs.ops[seq-1] = Op{}
			purged++
		}
	}
	hs.h = floor
	return purged
}

// OpsAfter 返回 seq > c 的全部现存操作，按 seq 升序。
func (hs *History) OpsAfter(c int) []Op {
	out := make([]Op, 0, len(hs.ops)-c)
	for seq := c + 1; seq <= len(hs.ops); seq++ {
		if op := hs.ops[seq-1]; op.Kind != 0 {
			out = append(out, op)
		}
	}
	return out
}

// LiveDocs 返回当前存活文档，按 id 字节序，带其最新 Index 的 seq。
func (hs *History) LiveDocs() []Doc {
	out := make([]Doc, 0, len(hs.live))
	for id, seq := range hs.live {
		out = append(out, Doc{ID: id, Seq: seq})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
