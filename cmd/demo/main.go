package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"

	"ontology/api"
	"ontology/esc"
	"ontology/frame"
)

var allOK = true

func line(ok bool, msg string) {
	if ok {
		fmt.Println("OK  " + msg)
	} else {
		allOK = false
		fmt.Println("FAIL " + msg)
	}
}

func hexs(b []byte) string {
	s := ""
	for i, x := range b {
		if i > 0 {
			s += " "
		}
		s += fmt.Sprintf("%02X", x)
	}
	return s
}

func concurrentOK() bool {
	a := api.New()
	src := a.EncodeFrames([][]byte{[]byte("hi"), []byte("a\nb\\x"), {}, []byte("\n")})
	base, _ := a.DecodeFrames(src)
	n := 24
	lists := make([][][]byte, n)
	serial := make([][]byte, n)
	for g := range lists {
		lists[g] = [][]byte{[]byte(fmt.Sprintf("g%d-\n-\\", g)), {}, []byte("end")}
		serial[g] = a.EncodeFrames(lists[g])
	}
	var decOK, encOK atomic.Bool
	decOK.Store(true)
	encOK.Store(true)
	var wg sync.WaitGroup
	for g := 0; g < n; g++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			got, err := a.DecodeFrames(src)
			if err != nil || len(got) != len(base) {
				decOK.Store(false)
				return
			}
			for i := range got {
				if !bytes.Equal(got[i], base[i]) {
					decOK.Store(false)
				}
			}
		}()
		go func(g int) {
			defer wg.Done()
			if !bytes.Equal(a.EncodeFrames(lists[g]), serial[g]) {
				encOK.Store(false)
			}
		}(g)
	}
	wg.Wait()
	return decOK.Load() && encOK.Load()
}

func main() {
	a := api.New()
	three := [][]byte{[]byte("hi"), []byte("a\nb"), []byte("x\\y")}
	wantFrames := []string{"68 69 0A", "61 5C 6E 62 0A", "78 5C 5C 79 0A"}
	gotFrames := make([]string, 3)
	for i := range three {
		gotFrames[i] = hexs(a.EncodeFrames([][]byte{three[i]}))
	}
	line(gotFrames[0] == wantFrames[0] && gotFrames[1] == wantFrames[1] && gotFrames[2] == wantFrames[2],
		"three frame escapes: "+gotFrames[0]+" | "+gotFrames[1]+" | "+gotFrames[2])

	stream := a.EncodeFrames(three)
	line(hexs(stream) == "68 69 0A 61 5C 6E 62 0A 78 5C 5C 79 0A", "full stream: "+hexs(stream))

	dec, err := a.DecodeFrames(stream)
	round := err == nil && len(dec) == 3 && bytes.Equal(dec[1], []byte("a\nb")) && bytes.Equal(dec[2], []byte("x\\y"))
	line(round, "round trip byte-identical")

	empty, err := a.DecodeFrames(a.EncodeFrames([][]byte{{}, []byte("x"), {}}))
	line(err == nil && len(empty) == 3 && len(empty[0]) == 0 && len(empty[2]) == 0, "empty frames legal")

	_, e1 := a.DecodeFrames([]byte("a\\x\n"))
	line(errors.Is(e1, esc.ErrIllegalEscape), "illegal escape rejected: "+fmt.Sprint(e1))
	_, e2 := a.DecodeFrames([]byte("ab\\\n"))
	line(errors.Is(e2, esc.ErrDanglingEscape), "dangling escape rejected: "+fmt.Sprint(e2))
	_, e3 := a.DecodeFrames([]byte("ab"))
	line(errors.Is(e3, frame.ErrMissingTerminator), "missing terminator rejected: "+fmt.Sprint(e3))

	r := frame.NewReader(append(append([]byte("ok\n"), []byte("a\\x\n")...), []byte("z\n")...))
	f, _ := r.NextFrame()
	p := r.Pos()
	_, cursorErr := r.NextFrame()
	_, againErr := r.NextFrame()
	line(bytes.Equal(f, []byte("ok")) && errors.Is(cursorErr, esc.ErrIllegalEscape) && r.Pos() == p && errors.Is(againErr, esc.ErrIllegalEscape),
		"cursor frozen after rejection, reader still usable")

	line(frame.SinglePassCheck() == nil, "single-pass decode, zero re-reads at large m")
	line(a.SelfCheck() == nil && concurrentOK(), "self-check and concurrent encode/decode")

	if !allOK {
		os.Exit(1)
	}
}
