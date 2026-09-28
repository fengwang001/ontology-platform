package ontology

// Logger receives structured decision logs.
type Logger interface {
	Printf(format string, args ...any)
}
