package match

type Matcher struct{}

func New(windowCap, chainLimit int) (*Matcher, error) { return nil, nil }

type Result struct {
	Distance int
	Length   int
}
