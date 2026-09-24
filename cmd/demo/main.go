// Command demo runs the spillable hash aggregator acceptance checks.
package main

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"

	"ontology/acc"
	"ontology/hashpart"
	"ontology/row"
)

func main() {
	var pass, fail int
	check := func(name string, ok bool) {
		if ok {
			pass++
			fmt.Println("OK   " + name)
		} else {
			fail++
			fmt.Println("FAIL " + name)
		}
	}

	rs := row.Row{Key: "g1", Value: math.Inf(1)}
	rd, rerr := row.Decode(row.Encode(rs))
	check("row codec roundtrip (+Inf bits, empty key later)", rerr == nil && rd.Key == rs.Key && math.Float64bits(rd.Value) == math.Float64bits(rs.Value))
	_, rshort := row.Decode([]byte{1, 2})
	check("row codec rejects truncated buffer", errors.Is(rshort, row.ErrShort))
	accOK := func() bool {
		var s1, s2 acc.State
		s1.Add(3)
		s1.Add(-2)
		s2.Add(7)
		m12 := acc.Merge(s1, s2)
		var all acc.State
		for _, v := range []float64{3, -2, 7} {
			all.Add(v)
		}
		if m12 != all {
			return false
		}
		if m := acc.Merge(acc.State{}, s1); m != s1 {
			return false
		}
		var z acc.State
		z.Add(math.Copysign(0, -1))
		return math.Float64bits(z.Min) == math.Float64bits(0.0)
	}()
	check("acc merge equals fold, empty-state identity, +-0 equal", accOK)
	hpOK := func() bool {
		dir, derr := os.MkdirTemp("", "demo-hp-")
		if derr != nil {
			return false
		}
		defer os.RemoveAll(dir)
		sp, serr := hashpart.NewSpiller(dir)
		if serr != nil {
			return false
		}
		bodies := [][]byte{acc.Encode("k", acc.State{Count: 1, Sum: 1, Min: 1, Max: 1})}
		name, werr := sp.Write(9, bodies)
		if werr != nil {
			return false
		}
		got, rerr := hashpart.ReadPartition(dir, 9)
		if rerr != nil || len(got) != 1 || !bytes.Equal(got[0], bodies[0]) {
			return false
		}
		raw, _ := os.ReadFile(name)
		cases := []struct {
			cut  int
			want error
		}{
			{1, hashpart.ErrHeader}, {17, hashpart.ErrLength},
			{22, hashpart.ErrBody}, {len(raw) - 2, hashpart.ErrCRC},
		}
		for _, c := range cases {
			tf := filepath.Join(dir, "p009-000000.part")
			if err := os.WriteFile(tf, raw[:c.cut], 0o600); err != nil {
				return false
			}
			_, err := hashpart.ReadFile(tf, 9)
			if !errors.Is(err, c.want) {
				return false
			}
			os.Remove(tf)
		}
		return true
	}()
	check("hashpart roundtrip + 4 truncation classes", hpOK)
	fmt.Printf("TOTAL pass=%d fail=%d\n", pass, fail)
}
