package ontology

import (
	"bytes"
	"encoding/json"
	"sort"
)

// snapshot is an immutable, byte-identical view of one committed version.
// It is never mutated after construction: all commit paths build a new
// snapshot from the previous one, satisfying the requirement that reads of
// any version remain byte-identical forever.
type snapshot struct {
	version Version
	deleted bool
	// props holds the values in sorted property order, used for both
	// iteration and deterministic byte encoding.
	props []snapshotProp
	raw   []byte
}

type snapshotProp struct {
	k Property
	v Value
}

func (s *snapshot) Version() Version { return s.version }
func (s *snapshot) Deleted() bool    { return s.deleted }
func (s *snapshot) Properties() []Property {
	out := make([]Property, len(s.props))
	for i, p := range s.props {
		out[i] = p.k
	}
	return out
}

func (s *snapshot) Get(p Property) (Value, bool) {
	i := sort.Search(len(s.props), func(i int) bool { return s.props[i].k >= p })
	if i < len(s.props) && s.props[i].k == p {
		return s.props[i].v, true
	}
	return nil, false
}

// Bytes returns the canonical JSON encoding of the version. The encoding is
// derived from the sorted props slice so it is stable across merges.
func (s *snapshot) Bytes() []byte {
	out := make([]byte, len(s.raw))
	copy(out, s.raw)
	return out
}

func encodeSnapshot(version Version, deleted bool, props []snapshotProp) *snapshot {
	var buf bytes.Buffer
	buf.WriteString(`{"version":`)
	buf.Write(jsonNumber(int64(version)))
	buf.WriteString(`,"deleted":`)
	if deleted {
		buf.WriteString("true")
	} else {
		buf.WriteString("false")
	}
	buf.WriteString(`,"properties":{`)
	for i, p := range props {
		if i > 0 {
			buf.WriteByte(',')
		}
		key, _ := json.Marshal(string(p.k))
		buf.Write(key)
		buf.WriteByte(':')
		val, err := json.Marshal(p.v)
		if err != nil {
			val = []byte("null")
		}
		buf.Write(val)
	}
	buf.WriteString("}}")
	return &snapshot{version: version, deleted: deleted, props: props, raw: buf.Bytes()}
}

func jsonNumber(n int64) []byte {
	if n == 0 {
		return []byte("0")
	}
	var neg bool
	if n < 0 {
		neg = true
		n = -n
	}
	var b [24]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return b[i:]
}

// cloneValues deep-codes values through JSON so later caller mutation cannot
// affect committed state.
func cloneValues(in map[Property]Value) map[Property]Value {
	if len(in) == 0 {
		return map[Property]Value{}
	}
	raw, err := json.Marshal(in)
	if err != nil {
		out := make(map[Property]Value, len(in))
		for k, v := range in {
			out[k] = v
		}
		return out
	}
	var tmp map[string]Value
	if err := json.Unmarshal(raw, &tmp); err != nil {
		out := make(map[Property]Value, len(in))
		for k, v := range in {
			out[k] = v
		}
		return out
	}
	out := make(map[Property]Value, len(tmp))
	for k, v := range tmp {
		out[Property(k)] = v
	}
	return out
}

// mergeProps applies values onto baseProps. Absent property keys are removed
// (explicit nil marks a property absent); everything else is overwritten.
func mergeProps(base []snapshotProp, values map[Property]Value) []snapshotProp {
	merged := make(map[Property]Value, len(base)+len(values))
	for _, p := range base {
		merged[p.k] = p.v
	}
	for k, v := range values {
		if v == nil {
			delete(merged, k)
			continue
		}
		merged[k] = v
	}
	out := make([]snapshotProp, 0, len(merged))
	for k, v := range merged {
		out = append(out, snapshotProp{k, v})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].k < out[j].k })
	return out
}

var _ Snapshot = (*snapshot)(nil)
