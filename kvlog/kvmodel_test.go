package kvlog

import (
	"hash/crc32"
	"os"
	"sort"
)

// naiveModel is an independently written in-memory reference for the logical
// keyspace. It knows nothing about segments: Put/Delete update the newest
// state of a key, Merge is logically a no-op, reopen re-derives nothing
// (the test rebuilds the model from the operation stream instead where
// needed; here it simply persists because operations are identical).
type modelState int

const (
	modelMissing modelState = iota
	modelPresent
	modelDeleted
)

type naiveModel struct {
	state map[string]modelState
	value map[string]string
}

func newNaiveModel() *naiveModel {
	return &naiveModel{
		state: make(map[string]modelState),
		value: make(map[string]string),
	}
}

func (m *naiveModel) put(k, v string) {
	m.state[k] = modelPresent
	m.value[k] = v
}

func (m *naiveModel) del(k string) {
	m.state[k] = modelDeleted
	delete(m.value, k)
}

func (m *naiveModel) get(k string) (modelState, string) {
	switch m.state[k] {
	case modelPresent:
		return modelPresent, m.value[k]
	case modelDeleted:
		return modelDeleted, ""
	default:
		return modelMissing, ""
	}
}

func (m *naiveModel) keys() []string {
	ks := make([]string, 0, len(m.state))
	for k := range m.state {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

// snapshot is a full logical dump used for exact-equality comparison after
// reopen / merge / crash recovery.
type snapshot struct {
	state map[string]modelState
	value map[string]string
}

func (m *naiveModel) snapshot() snapshot {
	s := snapshot{
		state: make(map[string]modelState, len(m.state)),
		value: make(map[string]string, len(m.value)),
	}
	for k, v := range m.state {
		s.state[k] = v
	}
	for k, v := range m.value {
		s.value[k] = v
	}
	return s
}

func dumpEngine(e *Engine) snapshot {
	s := snapshot{
		state: make(map[string]modelState),
		value: make(map[string]string),
	}
	for key, loc := range e.dir2.m {
		if loc.tomb {
			s.state[key] = modelDeleted
			continue
		}
		f, err := os.Open(e.segPath(loc.segment))
		if err != nil {
			s.state[key] = modelMissing
			continue
		}
		buf := make([]byte, loc.length)
		if _, err := f.ReadAt(buf, loc.offset); err != nil {
			f.Close()
			s.state[key] = modelMissing
			continue
		}
		f.Close()
		if u32(buf[0:]) != crc32.Checksum(buf[4:], crcTable) ||
			u64(buf[4:]) != loc.seq {
			s.state[key] = modelDeleted
			continue
		}
		klen := int(u32(buf[12:]))
		vlen := int(u32(buf[16:]))
		if recordHeaderLen+klen+vlen != loc.length ||
			string(buf[recordHeaderLen:recordHeaderLen+klen]) != key {
			s.state[key] = modelDeleted
			continue
		}
		s.state[key] = modelPresent
		s.value[key] = string(buf[recordHeaderLen+klen:])
	}
	return s
}

func snapshotsEqual(a, b snapshot) bool {
	if len(a.state) != len(b.state) {
		return false
	}
	for k, v := range a.state {
		if b.state[k] != v {
			return false
		}
	}
	for k, v := range a.value {
		if b.value[k] != v {
			return false
		}
	}
	return true
}
