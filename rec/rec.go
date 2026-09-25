// Package rec defines log records and segment dedup semantics.
package rec

// Rec is a single appended record with a globally increasing seq.
type Rec struct {
	Key string
	Val string
	Seq int64
}

// Bytes is the logical byte size of a record: len(Key)+len(Val).
func (r Rec) Bytes() int64 {
	return int64(len(r.Key) + len(r.Val))
}

// SumBytes sums the logical bytes of rs.
func SumBytes(rs []Rec) int64 {
	var n int64
	for _, r := range rs {
		n += r.Bytes()
	}
	return n
}

// MergeLatest dedups rs by Key, keeping only the record with the
// largest Seq per key, independent of input order.
func MergeLatest(rs []Rec) []Rec {
	best := make(map[string]Rec, len(rs))
	order := make([]string, 0, len(rs))
	for _, r := range rs {
		cur, ok := best[r.Key]
		if !ok {
			order = append(order, r.Key)
		}
		if !ok || r.Seq > cur.Seq {
			best[r.Key] = r
		}
	}
	out := make([]Rec, 0, len(order))
	for _, k := range order {
		out = append(out, best[k])
	}
	return out
}
