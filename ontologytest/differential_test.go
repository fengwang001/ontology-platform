package ontologytest

import (
	"bytes"
	"testing"
)

func TestDifferentialRandomized(t *testing.T) {
	var buf bytes.Buffer
	for seed := int64(1); seed <= 12; seed++ {
		buf.Reset()
		if err := RunDifferential(DifferentialConfig{
			Seed:       seed,
			Iterations: 300,
			Logger:     &buf,
		}); err != nil {
			t.Fatalf("seed %d: %v\n--- log tail ---\n%s", seed, err, tail(&buf))
		}
	}
}

func tail(buf *bytes.Buffer) string {
	s := buf.String()
	const max = 4000
	if len(s) > max {
		s = s[len(s)-max:]
	}
	return s
}
