package aggview

// Logger receives one human-readable line per significant event.
type Logger interface {
	Printf(format string, args ...any)
}
