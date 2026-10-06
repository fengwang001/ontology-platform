package apf

import (
	"fmt"
	"sort"
	"time"
)

// Distinguisher selects how requests within a level are split into flows.
type Distinguisher int

const (
	// ByUser distinguishes flows by the request's user.
	ByUser Distinguisher = iota
	// ByNamespace distinguishes flows by the request's namespace.
	ByNamespace
)

// Rule classifies a request into a level and a flow. Rules are considered in
// ascending Precedence order; ties break by ascending Name. The first
// matching rule wins. Users, Verbs and Resources are sets: an empty set
// matches everything, a non-empty set matches requests whose value belongs
// to it. The three conditions combine with AND.
type Rule struct {
	Name          string
	Precedence    int
	Users         []string
	Verbs         []string
	Resources     []string
	Level         string
	DistinguishBy Distinguisher
}

// Level is a priority level. An exempt level executes requests immediately
// without seats or queues. A limited level has shares (for nominal seat
// allocation), a queue length limit and a queue timeout.
type Level struct {
	Name         string
	Exempt       bool
	Shares       int
	QueueLimit   int
	QueueTimeout time.Duration
}

// Config is the full controller configuration, replaced atomically on
// update.
type Config struct {
	TotalSeats int
	Rules      []Rule
	Levels     []Level
}

// validate checks the configuration; any problem yields KindInvalidArgument.
func (c Config) validate() error {
	if c.TotalSeats < 0 {
		return errf(KindInvalidArgument, "total seats must be >= 0")
	}
	levels := make(map[string]bool, len(c.Levels))
	for _, l := range c.Levels {
		if l.Name == "" {
			return errf(KindInvalidArgument, "level name must not be empty")
		}
		if levels[l.Name] {
			return errf(KindInvalidArgument, fmt.Sprintf("duplicate level %q", l.Name))
		}
		levels[l.Name] = true
		if l.Exempt {
			continue
		}
		if l.Shares < 0 {
			return errf(KindInvalidArgument, fmt.Sprintf("level %q: negative shares", l.Name))
		}
		if l.QueueLimit < 0 {
			return errf(KindInvalidArgument, fmt.Sprintf("level %q: negative queue limit", l.Name))
		}
		if l.QueueTimeout < 0 {
			return errf(KindInvalidArgument, fmt.Sprintf("level %q: negative queue timeout", l.Name))
		}
	}
	rules := make(map[string]bool, len(c.Rules))
	for _, r := range c.Rules {
		if r.Name == "" {
			return errf(KindInvalidArgument, "rule name must not be empty")
		}
		if rules[r.Name] {
			return errf(KindInvalidArgument, fmt.Sprintf("duplicate rule %q", r.Name))
		}
		rules[r.Name] = true
		if !levels[r.Level] {
			return errf(KindInvalidArgument, fmt.Sprintf("rule %q: unknown level %q", r.Name, r.Level))
		}
		if r.DistinguishBy != ByUser && r.DistinguishBy != ByNamespace {
			return errf(KindInvalidArgument, fmt.Sprintf("rule %q: invalid distinguisher", r.Name))
		}
	}
	return nil
}

// allocateNominalSeats distributes totalSeats among the limited levels in
// proportion to their shares: each level gets floor(total*share/sumShares)
// and the rounding remainder is handed out one seat at a time, ordered by
// descending shares then ascending name.
func allocateNominalSeats(levels []Level, totalSeats int) map[string]int {
	nominal := make(map[string]int, len(levels))
	sumShares := 0
	for _, l := range levels {
		if !l.Exempt {
			nominal[l.Name] = 0
			sumShares += l.Shares
		}
	}
	if sumShares == 0 {
		return nominal
	}
	assigned := 0
	for _, l := range levels {
		if l.Exempt {
			continue
		}
		n := totalSeats * l.Shares / sumShares
		nominal[l.Name] = n
		assigned += n
	}
	remainder := totalSeats - assigned
	ordered := make([]Level, 0, len(levels))
	for _, l := range levels {
		if !l.Exempt {
			ordered = append(ordered, l)
		}
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].Shares != ordered[j].Shares {
			return ordered[i].Shares > ordered[j].Shares
		}
		return ordered[i].Name < ordered[j].Name
	})
	for i := 0; i < remainder && i < len(ordered); i++ {
		nominal[ordered[i].Name]++
	}
	return nominal
}
