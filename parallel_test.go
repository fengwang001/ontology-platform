package ontology_test

import "bytes"
import "errors"
import "math/rand"
import "sync"
import "testing"
import "ontology/dec"
import "ontology/enc"

func parallelData() []byte {
	rnd := rand.New(rand.NewSource(7))
	r := make([]byte, 1<<18)
	rnd.Read(r)
	data := bytes.Repeat([]byte("ontology-platform-lz77-block-"), 20000)
	return append(data, r...)
}

func TestParallelDeterministic(t *testing.T) {
	data := parallelData()
	base, err := enc.CompressParallel(data, 64<<10, 1, cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range []int{2, 4, 8} {
		got, err := enc.CompressParallel(data, 64<<10, w, cfg)
		if err != nil || !bytes.Equal(got, base) {
			t.Fatalf("workers=%d 输出与 workers=1 不同", w)
		}
	}
	for i := 0; i < 30; i++ { // 同一输入反复压缩结果一致
		got, _ := enc.CompressParallel(data, 64<<10, 8, cfg)
		if !bytes.Equal(got, base) {
			t.Fatalf("第 %d 次并行压缩结果不同", i)
		}
	}
	if out, err := decode(t, base, 4096); err != nil || !bytes.Equal(out, data) {
		t.Fatal("并行压缩结果无法被流式解压器还原")
	}
}

func TestConcurrentDecoders(t *testing.T) {
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for g := 0; g < 8; g++ {
		data := bytes.Repeat([]byte{byte(g), byte(g + 1)}, 10000)
		stream := encode(t, data, 4096)
		wg.Go(func() {
			d, _ := dec.New(dcfg())
			if _, err := d.Write(stream); err != nil {
				errs <- err
			} else if err := d.Close(); err != nil {
				errs <- err
			} else if !bytes.Equal(d.Output(), data) {
				errs <- errMismatch
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

var errMismatch = errors.New("解压结果与原文不符")

func TestCandidateBounds(t *testing.T) {
	rnd := rand.New(rand.NewSource(3))
	r64, r4m := make([]byte, 64<<10), make([]byte, 4<<20)
	rnd.Read(r64)
	rnd.Read(r4m)
	cases := []struct {
		name string
		data []byte
	}{
		{"same64K", bytes.Repeat([]byte{7}, 64<<10)},
		{"same4M", bytes.Repeat([]byte{7}, 4<<20)},
		{"rand64K", r64},
		{"rand4M", r4m},
	}
	perByte := map[string]float64{}
	for _, c := range cases {
		var buf bytes.Buffer
		e, _ := enc.New(&buf, cfg)
		e.Write(c.data)
		e.Close()
		cand := e.Candidates()
		if max := int64(cfg.MaxChain) * int64(len(c.data)); cand > max {
			t.Errorf("%s: 考察数 %d 超过上限 %d", c.name, cand, max)
		}
		perByte[c.name] = float64(cand) / float64(len(c.data))
		t.Logf("%s: 考察数=%d 每字节=%.6f 压缩后=%d", c.name, cand, perByte[c.name], buf.Len())
		if c.name == "same4M" && buf.Len() >= 64<<10 {
			t.Errorf("4MB 全同串压缩后 %d 字节，必须小于 64KB", buf.Len())
		}
	}
	a, b := perByte["same64K"], perByte["same4M"]
	if a > 2*b || b > 2*a {
		t.Errorf("两档每字节考察数相差超过 2 倍: %g vs %g", a, b)
	}
}
