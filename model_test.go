package blockstore

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

type modelBlock struct {
	raw      []byte
	form     StorageForm
	stored   []byte
	reason   FallbackReason
	physical int
}

type modelCacheEntry struct {
	index int
	data  []byte
	newer *modelCacheEntry
	older *modelCacheEntry
}

type naiveModel struct {
	blockSize    int
	minGain      int
	codec        shortCodec
	blocks       []modelBlock
	starts       []int64
	cacheCap     int
	cacheItems   map[int]*modelCacheEntry
	cacheOldest  *modelCacheEntry
	cacheNewest  *modelCacheEntry
	compressions int64
	decompresses int64
	hits         int64
}

func newNaiveModel(blockSize, minGain, cacheCap int) *naiveModel {
	return &naiveModel{
		blockSize:  blockSize,
		minGain:    minGain,
		cacheCap:   cacheCap,
		cacheItems: make(map[int]*modelCacheEntry),
	}
}

func (m *naiveModel) append(data []byte) []FallbackReason {
	reasons := make([]FallbackReason, 0)
	if len(data) == 0 {
		return reasons
	}

	position := 0
	if len(m.blocks) > 0 && len(m.blocks[len(m.blocks)-1].raw) < m.blockSize {
		tail := len(m.blocks) - 1
		old := m.blocks[tail]
		var oldRaw []byte
		if entry, cached := m.cacheItems[tail]; old.form == StoredCompressed && cached {
			oldRaw = append([]byte(nil), entry.data...)
		} else {
			oldRaw = append([]byte(nil), old.raw...)
			if old.form == StoredCompressed {
				m.decompresses++
				raw, ok := m.codec.Decompress(old.stored)
				if !ok {
					panic("model tail decompression failed")
				}
				oldRaw = raw
			} else {
				oldRaw = old.raw
			}
		}
		space := m.blockSize - len(old.raw)
		count := len(data)
		if count > space {
			count = space
		}
		merged := append(append([]byte(nil), oldRaw...), data[:count]...)
		m.blocks[tail] = m.encode(merged)
		reasons = append(reasons, m.blocks[tail].reason)
		position = count
		if len(merged) == m.blockSize {
			m.cacheRemove(tail)
		} else if _, cached := m.cacheItems[tail]; cached && m.blocks[tail].form != StoredCompressed {
			m.cacheRemove(tail)
		} else {
			m.cacheReplace(tail, merged)
		}
	}

	for position < len(data) {
		end := position + m.blockSize
		if end > len(data) {
			end = len(data)
		}
		chunk := append([]byte(nil), data[position:end]...)
		m.starts = append(m.starts, m.logicalBytes())
		m.blocks = append(m.blocks, m.encode(chunk))
		reasons = append(reasons, m.blocks[len(m.blocks)-1].reason)
		position = end
	}
	return reasons
}

func (m *naiveModel) encode(raw []byte) modelBlock {
	m.compressions++
	compressed, ok := m.codec.Compress(raw)
	block := modelBlock{raw: append([]byte(nil), raw...)}
	if !ok {
		block.form = StoredRaw
		block.stored = append([]byte(nil), raw...)
		block.reason = CompressionFailed
	} else if int64(m.minGain) > int64(len(raw))-int64(len(compressed)) {
		block.form = StoredRaw
		block.stored = append([]byte(nil), raw...)
		block.reason = InsufficientGain
	} else {
		block.form = StoredCompressed
		block.stored = append([]byte(nil), compressed...)
		block.reason = NoFallback
	}
	block.physical = headerSize + len(block.stored)
	return block
}

func (m *naiveModel) read(offset, length int64) (string, bool, *Error, int, int) {
	logical := m.logicalBytes()
	if offset < 0 || length < 0 {
		return "", false, &Error{Kind: InvalidArgument, BlockIndex: -1, Op: "read"}, 0, 0
	}
	if offset > logical {
		return "", false, &Error{Kind: OutOfRange, BlockIndex: -1, Op: "read"}, 0, 0
	}
	end := logical
	if offset+length < logical {
		end = offset + length
	}
	if end > logical {
		end = logical
	}
	if offset == end {
		return "", offset == logical, nil, 0, 0
	}

	first := m.blockIndex(offset)
	last := m.blockIndex(end - 1)
	rawByBlock := make(map[int]string)
	pendingCompressed := make(map[int]string)
	pendingHits := make([]int, 0)
	localDecompresses := 0
	localHits := 0
	var readErr *Error

	for index := first; index <= last; index++ {
		if _, ok := m.cacheItems[index]; ok {
			rawByBlock[index] = string(m.blocks[index].raw)
			pendingHits = append(pendingHits, index)
			localHits++
			continue
		}

		block := m.blocks[index]
		var raw []byte
		if block.form == StoredRaw {
			raw = append([]byte(nil), block.stored...)
		} else {
			data, ok := m.codec.Decompress(block.stored)
			if !ok {
				readErr = blockError("read", index, DecompressFailed)
				continue
			}
			raw = data
			m.decompresses++
			localDecompresses++
			pendingCompressed[index] = string(raw)
		}
		if len(raw) != len(block.raw) {
			readErr = blockError("read", index, LengthMismatch)
			continue
		}
		if checksum(raw) == checksum(block.raw) {
			rawByBlock[index] = string(raw)
		} else {
			readErr = blockError("read", index, ChecksumMismatch)
		}
	}

	if readErr != nil {
		return "", false, readErr, localDecompresses, localHits
	}
	for index := first; index <= last; index++ {
		if raw, pending := pendingCompressed[index]; pending {
			m.cachePut(index, []byte(raw))
			continue
		}
		if _, ok := m.cacheItems[index]; ok {
			m.cacheTouch(index)
		}
	}
	m.hits += int64(localHits)

	var builder strings.Builder
	for index := first; index <= last; index++ {
		blockStart := int64(index) * int64(m.blockSize)
		blockStart = m.starts[index]
		raw := rawByBlock[index]
		start := offset - blockStart
		if start < 0 {
			start = 0
		}
		stop := end - blockStart
		if stop > int64(len(raw)) {
			stop = int64(len(raw))
		}
		builder.WriteString(raw[start:stop])
	}
	return builder.String(), end == logical, nil, localDecompresses, localHits
}

func (m *naiveModel) blockIndex(offset int64) int {
	low, high := 0, len(m.starts)
	for low < high {
		middle := (low + high) / 2
		if m.starts[middle] <= offset {
			low = middle + 1
		} else {
			high = middle
		}
	}
	return low - 1
}

func (m *naiveModel) logicalBytes() int64 {
	var total int64
	for _, block := range m.blocks {
		total += int64(len(block.raw))
	}
	return total
}

func (m *naiveModel) physicalBytes() int64 {
	var total int64
	for _, block := range m.blocks {
		total += int64(block.physical)
	}
	return total
}

func (m *naiveModel) countReasons() (compressed, insufficient, failed int64) {
	for _, block := range m.blocks {
		switch block.reason {
		case NoFallback:
			compressed++
		case InsufficientGain:
			insufficient++
		case CompressionFailed:
			failed++
		}
	}
	return
}

func (m *naiveModel) cachePut(index int, data []byte) {
	if m.cacheCap == 0 {
		return
	}
	for len(m.cacheItems) >= m.cacheCap {
		m.cacheRemoveOldest()
	}
	entry := &modelCacheEntry{index: index}
	entry.data = data
	m.cacheItems[index] = entry
	if m.cacheNewest == nil {
		m.cacheOldest = entry
		m.cacheNewest = entry
		return
	}
	entry.older = m.cacheNewest
	m.cacheNewest.newer = entry
	m.cacheNewest = entry
}

func (m *naiveModel) cacheReplace(index int, data []byte) {
	if entry, ok := m.cacheItems[index]; ok {
		entry.data = append([]byte(nil), data...)
	}
}

func (m *naiveModel) cacheTouch(index int) {
	entry := m.cacheItems[index]
	if entry == m.cacheNewest {
		return
	}
	m.cacheUnlink(entry)
	entry.older = m.cacheNewest
	m.cacheNewest.newer = entry
	m.cacheNewest = entry
}

func (m *naiveModel) cacheRemove(index int) {
	entry, ok := m.cacheItems[index]
	if !ok {
		return
	}
	m.cacheUnlink(entry)
	delete(m.cacheItems, index)
}

func (m *naiveModel) cacheRemoveOldest() {
	entry := m.cacheOldest
	m.cacheUnlink(entry)
	delete(m.cacheItems, entry.index)
}

func (m *naiveModel) cacheUnlink(entry *modelCacheEntry) {
	if entry.older != nil {
		entry.older.newer = entry.newer
	} else {
		m.cacheOldest = entry.newer
	}
	if entry.newer != nil {
		entry.newer.older = entry.older
	} else {
		m.cacheNewest = entry.older
	}
	entry.older = nil
	entry.newer = nil
}

func TestRandomOperationsMatchNaiveModel(t *testing.T) {
	for _, seed := range []int64{1, 7, 42, 99, 2026} {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			random := rand.New(rand.NewSource(seed))
			blockSize := 3 + random.Intn(5)
			minGain := random.Intn(5)
			cacheCap := random.Intn(4)
			container, err := New(Config{BlockSize: blockSize, MinGain: minGain, CacheCapacity: cacheCap}, shortCodec{}, shortCodec{})
			if err != nil {
				t.Fatal(err)
			}
			model := newNaiveModel(blockSize, minGain, cacheCap)
			var log strings.Builder
			log.WriteString(fmt.Sprintf("config blockSize=%d minGain=%d cacheCap=%d\n", blockSize, minGain, cacheCap))

			for step := 0; step < 300; step++ {
				if random.Intn(2) == 0 || model.logicalBytes() == 0 {
					data := randomAppend(random)
					log.WriteString(fmt.Sprintf("op=%d append input=%q\n", step, data))
					reasons := model.append(data)
					if err := container.Append(data); err != nil {
						t.Fatalf("container append error: %v\n%s", err, log.String())
					}
					log.WriteString(fmt.Sprintf("op=%d append output=ok reasons=%v\n", step, reasons))
				} else {
					offset := random.Int63n(model.logicalBytes() + 2)
					length := int64(random.Intn(2 * blockSize))
					modelData, modelEOF, modelErr, localDecompresses, localHits := model.read(offset, length)
					result, containerErr := container.ReadAt(offset, length)
					log.WriteString(fmt.Sprintf("op=%d read input={offset:%d,length:%d}\n", step, offset, length))
					if modelErr != nil {
						var target *Error
						if !asError(containerErr, &target) || target.Kind != modelErr.Kind {
							t.Fatalf("error mismatch model=%v actual=%v\n%s", modelErr, containerErr, log.String())
						}
						log.WriteString(fmt.Sprintf("op=%d read output=error kind=%d decompresses=%d\n", step, modelErr.Kind, localDecompresses))
					} else {
						if containerErr != nil || string(result.Data) != modelData || result.EOF != modelEOF {
							t.Fatalf("read mismatch data=%q eof=%v err=%v, model=%q eof=%v\n%s",
								result.Data, result.EOF, containerErr, modelData, modelEOF, log.String())
						}
						log.WriteString(fmt.Sprintf("op=%d read output=%q eof=%v hits=%d decompresses=%d\n",
							step, modelData, modelEOF, localHits, localDecompresses))
					}
				}
				assertStatsMatchModel(t, container, model, log.String())
			}
			t.Logf("\n%s", log.String())
		})
	}
}

func randomAppend(random *rand.Rand) []byte {
	alphabet := []string{"a", "b", "c", "x", "y", "z", "w", "ab", "cd", "xxx", "xxxx", "yyy", "zzz", "ww"}
	size := random.Intn(3 * 7)
	data := make([]byte, 0, size)
	for len(data) < size {
		token := alphabet[random.Intn(len(alphabet))]
		remaining := size - len(data)
		if len(token) > remaining {
			token = token[:remaining]
		}
		data = append(data, token...)
	}
	return data
}

func assertStatsMatchModel(t *testing.T, container *Container, model *naiveModel, log string) {
	t.Helper()
	actual := container.Stats()
	compressed, insufficient, failed := model.countReasons()
	want := Stats{
		LogicalBytes:            model.logicalBytes(),
		PhysicalBytes:           model.physicalBytes(),
		CompressedBlocks:        compressed,
		InsufficientGainBlocks:  insufficient,
		CompressionFailedBlocks: failed,
		CompressorCalls:         model.compressions,
		DecompressorCalls:       model.decompresses,
		CacheHits:               model.hits,
	}
	if actual != want {
		t.Fatalf("stats mismatch\nactual=%+v\nwant=%+v\n%s", actual, want, log)
	}
}

func asError(err error, target **Error) bool {
	blockErr, ok := err.(*Error)
	if ok {
		*target = blockErr
	}
	return ok
}
