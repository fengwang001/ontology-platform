package vv

import "sort"

// Entry is one immutable value version for a key.
type Entry struct {
	Key     string
	Value   string
	Origin  string
	Version Vector
}

// CloneEntry returns an entry with an independent version map.
func CloneEntry(e Entry) Entry {
	return Entry{Key: e.Key, Value: e.Value, Origin: e.Origin, Version: e.Version.Clone()}
}

// OrderKey gives entries a deterministic total order.
func OrderKey(e Entry) string {
	buf := make([]byte, 0, len(e.Key)+len(e.Value)+len(e.Origin))
	for _, id := range sortedKeys(e.Version) {
		buf = appendString(buf, id)
		buf = appendUint64(buf, e.Version[id])
	}
	buf = appendString(buf, e.Value)
	return string(buf)
}

// Less defines the canonical sibling order.
func Less(a, b Entry) bool {
	return OrderKey(a) < OrderKey(b)
}

// SortEntries orders entries deterministically in place.
func SortEntries(entries []Entry) {
	sort.SliceStable(entries, func(i, j int) bool { return Less(entries[i], entries[j]) })
}

// EqualEntries reports deterministic, element-wise equality.
func EqualEntries(a, b []Entry) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if OrderKey(a[i]) != OrderKey(b[i]) || a[i].Key != b[i].Key {
			return false
		}
	}
	return true
}

func sortedKeys(v Vector) []string {
	keys := make([]string, 0, len(v))
	for id := range v {
		keys = append(keys, id)
	}
	sort.Strings(keys)
	return keys
}
