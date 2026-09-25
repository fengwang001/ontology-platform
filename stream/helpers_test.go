package stream_test

import (
	"bytes"
	"errors"
	"testing"

	"ontology/stream"
)

func runAll(dir stream.Dir, in []byte, cfg stream.Config) ([]byte, stream.Stats, error) {
	cfg.Dir = dir
	tr := stream.New(cfg)
	if _, err := tr.Write(in); err != nil {
		return tr.Output(), tr.Stats(), err
	}
	err := tr.Close()
	return tr.Output(), tr.Stats(), err
}

// splitAll 以 1..len+1 的每种切分跑一遍，断言与整段一致。
func splitAll(t *testing.T, dir stream.Dir, in []byte, cfg stream.Config) {
	t.Helper()
	want, wstats, werr := runAll(dir, in, cfg)
	for cut := 1; cut <= len(in)+1; cut++ {
		cfg.Dir = dir
		tr := stream.New(cfg)
		var gotErr error
		for i := 0; i < len(in); i += cut {
			j := i + cut
			if j > len(in) {
				j = len(in)
			}
			if _, err := tr.Write(in[i:j]); err != nil {
				gotErr = err
				break
			}
		}
		if gotErr == nil {
			gotErr = tr.Close()
		}
		if (werr == nil) != (gotErr == nil) ||
			(werr != nil && werr.Error() != gotErr.Error()) {
			t.Fatalf("cut=%d err want %v got %v", cut, werr, gotErr)
		}
		var wi, gi *stream.InvalidError
		if errors.As(werr, &wi) && (!errors.As(gotErr, &gi) || *wi != *gi) {
			t.Fatalf("cut=%d invalid err %#v vs %#v", cut, wi, gi)
		}
		if !bytes.Equal(want, tr.Output()) {
			t.Fatalf("cut=%d out want %x got %x", cut, want, tr.Output())
		}
		if wstats != tr.Stats() {
			t.Fatalf("cut=%d stats %#v vs %#v", cut, wstats, tr.Stats())
		}
	}
}
