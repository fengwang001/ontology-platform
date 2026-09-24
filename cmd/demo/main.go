package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"ontology/ckpt"
	"ontology/parse"
	"ontology/sink"
	"ontology/source"
)

func main() {
	fails := 0
	check := func(name string, ok bool) {
		if ok {
			fmt.Println("OK", name)
			return
		}
		fmt.Println("FAIL", name)
		fails++
	}

	check("skeleton runs", true)

	gen := source.NewGen(source.GenConfig{N: 5, ErrAt: 3}, 0)
	got := 0
	var srcErr error
	for {
		_, err := gen.Next(context.Background())
		if err != nil {
			srcErr = err
			break
		}
		got++
	}
	check("source yields frames then reports mid error",
		got == 3 && errors.Is(srcErr, source.ErrEnd))

	parser := &parse.Parser{}
	if _, ok := parser.Parse(source.Frame{Data: []byte("a=1")}); !ok {
		fails++
	}
	_, okBad := parser.Parse(source.Frame{Data: []byte("junk")})
	check("parse counts bad records and continues",
		!okBad && parser.Bad == 1)

	sdir, _ := os.MkdirTemp("", "sinkdemo")
	defer os.RemoveAll(sdir)
	sk := sink.New(sdir, 0, nil, -1)
	out, err := sk.Commit(4, []sink.Row{{Key: "a", Sum: 2, Cnt: 2}})
	tmp := out + ".tmp"
	os.WriteFile(tmp, []byte("partial-no-checksum"), 0o644)
	truncBad := sink.Verify(tmp) != nil
	removed, _ := sink.CleanupTemp(sdir)
	_, latestPos, _ := sink.LatestOutput(sdir)
	check("truncated temp rejected cleaned valid output kept",
		err == nil && truncBad && removed == 1 && latestPos == 4)

	cdir, _ := os.MkdirTemp("", "ckptdemo")
	defer os.RemoveAll(cdir)
	cs := ckpt.NewStore(cdir)
	cs.Save(ckpt.State{Pos: 10, Groups: map[string]ckpt.Group{"a": {Sum: 1, Cnt: 1}}})
	cs.Save(ckpt.State{Pos: 20, Groups: map[string]ckpt.Group{"a": {Sum: 2, Cnt: 2}}})
	cs.Corrupt(1)
	cst, fellBack, _ := cs.Load()
	check("corrupt newest checkpoint falls back to older",
		cst.Position() == 10 && fellBack)

	if fails == 0 {
		fmt.Printf("TOTAL 0 FAIL\n")
		return
	}
	fmt.Printf("TOTAL %d FAIL\n", fails)
}
