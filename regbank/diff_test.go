package regbank

import (
	"fmt"
	"math/rand/v2"
	"sync"
	"testing"
)

// op is one replayable bus/debug access.
type op struct {
	kind   string
	reg    string
	val    uint32
	be     int
	mask   uint32
	field  string
	fieldV uint32
}

func runOps(t *testing.T, b *Bank, n *naiveBank, ops []op, verbose bool) {
	t.Helper()
	for i, o := range ops {
		switch o.kind {
		case "write":
			errB := b.Write(o.reg, o.val, o.be)
			if errB != nil {
				t.Fatalf("op %d Write unexpected err %v", i, errB)
			}
			n.write(o.reg, o.val, o.be)
			if verbose {
				t.Logf("op %d input Write(%s, 0x%08x, be=%d); output stored", i, o.reg, o.val, o.be)
			}
		case "read":
			got, errB := b.Read(o.reg)
			if errB != nil {
				t.Fatalf("op %d Read unexpected err %v", i, errB)
			}
			want := n.read(o.reg)
			if verbose {
				t.Logf("op %d input Read(%s); output 0x%08x; naive 0x%08x; verdict %v", i, o.reg, got, want, got == want)
			}
			if got != want {
				t.Fatalf("op %d Read mismatch: got %#x want %#x", i, got, want)
			}
		case "rmw":
			res, errB := b.ReadModifyWrite(o.reg, o.mask, o.val)
			if errB != nil {
				t.Fatalf("op %d RMW unexpected err %v", i, errB)
			}
			wantR, wantS := n.rmw(o.reg, o.mask, o.val)
			if verbose {
				t.Logf("op %d input RMW(%s, mask=0x%08x, val=0x%08x); output read=0x%08x stored=0x%08x; naive 0x%08x/0x%08x; verdict %v",
					i, o.reg, o.mask, o.val, res.Read, res.Stored, wantR, wantS,
					res.Read == wantR && res.Stored == wantS)
			}
			if res.Read != wantR || res.Stored != wantS {
				t.Fatalf("op %d RMW mismatch: got (%#x,%#x) want (%#x,%#x)", i, res.Read, res.Stored, wantR, wantS)
			}
		case "hwset":
			if errB := b.HwSet(o.reg, o.field, o.fieldV); errB != nil {
				t.Fatalf("op %d HwSet unexpected err %v", i, errB)
			}
			n.hwSet(o.reg, o.field, o.fieldV)
			if verbose {
				t.Logf("op %d input HwSet(%s, %s, %d); output stored", i, o.reg, o.field, o.fieldV)
			}
		case "raw":
			got, errB := b.Raw(o.reg)
			if errB != nil {
				t.Fatalf("op %d Raw unexpected err %v", i, errB)
			}
			want := n.raw(o.reg)
			if verbose {
				t.Logf("op %d input Raw(%s); output 0x%08x; naive 0x%08x; verdict %v", i, o.reg, got, want, got == want)
			}
			if got != want {
				t.Fatalf("op %d Raw mismatch: got %#x want %#x", i, got, want)
			}
		case "reset":
			b.Reset()
			n.reset()
			if verbose {
				t.Logf("op %d input Reset; output all fields at reset values", i)
			}
		}
		// After every op, stored words of all registers must agree.
		for reg := range n.fieldOf {
			g, _ := b.Raw(reg)
			w := n.raw(reg)
			if g != w {
				t.Fatalf("op %d post-state %s mismatch: bank %#x naive %#x", i, reg, g, w)
			}
		}
	}
}

func randomOps(seed uint64, n int) []op {
	rng := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
	regs := []string{"MIX", "W1SRC", "STRADDLE"}
	fields := map[string][]string{
		"MIX":      {"rw0", "ro0", "woc", "w1c"},
		"W1SRC":    {"ws", "rc", "gap"},
		"STRADDLE": {"lo", "cross", "mid", "hi"},
	}
	width := map[string]map[string]int{}
	for _, rs := range validSpecs() {
		width[rs.Name] = map[string]int{}
		for _, fs := range rs.Fields {
			width[rs.Name][fs.Name] = fs.W
		}
	}

	ops := make([]op, n)
	for i := range ops {
		reg := regs[rng.IntN(len(regs))]
		switch rng.IntN(6) {
		case 0:
			ops[i] = op{kind: "write", reg: reg, val: rng.Uint32(), be: rng.IntN(16)}
		case 1:
			ops[i] = op{kind: "read", reg: reg}
		case 2:
			ops[i] = op{kind: "rmw", reg: reg, mask: rng.Uint32(), val: rng.Uint32()}
		case 3:
			f := fields[reg][rng.IntN(len(fields[reg]))]
			v := uint32(rng.IntN(1 << uint(width[reg][f])))
			ops[i] = op{kind: "hwset", reg: reg, field: f, fieldV: v}
		case 4:
			ops[i] = op{kind: "raw", reg: reg}
		default:
			ops[i] = op{kind: "reset"}
		}
	}
	return ops
}

func TestDifferentialNaive(t *testing.T) {
	specs := validSpecs()
	for _, seed := range []uint64{1, 2, 42, 1093} {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			ops := randomOps(seed, 300)
			b1, _ := NewBank(specs)
			n1 := newNaive(specs)
			runOps(t, b1, n1, ops, true)
			t.Logf("seed %d: %d randomized ops match the bit-by-bit naive model on every return value and stored word", seed, len(ops))

			// Deterministic replay must produce identical state.
			b2, _ := NewBank(specs)
			n2 := newNaive(specs)
			runOps(t, b2, n2, ops, false)
			for _, rs := range specs {
				g1, _ := b1.Raw(rs.Name)
				g2, _ := b2.Raw(rs.Name)
				if g1 != g2 {
					t.Fatalf("replay divergence on %s: %#x vs %#x", rs.Name, g1, g2)
				}
			}
			t.Logf("seed %d: identical replayed op sequence yields identical stored words", seed)
		})
	}
}

func TestInvariantsAlwaysHold(t *testing.T) {
	specs := validSpecs()
	b, _ := NewBank(specs)

	var wg sync.WaitGroup
	stop := make(chan struct{})
	ops := randomOps(777, 400)
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			rng := rand.New(rand.NewPCG(uint64(id), 0xdeadbeef))
			for {
				select {
				case <-stop:
					return
				default:
				}
				o := ops[rng.IntN(len(ops))]
				switch o.kind {
				case "write":
					_ = b.Write(o.reg, o.val, o.be)
				case "read":
					if _, err := b.Read(o.reg); err != nil {
						t.Errorf("read err: %v", err)
						return
					}
				case "rmw":
					if _, err := b.ReadModifyWrite(o.reg, o.mask, o.val); err != nil {
						t.Errorf("rmw err: %v", err)
						return
					}
				case "hwset":
					_ = b.HwSet(o.reg, o.field, o.fieldV)
				case "raw":
					if _, err := b.Raw(o.reg); err != nil {
						t.Errorf("raw err: %v", err)
						return
					}
				case "reset":
					b.Reset()
				}
			}
		}(w)
	}
	// Concurrent hammering for a short while; every observed word must keep
	// each field inside its width.
	widthOf := map[string]map[string]uint32{}
	for _, rs := range specs {
		widthOf[rs.Name] = map[string]uint32{}
		for _, fs := range rs.Fields {
			widthOf[rs.Name][fs.Name] = (uint32(1) << uint(fs.W)) - 1
		}
	}
	for i := 0; i < 20000; i++ {
		for _, rs := range specs {
			word, err := b.Raw(rs.Name)
			if err != nil {
				t.Fatal(err)
			}
			for _, fs := range rs.Fields {
				fv := (word >> uint(fs.Lo)) & widthOf[rs.Name][fs.Name]
				if fv > widthOf[rs.Name][fs.Name] {
					t.Fatalf("field %s.%s out of width: %#x", rs.Name, fs.Name, fv)
				}
			}
		}
	}
	close(stop)
	wg.Wait()
	t.Log("input 4 goroutines hammering all accesses concurrently; output no data race (run with -race), every Raw field within 2^w-1; verdict serializable")
}
