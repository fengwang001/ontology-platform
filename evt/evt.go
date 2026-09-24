// Package evt defines the ordering relation for timestamped events.
// It depends on no other package.
package evt

// Event is a single (Key, TS) occurrence identity.
type Event struct {
	Key string
	TS  int64
}

// Less reports whether a sorts before b: TS ascending, then Key lexicographic.
func Less(a, b Event) bool {
	if a.TS != b.TS {
		return a.TS < b.TS
	}
	return a.Key < b.Key
}

// Equal reports whether a and b are the same (Key, TS).
func Equal(a, b Event) bool {
	return a.TS == b.TS && a.Key == b.Key
}
