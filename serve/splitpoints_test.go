package serve_test

import (
	"testing"

	"ontology/source"
)

// fixedWriter 每次 Write 精确接受 step 字节（除最后一次），
// 用来把整段输出强制切成所有可能的固定长度。
type fixedWriter struct {
	step int
	buf  []byte
}

func (w *fixedWriter) Write(p []byte) (int, error) {
	n := len(p)
	if n > w.step {
		n = w.step
	}
	w.buf = append(w.buf, p[:n]...)
	return n, nil
}

// TestEverySplitPointProducesSameBytes：对 1..total 的每个切分长度，
// 从断点续写拼回的字节必须与参考响应体逐字节相同。
func TestEverySplitPointProducesSameBytes(t *testing.T) {
	data := makeData(60)

	// 多区间封装体作为参考。
	header := "bytes=0-9,20-29,45-59"
	ref := build(t, header, source.NewMemory(data))
	want := drainAll(t, ref)

	total := len(want)
	for step := 1; step <= total; step++ {
		a := build(t, header, source.NewMemory(data))
		w := &fixedWriter{step: step}
		for !a.Done() {
			if _, err := a.WriteTo(w); err != nil {
				t.Fatalf("step=%d write: %v", step, err)
			}
		}
		if len(w.buf) != total {
			t.Fatalf("step=%d length=%d want %d", step, len(w.buf), total)
		}
		for i := 0; i < total; i++ {
			if w.buf[i] != want[i] {
				t.Fatalf("step=%d byte %d mismatch", step, i)
			}
		}
	}
}

// TestResumeAcrossCalls：即使接收方在中途返回错误，游标仍保留，
// 换新接收方可从断点继续并最终得到完整字节。
func TestResumeAcrossCalls(t *testing.T) {
	data := makeData(30)
	ref := drainAll(t, build(t, "bytes=0-9,20-24", source.NewMemory(data)))

	a := build(t, "bytes=0-9,20-24", source.NewMemory(data))
	w1 := &fixedWriter{step: 3}
	for a.Written() < int64(len(ref)/2) {
		if _, err := a.WriteTo(w1); err != nil {
			t.Fatal(err)
		}
	}
	saved := a.Written()

	w2 := &fixedWriter{step: 5}
	for !a.Done() {
		if _, err := a.WriteTo(w2); err != nil {
			t.Fatal(err)
		}
	}
	combined := append(append([]byte(nil), w1.buf...), w2.buf...)
	if a.Written() != saved+int64(len(w2.buf)) {
		t.Fatal("written cursor not preserved across receivers")
	}
	for i := range ref {
		if combined[i] != ref[i] {
			t.Fatalf("resume byte %d mismatch", i)
		}
	}
}
