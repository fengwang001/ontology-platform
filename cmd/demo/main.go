package main

import (
	"errors"
	"fmt"

	"ontology/vec"
	"ontology/hyper"
	"ontology/bucket"
)

type verdict struct {
	name string
	ok   bool
	detail string
}

func (v verdict) line() string {
	tag := "OK"
	if !v.ok {
		tag = "FAIL"
	}
	return fmt.Sprintf("%s %s %s", tag, v.name, v.detail)
}

func main() {
	verdicts := []verdict{
		{name: "vec-dim-error", ok: func() bool {
			_, err := vec.Dot(vec.Vec{1, 2}, vec.Vec{1})
			return errors.Is(err, vec.ErrDimMismatch)
		}()},
		{name: "same-seed-signatures-identical", ok: func() bool {
			f1 := hyper.NewFamily(8, 8, 4, 123)
			f2 := hyper.NewFamily(8, 8, 4, 123)
			x := vec.Vec{0.1, -1, 2, .5, -.5, 3, 0, 2}
			for t := 0; t < 4; t++ {
				s1, _ := f1.Signature(t, x)
				s2, _ := f2.Signature(t, x)
				if s1 != s2 {
					return false
				}
			}
			return true
		}()},
		{name: "bucket-union-dedup", ok: func() bool {
			mt := bucket.NewMultiTable(2)
			mt.Add([]bucket.Signature{1, 2}, bucket.ID(0))
			mt.Add([]bucket.Signature{1, 9}, bucket.ID(1))
			got := mt.Candidates([]bucket.Signature{1, 9})
			return len(got) == 2
		}()},
	}
	pass := 0
	for _, v := range verdicts {
		fmt.Println(v.line())
		if v.ok {
			pass++
		}
	}
	fmt.Printf("TOTAL %d/%d\n", pass, len(verdicts))
	if pass != len(verdicts) {
		panic("demo checks failed")
	}
}
