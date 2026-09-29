package reconcile

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
)

// DiffKey is a leaf key whose two replicas disagree.
type DiffKey struct {
	Key     int
	Left    []byte
	Right   []byte
	LeftOK  bool
	RightOK bool
}

// Comparison is one recorded hash comparison, in deterministic pre-order.
type Comparison struct {
	Level       int
	Lo, Hi      int
	LeftDigest  []byte
	RightDigest []byte
	Equal       bool
	DrilledDown bool
}

// Report is the full reconciliation result.
type Report struct {
	LeftName  string
	RightName string
	Diffs     []DiffKey
	Sequence  []Comparison
}

// Comparer runs reconciliations. The zero value uses a text logger on stderr;
// callers may replace Logger and/or Writer.
type Comparer struct {
	Logger *slog.Logger
	Writer io.Writer
}

// DefaultComparer logs inputs, decisions and diffs to stderr.
var DefaultComparer = &Comparer{Writer: os.Stderr}

func (c *Comparer) logger() *slog.Logger {
	if c.Logger != nil {
		return c.Logger
	}
	writer := c.Writer
	if writer == nil {
		writer = io.Discard
	}
	return slog.New(slog.NewTextHandler(writer, nil))
}

// Reconcile locates every differing key between two identically shaped
// replicas by recursively halving (fanout-splitting) only hash-mismatched
// intervals. It returns ErrShapeMismatch for replicas whose key space size or
// fanout differs.
func Reconcile(left, right *Replica) (*Report, error) {
	return DefaultComparer.Reconcile(left, right)
}

// Reconcile performs the drill-down with explicit receiver configuration.
func (c *Comparer) Reconcile(left, right *Replica) (*Report, error) {
	if left == nil || right == nil {
		return nil, ErrNilReplica
	}
	if left.size != right.size || left.fanout != right.fanout {
		return nil, ErrShapeMismatch
	}
	logger := c.logger()

	left.mu.RLock()
	defer left.mu.RUnlock()
	if left != right {
		right.mu.RLock()
		defer right.mu.RUnlock()
	}

	ls := left.snapshotLocked()
	rs := right.snapshotLocked()
	logger.LogAttrs(context.Background(), slog.LevelInfo,
		"reconcile start",
		slog.String("left", ls.name),
		slog.String("right", rs.name),
		slog.Int("size", ls.size),
		slog.Int("fanout", ls.fanout),
		slog.Int("left_keys", len(ls.data)),
		slog.Int("right_keys", len(rs.data)),
	)

	d := &drill{
		logger: logger,
		left:   ls,
		right:  rs,
	}
	rootStride := ipow(ls.fanout, treeDepth(ls.size, ls.fanout))
	d.visit(treeDepth(ls.size, ls.fanout), 0, rootStride)

	report := &Report{
		LeftName:  ls.name,
		RightName: rs.name,
		Diffs:     d.diffs,
		Sequence:  d.sequence,
	}
	logger.LogAttrs(context.Background(), slog.LevelInfo,
		"reconcile done",
		slog.Int("comparisons", len(report.Sequence)),
		slog.Int("diff_keys", len(report.Diffs)),
		slog.Any("diffs", diffSummaries(report.Diffs)),
	)
	return report, nil
}

type drill struct {
	logger   *slog.Logger
	left     snapshot
	right    snapshot
	sequence []Comparison
	diffs    []DiffKey
}

// visit compares one grid node and, on mismatch at an internal node, visits
// its fanout children in ascending key order.
func (d *drill) visit(level, lo, stride int) {
	ld := d.left.nodeHashFrom(level, lo, stride)
	rd := d.right.nodeHashFrom(level, lo, stride)
	hi := lo + stride
	equal := bytes.Equal(ld[:], rd[:])

	cmp := Comparison{
		Level:       level,
		Lo:          lo,
		Hi:          hi,
		LeftDigest:  append([]byte(nil), ld[:]...),
		RightDigest: append([]byte(nil), rd[:]...),
		Equal:       equal,
	}
	d.sequence = append(d.sequence, cmp)

	if equal {
		d.logger.LogAttrs(context.Background(), slog.LevelInfo,
			"interval equal, skip",
			slog.Int("level", level), slog.Int("lo", lo), slog.Int("hi", hi),
		)
		return
	}

	if level == 0 {
		lv, lok := d.left.data[lo]
		rv, rok := d.right.data[lo]
		diff := DiffKey{Key: lo, LeftOK: lok, RightOK: rok}
		if lok {
			diff.Left = append([]byte(nil), lv...)
		}
		if rok {
			diff.Right = append([]byte(nil), rv...)
		}
		d.diffs = append(d.diffs, diff)
		d.logger.LogAttrs(context.Background(), slog.LevelWarn,
			"leaf mismatch, record diff",
			slog.Int("key", lo),
			slog.Bool("left_present", lok),
			slog.Bool("right_present", rok),
			slog.Int("left_len", len(lv)),
			slog.Int("right_len", len(rv)),
			slog.String("reason", leafReason(lok, rok, lv, rv)),
		)
		return
	}

	childStride := stride / d.left.fanout
	cmp.DrilledDown = true
	d.sequence[len(d.sequence)-1] = cmp
	d.logger.LogAttrs(context.Background(), slog.LevelInfo,
		"interval mismatch, drill down",
		slog.Int("level", level), slog.Int("lo", lo), slog.Int("hi", hi),
		slog.Int("fanout", d.left.fanout),
	)
	for i := 0; i < d.left.fanout; i++ {
		d.visit(level-1, lo+i*childStride, childStride)
	}
}

func (s snapshot) nodeHashFrom(level, lo, stride int) [32]byte {
	if digest, ok := s.nodeHash[nodeID{level: level, lo: lo}]; ok {
		return digest
	}
	virtualHi := lo + stride
	switch {
	case virtualHi <= s.size:
		return s.normalEmpty[level]
	case lo >= s.size:
		return s.structuralEmpty[level]
	default:
		return s.truncatedEmpty[level]
	}
}

func treeDepth(size, fanout int) int {
	depth := 0
	for stride := 1; stride < size; stride *= fanout {
		depth++
	}
	return depth
}

func ipow(base, exp int) int {
	result := 1
	for i := 0; i < exp; i++ {
		result *= base
	}
	return result
}

func leafReason(lok, rok bool, lv, rv []byte) string {
	switch {
	case lok && !rok:
		return "present on left, absent on right"
	case !lok && rok:
		return "absent on left, present on right"
	case bytes.Equal(lv, rv):
		return "unexpected equal values"
	default:
		return "both present with different values"
	}
}

type diffSummary struct {
	Key    int    `json:"key"`
	Reason string `json:"reason"`
}

func diffSummaries(diffs []DiffKey) []diffSummary {
	out := make([]diffSummary, 0, len(diffs))
	for _, diff := range diffs {
		out = append(out, diffSummary{
			Key:    diff.Key,
			Reason: leafReason(diff.LeftOK, diff.RightOK, diff.Left, diff.Right),
		})
	}
	return out
}
