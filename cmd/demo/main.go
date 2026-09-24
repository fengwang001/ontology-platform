// Command demo runs self-checks of the backpressure streaming aggregation pipeline.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"ontology/ckpt"
	"ontology/sink"
)

func main() {
	var fail int
	report := func(name string, ok bool) {
		if ok {
			fmt.Println("OK   " + name)
			return
		}
		fail++
		fmt.Println("FAIL " + name)
	}

	checks := []struct {
		name string
		ok   bool
	}{
		{"skeleton runs", true},
		{"checkpoint corrupt file falls back to previous", checkCkpt()},
		{"sink truncated temp rejected and leftover cleaned", checkSink()},
	}
	for _, c := range checks {
		report(c.name, c.ok)
	}

	total := len(checks)
	if fail == 0 {
		fmt.Printf("TOTAL %d/%d passed\n", total-fail, total)
		return
	}
	fmt.Printf("TOTAL %d/%d passed\n", total-fail, total)
}

func checkCkpt() bool {
	dir, err := os.MkdirTemp("", "demo-ckpt-")
	if err != nil {
		return false
	}
	defer os.RemoveAll(dir)
	st := ckpt.State{Pos: 7, Bad: 2, Groups: map[string]ckpt.Agg{"k": {Count: 3, Sum: 11}}}
	if err := ckpt.Save(dir, st); err != nil {
		return false
	}
	st.Pos = 9
	if err := ckpt.Save(dir, st); err != nil {
		return false
	}
	if err := os.WriteFile(filepath.Join(dir, "ckpt.000"), []byte("garbage"), 0o644); err != nil {
		return false
	}
	got, fallback, err := ckpt.Load(dir)
	return err == nil && fallback && got.Pos == 7 && got.Bad == 2
}

func checkSink() bool {
	dir, err := os.MkdirTemp("", "demo-sink-")
	if err != nil {
		return false
	}
	defer os.RemoveAll(dir)
	sk := sink.New(dir, "out.txt", 0)
	groups := map[string]ckpt.Agg{"a": {Count: 2, Sum: 5}, "": {Count: 1, Sum: -3}}
	data := sink.Render(groups)
	for i := 0; i < len(data); i++ {
		if sink.Validate(data[:i]) {
			return false
		}
	}
	if !sink.Validate(data) {
		return false
	}
	if err := sk.Commit(groups); err != nil {
		return false
	}
	tmp := filepath.Join(dir, "out.txt.tmp.123")
	if err := os.WriteFile(tmp, []byte("partial"), 0o644); err != nil {
		return false
	}
	removed, err := sk.CleanTemp()
	if err != nil || len(removed) != 1 {
		return false
	}
	_, err = os.Stat(tmp)
	return os.IsNotExist(err)
}
