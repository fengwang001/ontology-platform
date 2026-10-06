package exam

// Lifecycle is the question lifecycle: Available -> Suspended -> Retired,
// Available -> Retired, Suspended -> Available. Retired is terminal.
type Lifecycle int

const (
	Available Lifecycle = iota
	Suspended
	Retired
)

func (l Lifecycle) String() string {
	switch l {
	case Available:
		return "available"
	case Suspended:
		return "suspended"
	case Retired:
		return "retired"
	}
	return "unknown"
}

// Version is one immutable revision of a question. Number is monotonically
// increasing per question, starting at 1.
type Version struct {
	Number     int
	Score      int
	Difficulty int
	Knowledge  map[string]bool
}

func (v Version) cloneKnowledge() map[string]bool {
	out := make(map[string]bool, len(v.Knowledge))
	for k := range v.Knowledge {
		out[k] = true
	}
	return out
}

// Question owns its full version history. Old versions never change.
type Question struct {
	ID       string
	Life     Lifecycle
	Versions []Version // Versions[i].Number == i+1
}

func (q *Question) latest() *Version { return &q.Versions[len(q.Versions)-1] }
