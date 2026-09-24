package stack

// Frame is one named call frame on a root-to-leaf stack.
type Frame = string

// Normalize removes adjacent duplicate frames and truncates beyond maxDepth.
// A non-positive maxDepth means unlimited depth.
func Normalize(frames []string, maxDepth int) []string {
	normalized := make([]string, 0, len(frames))
	for _, frame := range frames {
		if len(normalized) > 0 && normalized[len(normalized)-1] == frame {
			continue
		}
		normalized = append(normalized, frame)
	}
	if maxDepth > 0 && len(normalized) > maxDepth {
		return append([]string(nil), normalized[:maxDepth]...)
	}
	return normalized
}

// Truncated reports whether the normalized representation lost suffix frames.
func Truncated(frames []string, maxDepth int) bool {
	return maxDepth > 0 && len(frames) > maxDepth
}
