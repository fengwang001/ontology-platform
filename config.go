package ontology

import "fmt"

type Config struct {
	PointerSize       int
	PointerAlignment  int
	MinCompositeSize  int
	MinCompositeAlign int
	MaxSize           int
	AllowedAlignments map[int]bool
	MaxAlign          int
	Logger            Logger
}

type Logger interface {
	Log(input string, output string, reason string)
}

type StdLogger struct{}

func (StdLogger) Log(input, output, reason string) {
	fmt.Printf("input=%s output=%s reason=%s\n", input, output, reason)
}
