package match

// Matcher is a hash-chain longest-match finder.
type Matcher struct{}

// New creates a Matcher over a window capacity with a bounded chain length.
func New(windowCap, chainLimit int) *Matcher { return &Matcher{} }
