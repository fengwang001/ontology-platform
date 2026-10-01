package ontology

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math/rand/v2"
	"sort"
	"testing"
)

type naiveFile struct {
	mode  Mode
	size  int64
	attrs map[string][]byte
}

type naiveWorld struct {
	a, x, bs, p int64
	nextID      int
	files       map[int]*naiveFile
}

type naivePlacement struct {
	inline        map[string]bool
	key           string
	externalBytes int64
	names         []string
}

type naiveStat struct {
	mode          Mode
	size          int64
	dataBlocks    int64
	extID         int
	externalBytes int64
	locations     []XattrLocation
}

type testOp struct {
	kind  string
	id    int
	size  int64
	name  string
	value []byte
}

type opOutput struct {
	id    int
	err   error
	value []byte
	stat  naiveStat
}

func TestRandomSequencesAgainstNaiveModel(t *testing.T) {
	for sequence := 0; sequence < 2000; sequence++ {
		t.Run(fmt.Sprintf("sequence_%04d", sequence), func(t *testing.T) {
			rng := rand.New(rand.NewPCG(uint64(sequence+1), uint64(sequence+1009)))
			a := int64(1 + rng.IntN(48))
			x := int64(1 + rng.IntN(160))
			bs := int64(1 + rng.IntN(16))
			p := int64(1 + rng.IntN(36))

			manager, err := NewManager(a, x, bs, p)
			if err != nil {
				t.Fatal(err)
			}
			model := &naiveWorld{a: a, x: x, bs: bs, p: p, nextID: 1, files: make(map[int]*naiveFile)}

			for step := 0; step < 20+rng.IntN(12); step++ {
				op := generateOp(rng, model)
				actual := applyActual(manager, op)
				expected := model.apply(op)
				t.Logf("input seq=%d step=%d params=(A=%d,X=%d,Bs=%d,P=%d) op=%+v; output actual=(id=%d,err=%v,value=%x,stat=%+v); expected=(id=%d,err=%v,value=%x,stat=%+v); basis=check argument/file/xattr first, then sorted inode placement until first miss and recompute shared nonempty Ext plus data blocks",
					sequence, step, a, x, bs, p, op,
					actual.id, actual.err, actual.value, actual.stat,
					expected.id, expected.err, expected.value, expected.stat)

				if !sameError(actual.err, expected.err) {
					t.Fatalf("error mismatch actual=%v expected=%v", actual.err, expected.err)
				}
				if expected.err == nil {
					if actual.id != expected.id || !bytes.Equal(actual.value, expected.value) {
						t.Fatalf("result mismatch actual=(%d,%x) expected=(%d,%x)", actual.id, actual.value, expected.id, expected.value)
					}
					if op.kind == "stat" {
						assertStatEqual(t, actual.stat, expected.stat)
					}
				}
				assertWorldsEqual(t, manager, model)
			}
		})
	}
}

func generateOp(rng *rand.Rand, world *naiveWorld) testOp {
	if len(world.files) == 0 || rng.IntN(100) < 8 {
		return testOp{kind: "create"}
	}
	id := chooseFileID(rng, world)
	file := world.files[id]
	switch rng.IntN(100) {
	case 0, 1, 2:
		return testOp{kind: "create"}
	case 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22:
		size := int64(rng.IntN(int(2*world.a + 8)))
		if rng.IntN(35) == 0 {
			size = 1 << 41
		}
		return testOp{kind: "resize", id: id, size: size}
	case 23, 24, 25, 26, 27, 28, 29, 30, 31, 32, 33, 34, 35, 36, 37, 38, 39, 40,
		41, 42, 43, 44, 45, 46, 47, 48, 49, 50, 51, 52:
		return testOp{kind: "set", id: id, name: randomName(rng, file), value: randomValue(rng)}
	case 53, 54, 55, 56, 57, 58, 59, 60, 61, 62, 63, 64, 65:
		return testOp{kind: "remove", id: id, name: randomName(rng, file)}
	case 66, 67, 68, 69, 70, 71, 72, 73, 74, 75, 76, 77, 78:
		return testOp{kind: "get", id: id, name: randomName(rng, file)}
	case 79, 80, 81, 82, 83, 84, 85, 86, 87, 88:
		return testOp{kind: "clone", id: id}
	default:
		return testOp{kind: "stat", id: id}
	}
}

func chooseFileID(rng *rand.Rand, world *naiveWorld) int {
	if rng.IntN(8) == 0 {
		return world.nextID
	}
	ids := make([]int, 0, len(world.files))
	for id := range world.files {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	return ids[rng.IntN(len(ids))]
}

func randomName(rng *rand.Rand, file *naiveFile) string {
	if file == nil {
		return "missing"
	}
	if len(file.attrs) > 0 && rng.IntN(3) == 0 {
		return sortedAttrNames(file.attrs)[rng.IntN(len(file.attrs))]
	}
	if rng.IntN(35) == 0 {
		return ""
	}
	return fmt.Sprintf("%c%c", 'a'+rng.IntN(8), 'a'+rng.IntN(8))
}

func randomValue(rng *rand.Rand) []byte {
	if rng.IntN(80) == 0 {
		return bytes.Repeat([]byte("z"), 4097)
	}
	return bytes.Repeat([]byte{byte('a' + rng.IntN(16))}, rng.IntN(80))
}

func applyActual(manager *Manager, op testOp) opOutput {
	result := opOutput{}
	switch op.kind {
	case "create":
		result.id, result.err = manager.Create()
	case "resize":
		result.err = manager.Resize(op.id, op.size)
	case "set":
		result.err = manager.SetXattr(op.id, op.name, op.value)
	case "remove":
		result.err = manager.RemoveXattr(op.id, op.name)
	case "get":
		result.value, result.err = manager.GetXattr(op.id, op.name)
	case "clone":
		result.id, result.err = manager.Clone(op.id)
	case "stat":
		info, err := manager.Stat(op.id)
		result.err = err
		if err == nil {
			result.stat = naiveStat{mode: info.Mode, size: info.Size, dataBlocks: info.DataBlocks, extID: info.ExtID, externalBytes: info.ExternalBytes, locations: info.Xattrs}
		}
	}
	return result
}
func (world *naiveWorld) apply(op testOp) opOutput {
	result := opOutput{}
	switch op.kind {
	case "create":
		world.files[world.nextID] = &naiveFile{mode: Inline, attrs: make(map[string][]byte)}
		result.id = world.nextID
		world.nextID++
	case "resize":
		if op.size < 0 || op.size > 1<<40 {
			result.err = ErrInvalidArgument
			break
		}
		file, ok := world.files[op.id]
		if !ok {
			result.err = ErrFileNotFound
			break
		}
		desired := file.mode
		if file.mode == Inline {
			if op.size <= world.a {
				if _, ok := naiveCompute(Inline, op.size, file.attrs, world.a, world.x); ok {
					desired = Inline
				} else {
					desired = Block
				}
			} else {
				desired = Block
			}
		} else if op.size == 0 {
			desired = Inline
		}
		if _, ok := naiveCompute(desired, op.size, file.attrs, world.a, world.x); !ok {
			result.err = ErrAttributeTooBig
			break
		}
		oldMode, oldSize := file.mode, file.size
		file.mode, file.size = desired, op.size
		if world.used() > world.p {
			file.mode, file.size = oldMode, oldSize
			result.err = ErrPoolFull
		}
	case "clone":
		file, ok := world.files[op.id]
		if !ok {
			result.err = ErrFileNotFound
			break
		}
		id := world.nextID
		world.files[id] = &naiveFile{mode: file.mode, size: file.size, attrs: naiveCloneAttrs(file.attrs)}
		if world.used() > world.p {
			delete(world.files, id)
			result.err = ErrPoolFull
			break
		}
		result.id = id
		world.nextID++
	case "set":
		if len(op.name) < 1 || len(op.name) > 255 || len(op.value) > 4096 {
			result.err = ErrInvalidArgument
			break
		}
		file, ok := world.files[op.id]
		if !ok {
			result.err = ErrFileNotFound
			break
		}
		candidate := naiveCloneAttrs(file.attrs)
		candidate[op.name] = append([]byte(nil), op.value...)
		desired := file.mode
		if file.mode == Inline {
			if _, ok := naiveCompute(Inline, file.size, candidate, world.a, world.x); ok {
				desired = Inline
			} else if _, ok = naiveCompute(Block, file.size, candidate, world.a, world.x); ok {
				desired = Block
			} else {
				result.err = ErrAttributeTooBig
				break
			}
		} else if _, ok = naiveCompute(Block, file.size, candidate, world.a, world.x); !ok {
			result.err = ErrAttributeTooBig
			break
		}
		oldMode, oldAttrs := file.mode, file.attrs
		file.mode, file.attrs = desired, candidate
		if world.used() > world.p {
			file.mode, file.attrs = oldMode, oldAttrs
			result.err = ErrPoolFull
		}
	case "remove":
		if len(op.name) < 1 || len(op.name) > 255 {
			result.err = ErrInvalidArgument
			break
		}
		file, ok := world.files[op.id]
		if !ok {
			result.err = ErrFileNotFound
			break
		}
		if _, ok := file.attrs[op.name]; !ok {
			result.err = ErrNoAttribute
			break
		}
		candidate := naiveCloneAttrs(file.attrs)
		delete(candidate, op.name)
		if _, ok := naiveCompute(file.mode, file.size, candidate, world.a, world.x); !ok {
			result.err = ErrAttributeTooBig
			break
		}
		oldAttrs := file.attrs
		file.attrs = candidate
		if world.used() > world.p {
			file.attrs = oldAttrs
			result.err = ErrPoolFull
		}
	case "get":
		if len(op.name) < 1 || len(op.name) > 255 {
			result.err = ErrInvalidArgument
			break
		}
		file, ok := world.files[op.id]
		if !ok {
			result.err = ErrFileNotFound
			break
		}
		value, ok := file.attrs[op.name]
		if !ok {
			result.err = ErrNoAttribute
			break
		}
		result.value = append([]byte(nil), value...)
	case "stat":
		file, ok := world.files[op.id]
		if !ok {
			result.err = ErrFileNotFound
			break
		}
		placement, _ := naiveCompute(file.mode, file.size, file.attrs, world.a, world.x)
		result.stat = world.statFor(file, op.id, placement)
	}
	return result
}
func (world *naiveWorld) used() int64 {
	external := make(map[string]struct{})
	var total int64
	for _, file := range world.files {
		if file.mode == Block {
			total += naiveDataBlocks(file.size, world.bs)
		}
		placement, _ := naiveCompute(file.mode, file.size, file.attrs, world.a, world.x)
		if placement.key != "" {
			external[placement.key] = struct{}{}
		}
	}
	return total + int64(len(external))
}
func (world *naiveWorld) statFor(file *naiveFile, id int, placement naivePlacement) naiveStat {
	stat := naiveStat{mode: file.mode, size: file.size, externalBytes: placement.externalBytes, locations: make([]XattrLocation, 0, len(placement.names))}
	if file.mode == Block {
		stat.dataBlocks = naiveDataBlocks(file.size, world.bs)
	} else {
		stat.dataBlocks = 0
	}
	if placement.key != "" {
		for fileID, other := range world.files {
			otherPlacement, _ := naiveCompute(other.mode, other.size, other.attrs, world.a, world.x)
			if otherPlacement.key == placement.key && (stat.extID == 0 || fileID < stat.extID) {
				stat.extID = fileID
			}
		}
	}
	for _, name := range placement.names {
		location := External
		if placement.inline[name] {
			location = InInode
		}
		stat.locations = append(stat.locations, XattrLocation{Name: name, Location: location})
	}
	return stat
}
func naiveCompute(mode Mode, size int64, attrs map[string][]byte, a, x int64) (naivePlacement, bool) {
	if size < 0 || (mode == Inline && size > a) {
		return naivePlacement{}, false
	}
	names := sortedAttrNames(attrs)
	result := naivePlacement{inline: make(map[string]bool), names: names}
	remaining := a
	if mode == Inline {
		remaining = a - size
	}
	var key bytes.Buffer
	usingExternal := false
	for _, name := range names {
		value := attrs[name]
		needed := int64(4 + naiveRoundUp4(len(name)) + naiveRoundUp4(len(value)))
		if !usingExternal && needed <= remaining {
			remaining -= needed
			result.inline[name] = true
			continue
		}
		usingExternal = true
		result.externalBytes += needed
		var header [4]byte
		binary.BigEndian.PutUint32(header[:], uint32(len(name)))
		key.Write(header[:])
		key.WriteString(name)
		binary.BigEndian.PutUint32(header[:], uint32(len(value)))
		key.Write(header[:])
		key.Write(value)
	}
	if result.externalBytes > x {
		return naivePlacement{}, false
	}
	if usingExternal {
		result.key = key.String()
	}
	return result, true
}

func naiveCloneAttrs(attrs map[string][]byte) map[string][]byte {
	result := make(map[string][]byte, len(attrs))
	for name, value := range attrs {
		result[name] = append([]byte(nil), value...)
	}
	return result
}

func naiveDataBlocks(size, blockSize int64) int64 {
	if size == 0 {
		return 0
	}
	return (size + blockSize - 1) / blockSize
}

func naiveRoundUp4(n int) int { return (n + 3) &^ 3 }

func sortedAttrNames(attrs map[string][]byte) []string {
	names := make([]string, 0, len(attrs))
	for name := range attrs {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
func sameError(actual, expected error) bool { return errors.Is(actual, expected) }
func assertWorldsEqual(t *testing.T, manager *Manager, world *naiveWorld) {
	t.Helper()
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	if manager.nextID != world.nextID || len(manager.files) != len(world.files) {
		t.Fatalf("file set mismatch manager=(next=%d,count=%d) model=(next=%d,count=%d)", manager.nextID, len(manager.files), world.nextID, len(world.files))
	}
	if manager.used != world.used() || manager.used > manager.p {
		t.Fatalf("used mismatch manager=%d model=%d pool=%d", manager.used, world.used(), manager.p)
	}
	for id, actual := range manager.files {
		expected, ok := world.files[id]
		if !ok {
			t.Fatalf("manager has extra file %d", id)
		}
		if actual.mode != expected.mode || actual.size != expected.size || len(actual.attrs) != len(expected.attrs) {
			t.Fatalf("file %d mismatch manager=%+v model=%+v", id, actual, expected)
		}
		actualPlacement, actualOK := manager.computeLayout(actual.mode, actual.size, actual.attrs)
		expectedPlacement, expectedOK := naiveCompute(expected.mode, expected.size, expected.attrs, world.a, world.x)
		if actualOK != expectedOK || actualPlacement.externalBytes != expectedPlacement.externalBytes || actualPlacement.externalKey != expectedPlacement.key {
			t.Fatalf("file %d placement mismatch", id)
		}
		for name, value := range actual.attrs {
			if !bytes.Equal(value, expected.attrs[name]) {
				t.Fatalf("file %d xattr %s mismatch", id, name)
			}
		}
	}
}

func assertStatEqual(t *testing.T, actual naiveStat, expected naiveStat) {
	t.Helper()
	if actual.mode != expected.mode || actual.size != expected.size || actual.dataBlocks != expected.dataBlocks || actual.extID != expected.extID || actual.externalBytes != expected.externalBytes {
		t.Fatalf("stat mismatch actual=%+v expected=%+v", actual, expected)
	}
	if len(actual.locations) != len(expected.locations) {
		t.Fatalf("stat locations mismatch actual=%+v expected=%+v", actual.locations, expected.locations)
	}
	for i := range actual.locations {
		if actual.locations[i] != expected.locations[i] {
			t.Fatalf("stat location mismatch actual=%+v expected=%+v", actual.locations, expected.locations)
		}
	}
}

var _ = bytes.Repeat
var _ = binary.BigEndian
var _ = errors.Is
var _ = fmt.Sprintf
var _ = sort.Strings
