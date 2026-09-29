package recall

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"testing"
)

func testLogger() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return logger, &buf
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func mustOpen(t *testing.T, ctx context.Context, r *Recorder) (uint64, int64) {
	t.Helper()
	id, wm, err := r.OpenSnapshot(ctx)
	if err != nil {
		t.Fatalf("OpenSnapshot: %v", err)
	}
	return id, wm
}

func appendN(t *testing.T, ctx context.Context, r *Recorder, n int64) {
	t.Helper()
	for i := int64(0); i < n; i++ {
		want := r.nextSeq + 1
		seq, err := r.Append(ctx, []byte("r"+itoa(want)))
		if err != nil || seq != want {
			t.Fatalf("Append #%d = %d, %v; want %d, nil", i+1, seq, err, want)
		}
	}
}

func itoa(i int64) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var b [24]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		b[pos] = '-'
	}
	return string(b[pos:])
}
