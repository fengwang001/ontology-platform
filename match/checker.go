package match

type Constructor struct {
	Name       string
	FieldTypes []string
}

type Pattern interface {
	pattern()
}

type Wildcard struct{}

type ConstructorPattern struct {
	Name     string
	Children []Pattern
}

type OrPattern struct {
	Branches []Pattern
}

func (Wildcard) pattern()           {}
func (ConstructorPattern) pattern() {}
func (OrPattern) pattern()          {}

type ArmResult struct {
	Index           int
	BranchRedundant []bool
	Redundant       bool
}

type Counterexample struct {
	Pattern Pattern
}

type CheckResult struct {
	Exhaustive     bool
	Counterexample *Counterexample
	Text           string
}

func (c *Checker) MissingCalls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.missingCalls
}
