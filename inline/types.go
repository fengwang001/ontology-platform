package inline

import "errors"

// Reason is the rejection reason recorded for a non-inlined call site.
// The integer order is the fixed reporting priority: when several causes hold
// for one site, the numerically smallest one is reported.
//
//	undefined callee > noinline > direct recursion >
//	uninlinable structure > chain exceeded > budget
type Reason int

const (
	ReasonUndefinedCallee Reason = iota
	ReasonNoInline
	ReasonDirectRecursion
	ReasonUninlinableStructure
	ReasonChainExceeded
	ReasonBudget
)

var reasonNames = [...]string{
	"undefined-callee",
	"noinline-flag",
	"direct-recursion",
	"uninlinable-structure",
	"expansion-chain-exceeded",
	"budget-exceeded",
}

func (r Reason) String() string {
	if int(r) < 0 || int(r) >= len(reasonNames) {
		return "unknown"
	}
	return reasonNames[r]
}

// Flag is an inlining annotation on a function.
type Flag int

const (
	FlagNone Flag = iota
	FlagNoInline
	FlagAlwaysInline
)

// Func is one input function definition.
type Func struct {
	Name        string
	Size        int
	Flags       Flag
	Uninlinable bool
	// BaseHeat is the heat scale of the function body. A copied site's heat is
	// its original heat times the ratio outerHeat / callee.BaseHeat. Zero means
	// "derive": the maximum heat of the function's own sites; for functions
	// without sites it means 1.
	BaseHeat float64
	Sites    []Site
}

// Site is one static call site inside a function body.
type Site struct {
	Callee string
	Heat   float64
}

// Config holds the fixed budget rules of one decision task.
type Config struct {
	GlobalSizeLimit   int
	GrowthMultiple    int
	GrowthMultipleDen int
	CallOverhead      int

	// MaxChainOccurrences bounds how many times one function may occur on a
	// single expansion chain. Inlining the occurrence that would exceed it is
	// rejected.
	MaxChainOccurrences int
}

// withDefaults fills the fixed defaults: budget 1x growth, global cap 1000,
// call overhead 1, at most 3 occurrences of one function per chain.
func (c Config) withDefaults() Config {
	d := c
	if d.GrowthMultiple <= 0 {
		d.GrowthMultiple = 1
	}
	if d.GrowthMultipleDen <= 0 {
		d.GrowthMultipleDen = 1
	}
	if d.GlobalSizeLimit <= 0 {
		d.GlobalSizeLimit = 1000
	}
	if d.CallOverhead < 0 {
		d.CallOverhead = 0
	}
	if d.MaxChainOccurrences <= 0 {
		d.MaxChainOccurrences = 3
	}
	return d
}

// validate reports malformed, non-deterministic inputs.
func (f Func) validate() error {
	if f.Size < 0 {
		return errors.New("inline: negative function size")
	}
	if f.Flags < FlagNone || f.Flags > FlagAlwaysInline {
		return errors.New("inline: invalid function flag")
	}
	for _, s := range f.Sites {
		if s.Heat < 0 || isNaN(s.Heat) || isInf(s.Heat) {
			return errors.New("inline: site heat must be finite and non-negative")
		}
	}
	if f.BaseHeat != 0 && (f.BaseHeat < 0 || isNaN(f.BaseHeat) || isInf(f.BaseHeat)) {
		return errors.New("inline: base heat must be finite and non-negative")
	}
	return nil
}

func isNaN(f float64) bool { return f != f }

func isInf(f float64) bool { return f > 1e308 || f < -1e308 }
