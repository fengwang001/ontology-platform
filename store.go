package blockstore

import "sync"

type Container struct {
	mu           sync.Mutex
	blockSize    int
	minGain      int
	compressor   Compressor
	decompressor Decompressor
	blocks       []*blockRecord
	starts       []int64
	cache        *lruCache

	logicalBytes            int64
	physicalBytes           int64
	compressedBlocks        int64
	insufficientGainBlocks  int64
	compressionFailedBlocks int64
	compressorCalls         int64
	decompressorCalls       int64
	cacheHits               int64
}

func New(config Config, compressor Compressor, decompressor Decompressor) (*Container, error) {
	if config.BlockSize <= 0 || config.MinGain < 0 || config.CacheCapacity < 0 {
		return nil, &Error{Kind: InvalidArgument, BlockIndex: -1, Op: "new"}
	}
	if compressor == nil || decompressor == nil {
		return nil, &Error{Kind: InvalidArgument, BlockIndex: -1, Op: "new"}
	}
	return &Container{
		blockSize:    config.BlockSize,
		minGain:      config.MinGain,
		compressor:   compressor,
		decompressor: decompressor,
		cache:        newLRUCache(config.CacheCapacity),
	}, nil
}

func (c *Container) Append(data []byte) error {
	if len(data) == 0 {
		return nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	nextBlocks := make([]*blockRecord, len(c.blocks))
	copy(nextBlocks, c.blocks)
	nextStarts := make([]int64, len(c.starts))
	copy(nextStarts, c.starts)
	nextLogical := c.logicalBytes
	nextPhysical := c.physicalBytes
	nextCompressed := c.compressedBlocks
	nextInsufficient := c.insufficientGainBlocks
	nextFailed := c.compressionFailedBlocks
	nextCalls := c.compressorCalls
	rewritten := -1
	tailCompleted := false
	decompressions := int64(0)
	tailWasCached := false
	var rewrittenContent []byte

	dataPosition := 0
	if len(nextBlocks) > 0 && nextLogical%int64(c.blockSize) != 0 {
		rewritten = len(nextBlocks) - 1
		old := nextBlocks[rewritten]
		oldLength := int(nextLogical - nextStarts[rewritten])
		var oldContent []byte
		if cached, ok := c.cache.peek(rewritten); ok {
			oldContent = append([]byte(nil), cached...)
			tailWasCached = true
		} else {
			var err error
			oldContent, _, _, err = decodeBlock(old, rewritten, countingDecompressor{c, &decompressions})
			if err != nil {
				c.decompressorCalls += decompressions
				return err
			}
		}

		available := c.blockSize - oldLength
		copyLength := len(data)
		if copyLength > available {
			copyLength = available
		}
		merged := make([]byte, 0, c.blockSize)
		merged = append(merged, oldContent[:oldLength]...)
		merged = append(merged, data[:copyLength]...)
		rewrittenContent = merged
		dataPosition = copyLength

		nextPhysical -= int64(len(old.physical))
		if old.reason == CompressionFailed {
			nextFailed--
		} else if old.reason == InsufficientGain {
			nextInsufficient--
		} else {
			nextCompressed--
		}

		record := encodeBlock(merged, c.minGain, countingCompressor{c, &nextCalls})
		nextBlocks[rewritten] = record
		nextPhysical += int64(len(record.physical))
		c.countEncoding(record, &nextCompressed, &nextInsufficient, &nextFailed)
		nextLogical += int64(copyLength)
		tailCompleted = len(merged) == c.blockSize
	}

	for dataPosition < len(data) {
		end := dataPosition + c.blockSize
		if end > len(data) {
			end = len(data)
		}
		chunk := append([]byte(nil), data[dataPosition:end]...)
		record := encodeBlock(chunk, c.minGain, countingCompressor{c, &nextCalls})
		nextStarts = append(nextStarts, nextLogical)
		nextBlocks = append(nextBlocks, record)
		nextLogical += int64(len(chunk))
		nextPhysical += int64(len(record.physical))
		c.countEncoding(record, &nextCompressed, &nextInsufficient, &nextFailed)
		dataPosition = end
	}

	c.blocks = nextBlocks
	c.starts = nextStarts
	c.logicalBytes = nextLogical
	c.physicalBytes = nextPhysical
	c.compressedBlocks = nextCompressed
	c.insufficientGainBlocks = nextInsufficient
	c.compressionFailedBlocks = nextFailed
	c.compressorCalls = nextCalls
	c.decompressorCalls += decompressions
	if rewritten >= 0 && tailCompleted {
		c.cache.remove(rewritten)
	} else if rewritten >= 0 && tailWasCached && headerForm(nextBlocks[rewritten]) == StoredCompressed {
		c.cache.replace(rewritten, append([]byte(nil), rewrittenContent...))
	} else if rewritten >= 0 && tailWasCached {
		c.cache.remove(rewritten)
	}
	return nil
}

func (c *Container) ReadAt(offset, length int64) (ReadResult, error) {
	if offset < 0 || length < 0 {
		return ReadResult{}, &Error{Kind: InvalidArgument, BlockIndex: -1, Op: "read"}
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if offset > c.logicalBytes {
		return ReadResult{}, &Error{Kind: OutOfRange, BlockIndex: -1, Op: "read"}
	}

	end := c.logicalBytes
	if offset+length < c.logicalBytes {
		end = offset + length
	}
	if end > c.logicalBytes {
		end = c.logicalBytes
	}
	if offset == end {
		return ReadResult{Data: []byte{}, EOF: offset == c.logicalBytes}, nil
	}

	first := c.blockIndex(offset)
	last := c.blockIndex(end - 1)
	decoded := make([][]byte, last-first+1)
	pendingPuts := make(map[int][]byte)
	pendingTouches := make([]int, 0, last-first+1)
	hits := int64(0)
	decompressions := int64(0)
	var readErr *Error

	for block := first; block <= last; block++ {
		if data, ok := c.cache.peek(block); ok {
			decoded[block-first] = append([]byte(nil), data...)
			pendingTouches = append(pendingTouches, block)
			hits++
			continue
		}

		data, form, _, err := decodeBlock(c.blocks[block], block, countingDecompressor{c, &decompressions})
		if err != nil {
			blockErr, _ := err.(*Error)
			if readErr == nil || blockErr.Kind < readErr.Kind ||
				(blockErr.Kind == readErr.Kind && blockErr.BlockIndex < readErr.BlockIndex) {
				readErr = blockErr
			}
		}
		decoded[block-first] = data
		if err == nil && form == StoredCompressed {
			pendingPuts[block] = append([]byte(nil), data...)
		}
	}

	if readErr != nil {
		c.decompressorCalls += decompressions
		return ReadResult{}, readErr
	}

	for block := first; block <= last; block++ {
		if data, pending := pendingPuts[block]; pending {
			c.cache.put(block, data)
			continue
		}
		c.cache.get(block)
	}
	c.cacheHits += hits
	c.decompressorCalls += decompressions

	result := make([]byte, 0, end-offset)
	for block := first; block <= last; block++ {
		blockStart := c.starts[block]
		sliceStart := offset - blockStart
		if sliceStart < 0 {
			sliceStart = 0
		}
		sliceEnd := end - blockStart
		if sliceEnd > int64(len(decoded[block-first])) {
			sliceEnd = int64(len(decoded[block-first]))
		}
		if sliceStart < sliceEnd {
			result = append(result, decoded[block-first][sliceStart:sliceEnd]...)
		}
	}
	return ReadResult{Data: result, EOF: end == c.logicalBytes}, nil
}

func (c *Container) Stats() Stats {
	c.mu.Lock()
	defer c.mu.Unlock()
	return Stats{
		LogicalBytes:            c.logicalBytes,
		PhysicalBytes:           c.physicalBytes,
		CompressedBlocks:        c.compressedBlocks,
		InsufficientGainBlocks:  c.insufficientGainBlocks,
		CompressionFailedBlocks: c.compressionFailedBlocks,
		CompressorCalls:         c.compressorCalls,
		DecompressorCalls:       c.decompressorCalls,
		CacheHits:               c.cacheHits,
	}
}

type countingCompressor struct {
	container *Container
	calls     *int64
}

func (c countingCompressor) Compress(data []byte) ([]byte, bool) {
	*c.calls++
	return c.container.compressor.Compress(data)
}

type countingDecompressor struct {
	container    *Container
	decompresses *int64
}

func (c countingDecompressor) Decompress(data []byte) ([]byte, bool) {
	*c.decompresses++
	return c.container.decompressor.Decompress(data)
}

func (c *Container) countEncoding(record *blockRecord, compressed, insufficient, failed *int64) {
	switch record.reason {
	case CompressionFailed:
		*failed++
	case InsufficientGain:
		*insufficient++
	default:
		*compressed++
	}
}

func (c *Container) blockIndex(offset int64) int {
	low, high := 0, len(c.starts)
	for low < high {
		middle := (low + high) / 2
		if c.starts[middle] <= offset {
			low = middle + 1
		} else {
			high = middle
		}
	}
	return low - 1
}
