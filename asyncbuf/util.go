package asyncbuf

import "fmt"

func sprintf(format string, args ...any) string { return fmt.Sprintf(format, args...) }

// String renders mode names in logs and user output.
func (m Mode) String() string { return modeName(m) }

// String renders event kinds in logs.
func (k EventKind) String() string {
	if k == WatermarkEvent {
		return "watermark"
	}
	return "element"
}
