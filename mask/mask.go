// Package mask 按编译后的规则集对记录执行脱敏。
package mask

import (
	"encoding/hex"
	"hash/fnv"
	"reflect"
	"sync/atomic"

	"ontology/record"
	"ontology/rule"
)

const replaced = "***REDACTED***"

// Processor 在不可变规则集上脱敏；计数器线程安全。
type Processor struct {
	set       *rule.Set
	rejected  atomic.Int64
	processed atomic.Int64
}

// New 构造处理器。
func New(set *rule.Set) *Processor { return &Processor{set: set} }

// Rejected 返回因深度/环问题被拒绝的记录数。
func (p *Processor) Rejected() int64 { return p.rejected.Load() }

// Processed 返回成功处理的记录数。
func (p *Processor) Processed() int64 { return p.processed.Load() }

// Apply 返回脱敏后的新记录（不修改原记录）。
func (p *Processor) Apply(r *record.Record) (*record.Record, error) {
	w := &walker{set: p.set, seen: map[uintptr]bool{}}
	out, err := w.doMap(reflect.ValueOf(r.Fields), 1, p.set.Root())
	if err != nil {
		p.rejected.Add(1)
		return nil, err
	}
	p.processed.Add(1)
	return &record.Record{Level: r.Level, TraceID: r.TraceID, Fields: out,
		Incomplete: r.Incomplete}, nil
}

type walker struct {
	set  *rule.Set
	seen map[uintptr]bool
}

func (w *walker) enter(ptr uintptr) bool {
	if w.seen[ptr] {
		return false
	}
	w.seen[ptr] = true
	return true
}

func (w *walker) doMap(rv reflect.Value, depth int, node *rule.Node) (record.Fields, error) {
	if depth > record.MaxDepth {
		return nil, record.ErrDepth
	}
	if !w.enter(rv.Pointer()) {
		return nil, record.ErrCycle
	}
	defer delete(w.seen, rv.Pointer())
	dst := record.Fields{}
	iter := rv.MapRange()
	for iter.Next() {
		key := iter.Key().String()
		raw := iter.Value()
		val := raw
		if val.Kind() == reflect.Interface {
			val = val.Elem()
		}
		entry, child, end := w.set.LookupOn(node, key)
		switch {
		case end:
			dst[key] = applyEntry(entry, val)
		case val.Kind() == reflect.String:
			dst[key] = w.maybeValue(val.String(), val)
		default:
			put, err := w.doAny(val, depth+1, child)
			if err != nil {
				return nil, err
			}
			dst[key] = put
		}
	}
	return dst, nil
}

func (w *walker) doSlice(rv reflect.Value, depth int, node *rule.Node) ([]any, error) {
	if depth > record.MaxDepth {
		return nil, record.ErrDepth
	}
	if !w.enter(rv.Pointer()) {
		return nil, record.ErrCycle
	}
	defer delete(w.seen, rv.Pointer())
	dst := make([]any, rv.Len())
	for i := 0; i < rv.Len(); i++ {
		val := rv.Index(i)
		if val.Kind() == reflect.Interface {
			val = val.Elem()
		}
		if val.Kind() == reflect.String {
			dst[i] = w.maybeValue(val.String(), val)
			continue
		}
		put, err := w.doAny(val, depth+1, node)
		if err != nil {
			return nil, err
		}
		dst[i] = put
	}
	return dst, nil
}

func (w *walker) doAny(val reflect.Value, depth int, node *rule.Node) (any, error) {
	if !val.IsValid() {
		return nil, nil
	}
	switch val.Kind() {
	case reflect.Map:
		return w.doMap(val, depth, node)
	case reflect.Slice:
		return w.doSlice(val, depth, node)
	case reflect.String:
		return w.maybeValue(val.String(), val), nil
	case reflect.Int, reflect.Int64:
		return val.Int(), nil
	case reflect.Float64, reflect.Float32:
		return val.Float(), nil
	case reflect.Bool:
		return val.Bool(), nil
	default:
		return val.Interface(), nil
	}
}

func (w *walker) maybeValue(s string, rv reflect.Value) any {
	if es := w.set.MatchValue(s); len(es) > 0 {
		return applyEntry(es[0], rv)
	}
	return s
}

func applyEntry(e rule.Entry, rv reflect.Value) any {
	s := rv.String()
	switch e.Action {
	case rule.ActionHash:
		return HashValue(s)
	case rule.ActionTruncate:
		r := []rune(s)
		if len(r) <= e.Keep {
			return s
		}
		return string(r[:e.Keep]) + "\u2026(truncated)"
	default: // ActionReplace
		return replaced
	}
}

// HashValue 返回值的 FNV-1a-64 十六进制摘要。
func HashValue(s string) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	return "h:" + hex.EncodeToString(h.Sum(nil))
}
