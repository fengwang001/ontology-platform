package ontology

import (
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

// naiveModel is a from-scratch reference implementation that stores the FAT
// as a byte image and performs every operation directly on packed 12-bit
// entries, mirroring the specification rules step by step.
type naiveModel struct {
	c     int
	img   []byte
	files map[uint16]struct{}
	rover uint16
}

func newNaiveModel(c int) *naiveModel {
	m := &naiveModel{
		c:     c,
		img:   make([]byte, ((c+2)*3+1)/2),
		files: map[uint16]struct{}{},
		rover: 2,
	}
	m.set(0, 0xFF8)
	m.set(1, eocMarker)
	return m
}

func (m *naiveModel) get(n int) uint16 {
	o := n + n/2
	if n%2 == 0 {
		return uint16(m.img[o]) | uint16(m.img[o+1]&0x0F)<<8
	}
	return uint16(m.img[o]>>4) | uint16(m.img[o+1])<<4
}

// set writes one 12-bit entry without disturbing any bits of its neighbour.
func (m *naiveModel) set(n int, v uint16) {
	o := n + n/2
	if n%2 == 0 {
		m.img[o] = byte(v & 0xFF)
		m.img[o+1] = (m.img[o+1] & 0xF0) | byte((v>>8)&0x0F)
	} else {
		m.img[o] = (m.img[o] & 0x0F) | byte((v&0x0F)<<4)
		m.img[o+1] = byte(v >> 4)
	}
}

func (m *naiveModel) max() int { return m.c + 1 }

func (m *naiveModel) freeCount() int {
	n := 0
	for c := 2; c <= m.max(); c++ {
		if m.get(c) == 0 {
			n++
		}
	}
	return n
}

func (m *naiveModel) chain(h uint16) []uint16 {
	out := []uint16{h}
	cur := int(h)
	for {
		v := m.get(cur)
		if v >= 0xFF8 {
			return out
		}
		cur = int(v)
		out = append(out, uint16(cur))
	}
}

func (m *naiveModel) allocate(n int) []uint16 {
	start, run := -1, 0
	for c := int(m.rover); c <= m.max(); c++ {
		if m.get(c) == 0 {
			if run == 0 {
				start = c
			}
			run++
			if run == n {
				break
			}
		} else {
			start, run = -1, 0
		}
	}
	if run == n {
		got := make([]uint16, n)
		for i := range got {
			got[i] = uint16(start + i)
		}
		return got
	}

	got := make([]uint16, 0, n)
	for i := 0; i < m.c; i++ {
		c := 2 + (int(m.rover)-2+i)%m.c
		if m.get(c) == 0 {
			got = append(got, uint16(c))
			if len(got) == n {
				break
			}
		}
	}
	return got
}

func (m *naiveModel) writeChain(chain []uint16) {
	for i := 0; i+1 < len(chain); i++ {
		m.set(int(chain[i]), chain[i+1])
	}
	m.set(int(chain[len(chain)-1]), eocMarker)
}

func (m *naiveModel) advance(last uint16) {
	n := int(last) + 1
	if n > m.max() {
		n = 2
	}
	m.rover = uint16(n)
}

func (m *naiveModel) rewind(released []uint16) {
	if len(released) == 0 {
		return
	}
	min := released[0]
	for _, c := range released[1:] {
		if c < min {
			min = c
		}
	}
	if min < m.rover {
		m.rover = min
	}
}

func (m *naiveModel) create(n int) (uint16, error) {
	if n < 1 {
		return 0, ErrInvalidArgument
	}
	if m.freeCount() < n {
		return 0, ErrNoSpace
	}
	got := m.allocate(n)
	m.writeChain(got)
	m.files[got[0]] = struct{}{}
	m.advance(got[len(got)-1])
	return got[0], nil
}

func (m *naiveModel) extend(h uint16, n int) error {
	if n < 1 {
		return ErrInvalidArgument
	}
	if _, ok := m.files[h]; !ok {
		return ErrFileNotFound
	}
	if m.freeCount() < n {
		return ErrNoSpace
	}
	old := m.chain(h)
	got := m.allocate(n)
	m.set(int(old[len(old)-1]), got[0])
	m.writeChain(got)
	m.advance(got[len(got)-1])
	return nil
}

func (m *naiveModel) truncate(h uint16, k int) error {
	if k < 1 {
		return ErrInvalidArgument
	}
	if _, ok := m.files[h]; !ok {
		return ErrFileNotFound
	}
	old := m.chain(h)
	if k > len(old) {
		return ErrOutOfRange
	}
	if k == len(old) {
		return nil
	}
	for _, c := range old[k:] {
		m.set(int(c), 0)
	}
	m.set(int(old[k-1]), eocMarker)
	m.rewind(old[k:])
	return nil
}

func (m *naiveModel) del(h uint16) error {
	if _, ok := m.files[h]; !ok {
		return ErrFileNotFound
	}
	old := m.chain(h)
	for _, c := range old {
		m.set(int(c), 0)
	}
	delete(m.files, h)
	m.rewind(old)
	return nil
}

func (m *naiveModel) markBad(c uint16) error {
	if c < 2 || int(c) > m.max() {
		return ErrInvalidArgument
	}
	if m.get(int(c)) != 0 {
		return ErrClusterNotFree
	}
	m.set(int(c), badMarker)
	return nil
}

func (m *naiveModel) defrag(h uint16) (uint16, error) {
	if _, ok := m.files[h]; !ok {
		return 0, ErrFileNotFound
	}
	old := m.chain(h)
	oldSet := map[uint16]struct{}{}
	for _, c := range old {
		oldSet[c] = struct{}{}
	}
	nw := make([]uint16, 0, len(old))
	for c := 2; c <= m.max() && len(nw) < len(old); c++ {
		v := m.get(c)
		cc := uint16(c)
		if v == 0 {
			nw = append(nw, cc)
		} else if _, in := oldSet[cc]; in {
			nw = append(nw, cc)
		}
	}
	same := true
	for i := range old {
		if old[i] != nw[i] {
			same = false
			break
		}
	}
	if same {
		return h, nil
	}
	newSet := map[uint16]struct{}{}
	for _, c := range nw {
		newSet[c] = struct{}{}
	}
	var released []uint16
	for _, c := range old {
		if _, keep := newSet[c]; !keep {
			released = append(released, c)
		}
	}
	for _, c := range released {
		m.set(int(c), 0)
	}
	m.writeChain(nw)
	m.rewind(released)
	delete(m.files, h)
	m.files[nw[0]] = struct{}{}
	return nw[0], nil
}

type opKind int

const (
	opCreate opKind = iota
	opExtend
	opTruncate
	opDelete
	opMarkBad
	opDefrag
	opImage
	opChain
	opFree
	opRover
)

type modelOp struct {
	kind opKind
	n    int
	c    uint16
	h    uint16
}

func (o modelOp) String() string {
	switch o.kind {
	case opCreate:
		return fmt.Sprintf("Create(%d)", o.n)
	case opExtend:
		return fmt.Sprintf("Extend(h=%d,n=%d)", o.h, o.n)
	case opTruncate:
		return fmt.Sprintf("Truncate(h=%d,k=%d)", o.h, o.n)
	case opDelete:
		return fmt.Sprintf("Delete(h=%d)", o.h)
	case opMarkBad:
		return fmt.Sprintf("MarkBad(%d)", o.c)
	case opDefrag:
		return fmt.Sprintf("Defrag(h=%d)", o.h)
	case opImage:
		return "Image()"
	case opChain:
		return fmt.Sprintf("Chain(h=%d)", o.h)
	case opFree:
		return "Free()"
	default:
		return "Rover()"
	}
}

func sameErr(a, b error) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return errors.Is(a, b) && errors.Is(b, a)
}

// stateSnapshot is compared after every single operation so the real manager
// and the naive byte-image model stay byte-for-byte identical.
type stateSnapshot struct {
	img    string
	rover  uint16
	free   int
	files  []uint16
	chains map[uint16]string
}

func takeSnapshot(f *FAT12) stateSnapshot {
	f.mu.RLock()
	defer f.mu.RUnlock()
	s := stateSnapshot{
		img:    string(encodeImage(f.fat)),
		rover:  f.rover,
		free:   f.freeCount(),
		chains: map[uint16]string{},
	}
	for h := range f.files {
		s.files = append(s.files, h)
		ch, _ := f.chainOf(h)
		s.chains[h] = fmt.Sprint(ch)
	}
	sortU16(s.files)
	return s
}

func takeNaiveSnapshot(m *naiveModel) stateSnapshot {
	s := stateSnapshot{
		img:    string(m.img),
		rover:  m.rover,
		free:   m.freeCount(),
		chains: map[uint16]string{},
	}
	for h := range m.files {
		s.files = append(s.files, h)
		s.chains[h] = fmt.Sprint(m.chain(h))
	}
	sortU16(s.files)
	return s
}

func sortU16(a []uint16) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j-1] > a[j]; j-- {
			a[j-1], a[j] = a[j], a[j-1]
		}
	}
}

func snapshotsEqual(a, b stateSnapshot) (string, bool) {
	if a.img != b.img {
		return "byte image differs", false
	}
	if a.rover != b.rover {
		return fmt.Sprintf("rover %d != %d", a.rover, b.rover), false
	}
	if a.free != b.free {
		return fmt.Sprintf("free %d != %d", a.free, b.free), false
	}
	if fmt.Sprint(a.files) != fmt.Sprint(b.files) {
		return fmt.Sprintf("file handles %v != %v", a.files, b.files), false
	}
	for _, h := range a.files {
		if a.chains[h] != b.chains[h] {
			return fmt.Sprintf("chain of %d: %s != %s", h, a.chains[h], b.chains[h]), false
		}
	}
	return "image/rover/free/files/chains all equal", true
}

func TestRandomAgainstNaiveModel(t *testing.T) {
	const trials = 2000
	rng := rand.New(rand.NewSource(20261001))

	for trial := 0; trial < trials; trial++ {
		c := 1 + rng.Intn(64)
		if trial == 0 {
			c = 1
		} else if trial == 1 {
			c = 4078
		}
		f, err := New(c)
		if err != nil {
			t.Fatalf("trial %d New(%d): %v", trial, c, err)
		}
		m := newNaiveModel(c)

		steps := 25 + rng.Intn(40)
		var log strings.Builder
		fmt.Fprintf(&log, "trial=%d C=%d steps=%d\n", trial, c, steps)

		var live []uint16
		for step := 0; step < steps; step++ {
			var o modelOp
			r := rng.Intn(100)
			switch {
			case r < 20:
				o = modelOp{kind: opCreate, n: 1 + rng.Intn(c+2)}
			case r < 35:
				o = modelOp{kind: opExtend, n: 1 + rng.Intn(c+2)}
				if len(live) > 0 {
					o.h = live[rng.Intn(len(live))]
				} else {
					o.h = uint16(2 + rng.Intn(c+1))
				}
			case r < 47:
				o = modelOp{kind: opTruncate, n: 1 + rng.Intn(c+2)}
				if len(live) > 0 {
					o.h = live[rng.Intn(len(live))]
				} else {
					o.h = uint16(2 + rng.Intn(c+1))
				}
			case r < 57:
				if len(live) > 0 {
					o = modelOp{kind: opDelete, h: live[rng.Intn(len(live))]}
				} else {
					o = modelOp{kind: opDelete, h: uint16(2 + rng.Intn(c+1))}
				}
			case r < 67:
				if rng.Intn(8) == 0 {
					o = modelOp{kind: opMarkBad, c: uint16(rng.Intn(c + 3))}
				} else {
					o = modelOp{kind: opMarkBad, c: uint16(2 + rng.Intn(c))}
				}
			case r < 78:
				if len(live) > 0 {
					o = modelOp{kind: opDefrag, h: live[rng.Intn(len(live))]}
				} else {
					o = modelOp{kind: opDefrag, h: uint16(2 + rng.Intn(c+1))}
				}
			case r < 84:
				o = modelOp{kind: opImage}
			case r < 90:
				if len(live) > 0 {
					o = modelOp{kind: opChain, h: live[rng.Intn(len(live))]}
				} else {
					o = modelOp{kind: opChain, h: uint16(2 + rng.Intn(c+1))}
				}
			case r < 95:
				o = modelOp{kind: opFree}
			default:
				o = modelOp{kind: opRover}
			}

			// Deliberately inject invalid sizes after generation so the
			// rejection-order path is covered as well.
			if rng.Intn(12) == 0 {
				switch o.kind {
				case opCreate, opExtend:
					o.n = -rng.Intn(3)
				case opTruncate:
					o.n = -rng.Intn(3)
				}
			}

			var realRet, modelRet string
			var realErr, modelErr error
			switch o.kind {
			case opCreate:
				var rv, mv uint16
				rv, realErr = f.Create(o.n)
				mv, modelErr = m.create(o.n)
				if realErr == nil {
					realRet = fmt.Sprintf("handle=%d", rv)
					live = append(live, rv)
				}
				if modelErr == nil {
					modelRet = fmt.Sprintf("handle=%d", mv)
				}
			case opExtend:
				realErr = f.Extend(o.h, o.n)
				modelErr = m.extend(o.h, o.n)
			case opTruncate:
				realErr = f.Truncate(o.h, o.n)
				modelErr = m.truncate(o.h, o.n)
			case opDelete:
				realErr = f.Delete(o.h)
				modelErr = m.del(o.h)
				if realErr == nil {
					out := live[:0]
					for _, h := range live {
						if h != o.h {
							out = append(out, h)
						}
					}
					live = out
				}
			case opMarkBad:
				realErr = f.MarkBad(o.c)
				modelErr = m.markBad(o.c)
			case opDefrag:
				var rv, mv uint16
				rv, realErr = f.Defrag(o.h)
				mv, modelErr = m.defrag(o.h)
				if realErr == nil {
					realRet = fmt.Sprintf("handle=%d", rv)
					for i, h := range live {
						if h == o.h {
							live[i] = rv
						}
					}
				}
				if modelErr == nil {
					modelRet = fmt.Sprintf("handle=%d", mv)
				}
			case opImage:
				realRet = fmt.Sprintf("len=%d", len(f.Image()))
				modelRet = fmt.Sprintf("len=%d", len(m.img))
			case opChain:
				var ch []uint16
				ch, realErr = f.Chain(o.h)
				realRet = fmt.Sprint(ch)
				if _, ok := m.files[o.h]; ok {
					modelRet = fmt.Sprint(m.chain(o.h))
				} else {
					modelErr = ErrFileNotFound
				}
			case opFree:
				realRet = fmt.Sprintf("%d", f.Free())
				modelRet = fmt.Sprintf("%d", m.freeCount())
			case opRover:
				realRet = fmt.Sprintf("%d", f.Rover())
				modelRet = fmt.Sprintf("%d", m.rover)
			}

			fmt.Fprintf(&log, "  step=%d input=%s output=(%v, %v) vs (%v, %v)",
				step, o, realRet, errName(realErr), modelRet, errName(modelErr))

			if !sameErr(realErr, modelErr) {
				t.Fatalf("trial %d step %d %s: error %v != %v\n%s",
					trial, step, o, realErr, modelErr, log.String())
			}
			if realErr == nil && realRet != modelRet {
				t.Fatalf("trial %d step %d %s: return %q != %q\n%s",
					trial, step, o, realRet, modelRet, log.String())
			}

			rs := takeSnapshot(f)
			ms := takeNaiveSnapshot(m)
			if why, ok := snapshotsEqual(rs, ms); !ok {
				t.Fatalf("trial %d step %d %s: %s\n%s",
					trial, step, o, why, log.String())
			} else {
				fmt.Fprintf(&log, " => %s\n", why)
			}
		}

		// The trial passed; print the input/output/verdict trace in verbose
		// mode so the requested logging is locally observable.
		t.Logf("%s  verdict=PASS", log.String())
	}
}

func errName(err error) string {
	if err == nil {
		return "nil"
	}
	return err.Error()
}

func TestReplayDeterminism(t *testing.T) {
	const c = 40
	rng := rand.New(rand.NewSource(777))

	play := func(f *FAT12) []string {
		var live []uint16
		out := make([]string, 0, 220)
		for step := 0; step < 220; step++ {
			var line string
			switch rng.Intn(7) {
			case 0:
				h, err := f.Create(1 + rng.Intn(6))
				line = fmt.Sprintf("create %d %v", h, errName(err))
				if err == nil {
					live = append(live, h)
				}
			case 1:
				if len(live) == 0 {
					continue
				}
				h := live[rng.Intn(len(live))]
				err := f.Extend(h, 1+rng.Intn(5))
				line = fmt.Sprintf("extend %d %v", h, errName(err))
			case 2:
				if len(live) == 0 {
					continue
				}
				h := live[rng.Intn(len(live))]
				err := f.Truncate(h, 1+rng.Intn(7))
				line = fmt.Sprintf("truncate %d %v", h, errName(err))
			case 3:
				cc := uint16(2 + rng.Intn(c))
				err := f.MarkBad(cc)
				line = fmt.Sprintf("markbad %d %v", cc, errName(err))
			case 4:
				if len(live) == 0 {
					continue
				}
				idx := rng.Intn(len(live))
				h := live[idx]
				nh, err := f.Defrag(h)
				line = fmt.Sprintf("defrag %d->%d %v", h, nh, errName(err))
				if err == nil {
					live[idx] = nh
				}
			case 5:
				if len(live) == 0 {
					continue
				}
				idx := rng.Intn(len(live))
				h := live[idx]
				err := f.Delete(h)
				line = fmt.Sprintf("delete %d %v", h, errName(err))
				if err == nil {
					live = append(live[:idx], live[idx+1:]...)
				}
			default:
				ch, err := f.Chain(uint16(2 + rng.Intn(c+2)))
				line = fmt.Sprintf("chain %v %v", ch, errName(err))
			}
			out = append(out, fmt.Sprintf("%s | img=%x rover=%d free=%d",
				line, f.Image(), f.Rover(), f.Free()))
		}
		return out
	}

	a, _ := New(c)
	b, _ := New(c)
	r1 := play(a)
	rng.Seed(777)
	r2 := play(b)
	for i := range r1 {
		if r1[i] != r2[i] {
			t.Fatalf("replay diverged at step %d:\n%s\n%s", i, r1[i], r2[i])
		}
	}
	if string(a.Image()) != string(b.Image()) || a.Rover() != b.Rover() {
		t.Fatal("final replayed state differs")
	}
}
