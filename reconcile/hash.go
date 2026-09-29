package reconcile

import (
	"crypto/sha256"
	"encoding/binary"
)

// hashTree is the per-level hash digest of the recursive equal-partitioned
// key space. Level 0 holds the root digest; the last level holds one digest
// per key position. los/his record the [lo, hi) interval of every node.
type hashTree struct {
	fanout uint64
	depth  int
	levels [][][32]byte
	los    [][]uint64
	his    [][]uint64
}

var leafTag = []byte("leaf:v1")
var internalTag = []byte("node:v1")

func newHashTree(fanout, keyMax uint64) *hashTree {
	size := keyMax + 1
	depth := 1
	for width := uint64(1); width < size; {
		width *= fanout
		depth++
	}
	t := &hashTree{
		fanout: fanout,
		depth:  depth,
		levels: make([][][32]byte, depth),
		los:    make([][]uint64, depth),
		his:    make([][]uint64, depth),
	}

	width := powUint64(fanout, depth-1)
	t.levels[depth-1] = make([][32]byte, width)
	t.los[depth-1] = make([]uint64, width)
	t.his[depth-1] = make([]uint64, width)
	for i := range t.levels[depth-1] {
		key := uint64(i)
		if key < size {
			t.los[depth-1][i] = key
			t.his[depth-1][i] = key + 1
			t.levels[depth-1][i] = hashLeaf(false, key, 0)
		} else {
			t.los[depth-1][i] = size
			t.his[depth-1][i] = size
			t.levels[depth-1][i] = nilDigest
		}
	}

	for level := depth - 2; level >= 0; level-- {
		childWidth := width
		width = (width + fanout - 1) / fanout
		t.levels[level] = make([][32]byte, width)
		t.los[level] = make([]uint64, width)
		t.his[level] = make([]uint64, width)
		children := make([][32]byte, fanout)
		for i := range t.levels[level] {
			first := uint64(i) * fanout
			last := first + fanout
			if last > childWidth {
				last = childWidth
			}
			for j := uint64(0); j < fanout; j++ {
				if first+j < last {
					children[j] = t.levels[level+1][first+j]
				} else {
					children[j] = nilDigest
				}
			}
			t.los[level][i] = t.los[level+1][first]
			t.his[level][i] = t.his[level+1][last-1]
			t.levels[level][i] = hashInternal(t.los[level][i], t.his[level][i], children)
		}
	}
	return t
}

// nilDigest marks key positions beyond keyMax; it never participates in a
// real key space and stays constant across replicas.
var nilDigest [32]byte

// hashLeaf computes the digest of one key position. An absent key and a
// present key whose value is zero get distinct domain-separated digests.
func hashLeaf(present bool, key, value uint64) [32]byte {
	h := sha256.New()
	h.Write(leafTag)
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], key)
	h.Write(buf[:])
	if present {
		h.Write([]byte{1})
		binary.BigEndian.PutUint64(buf[:], value)
		h.Write(buf[:])
	} else {
		h.Write([]byte{0})
	}
	var out [32]byte
	h.Sum(out[:0])
	return out
}

// hashInternal combines exactly fanout child digests together with the
// node's [lo, hi) interval, so equal content at different positions still
// hashes differently.
func hashInternal(lo, hi uint64, children [][32]byte) [32]byte {
	h := sha256.New()
	h.Write(internalTag)
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], lo)
	h.Write(buf[:])
	binary.BigEndian.PutUint64(buf[:], hi)
	h.Write(buf[:])
	for i := range children {
		h.Write(children[i][:])
	}
	var out [32]byte
	h.Sum(out[:0])
	return out
}

// updateLeaf replaces a leaf digest and recomputes the path to the root.
func (t *hashTree) updateLeaf(key uint64, digest [32]byte) {
	idx := key
	t.levels[t.depth-1][idx] = digest
	children := make([][32]byte, t.fanout)
	for level := t.depth - 2; level >= 0; level-- {
		idx /= t.fanout
		first := idx * t.fanout
		childWidth := uint64(len(t.levels[level+1]))
		for j := uint64(0); j < t.fanout; j++ {
			pos := first + j
			if pos < childWidth {
				children[j] = t.levels[level+1][pos]
			} else {
				children[j] = nilDigest
			}
		}
		t.levels[level][idx] = hashInternal(t.los[level][idx], t.his[level][idx], children)
	}
}

func powUint64(base uint64, exp int) uint64 {
	result := uint64(1)
	for i := 0; i < exp; i++ {
		result *= base
	}
	return result
}

func cloneTree(src *hashTree) *hashTree {
	dst := &hashTree{
		fanout: src.fanout,
		depth:  src.depth,
		levels: make([][][32]byte, len(src.levels)),
		los:    make([][]uint64, len(src.los)),
		his:    make([][]uint64, len(src.his)),
	}
	for i := range src.levels {
		dst.levels[i] = append([]digest(nil), src.levels[i]...)
		dst.los[i] = append([]uint64(nil), src.los[i]...)
		dst.his[i] = append([]uint64(nil), src.his[i]...)
	}
	return dst
}

type digest = [32]byte
