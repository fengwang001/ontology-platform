package chunkcontainer

import (
	"bytes"
	"hash/crc32"
	"sync"
	"sync/atomic"
)

type Stats struct {
	LogicalBytes             int64
	PhysicalBytes            int64
	CompressedBlocks         int64
	InsufficientGainBlocks   int64
	CompressionFailureBlocks int64
	CompressorCalls          int64
	DecompressorCalls        int64
	CacheHits                int64
}

type Container struct {
	config       Config
	compressor   Compressor
	decompressor Decompressor
	cache        *blockCache
	decompressMu sync.Mutex

	mu     sync.RWMutex
	blocks []blockRecord

	compressorCalls   atomic.Int64
	decompressorCalls atomic.Int64
	cacheHits         atomic.Int64
}

func New(config Config, compressor Compressor, decompressor Decompressor) (*Container, error) {
	if config.BlockSize <= 0 || config.MinimumGain < 0 || config.CacheCapacity < 0 || compressor == nil || decompressor == nil {
		return nil, ErrInvalidArgument
	}
	return &Container{
		config:       config,
		compressor:   compressor,
		decompressor: decompressor,
		cache:        newBlockCache(config.CacheCapacity),
	}, nil
}

func (c *Container) Append(data []byte) (AppendResult, error) {
	if len(data) == 0 {
		return AppendResult{}, nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	compressor := &countingCompressor{Compressor: c.compressor, calls: &c.compressorCalls}
	nextBlocks := append([]blockRecord(nil), c.blocks...)
	input := bytes.Clone(data)
	decisions := make([]BlockDecision, 0)
	cursor := 0
	rewrittenIndex := -1

	if len(nextBlocks) > 0 && nextBlocks[len(nextBlocks)-1].origLength < c.config.BlockSize {
		index := len(nextBlocks) - 1
		rewrittenIndex = index
		capacity := c.config.BlockSize - nextBlocks[index].origLength
		amount := min(capacity, len(input))
		combined := make([]byte, 0, nextBlocks[index].origLength+amount)
		combined = append(combined, nextBlocks[index].original...)
		combined = append(combined, input[:amount]...)
		record := decideBlock(combined, c.config.MinimumGain, compressor)
		nextBlocks[index] = record
		decisions = append(decisions, decisionForBlock(index, record))
		cursor = amount
	}

	for cursor < len(input) {
		end := min(cursor+c.config.BlockSize, len(input))
		record := decideBlock(input[cursor:end], c.config.MinimumGain, compressor)
		index := len(nextBlocks)
		nextBlocks = append(nextBlocks, record)
		decisions = append(decisions, decisionForBlock(index, record))
		cursor = end
	}

	c.blocks = nextBlocks
	if rewrittenIndex >= 0 {
		c.cache.remove(rewrittenIndex)
	}
	return AppendResult{Blocks: decisions}, nil
}

func (c *Container) ReadAt(offset, length int) (ReadResult, error) {
	if offset < 0 || length < 0 {
		return ReadResult{}, ErrInvalidArgument
	}

	c.mu.RLock()
	defer c.mu.RUnlock()

	totalLength := c.logicalLengthLocked()
	if offset > totalLength {
		return ReadResult{}, ErrOutOfBounds
	}

	end := min(offset+length, totalLength)
	if offset == end {
		return ReadResult{Data: []byte{}, EOF: offset == totalLength}, nil
	}

	firstBlock := offset / c.config.BlockSize
	lastBlock := (end - 1) / c.config.BlockSize
	decodedByIndex := make(map[int][]byte, lastBlock-firstBlock+1)
	missedBlocks := make(map[int]bool, lastBlock-firstBlock+1)
	hits := int64(0)
	decompressions := int64(0)
	var firstLengthMismatch *BlockError
	var firstChecksumMismatch *BlockError

	for index := firstBlock; index <= lastBlock; index++ {
		record := c.blocks[index]
		var decoded []byte

		if record.form == FormDirect {
			decoded = record.original
		} else if cached, ok := c.cache.peek(index); ok {
			decoded = cached
			hits++
		} else {
			missedBlocks[index] = true
			c.decompressMu.Lock()
			plain, ok := c.decompressor.Decompress(bytes.Clone(record.stored))
			c.decompressMu.Unlock()
			decompressions++
			if !ok {
				c.decompressorCalls.Add(decompressions)
				c.cacheHits.Add(hits)
				return ReadResult{}, BlockError{Kind: Decompression, BlockIndex: index}
			}
			decoded = bytes.Clone(plain)
		}

		if len(decoded) != record.origLength {
			if firstLengthMismatch == nil {
				firstLengthMismatch = &BlockError{Kind: LengthMismatch, BlockIndex: index}
			}
		} else if crc32.ChecksumIEEE(decoded) != record.checksum {
			if firstChecksumMismatch == nil {
				firstChecksumMismatch = &BlockError{Kind: ChecksumMismatch, BlockIndex: index}
			}
		}
		decodedByIndex[index] = decoded
	}

	if firstLengthMismatch != nil {
		c.decompressorCalls.Add(decompressions)
		c.cacheHits.Add(hits)
		return ReadResult{}, *firstLengthMismatch
	}
	if firstChecksumMismatch != nil {
		c.decompressorCalls.Add(decompressions)
		c.cacheHits.Add(hits)
		return ReadResult{}, *firstChecksumMismatch
	}

	output := make([]byte, 0, end-offset)
	for index := firstBlock; index <= lastBlock; index++ {
		decoded := decodedByIndex[index]

		blockStart := index * c.config.BlockSize
		readStart := max(offset, blockStart)
		readEnd := min(end, blockStart+len(decoded))
		output = append(output, decoded[readStart-blockStart:readEnd-blockStart]...)
	}

	for index := firstBlock; index <= lastBlock; index++ {
		if c.blocks[index].form == FormCompressed {
			c.cache.touch(index, decodedByIndex[index])
		}
	}
	c.decompressorCalls.Add(decompressions)
	c.cacheHits.Add(hits)
	return ReadResult{Data: output, EOF: end == totalLength}, nil
}

func (c *Container) Stats() Stats {
	c.mu.RLock()
	defer c.mu.RUnlock()

	stats := Stats{
		CompressorCalls:   c.compressorCalls.Load(),
		DecompressorCalls: c.decompressorCalls.Load(),
		CacheHits:         c.cacheHits.Load(),
	}
	stats.LogicalBytes = int64(c.logicalLengthLocked())
	for _, record := range c.blocks {
		stats.PhysicalBytes += int64(HeaderSize + len(record.stored))
		switch {
		case record.form == FormCompressed:
			stats.CompressedBlocks++
		case record.reason == ReasonInsufficientGain:
			stats.InsufficientGainBlocks++
		case record.reason == ReasonCompressionFailed:
			stats.CompressionFailureBlocks++
		}
	}
	return stats
}

func (c *Container) logicalLengthLocked() int {
	if len(c.blocks) == 0 {
		return 0
	}
	return (len(c.blocks)-1)*c.config.BlockSize + c.blocks[len(c.blocks)-1].origLength
}

type countingCompressor struct {
	Compressor
	calls *atomic.Int64
}

func (compressor *countingCompressor) Compress(data []byte) ([]byte, bool) {
	compressed, ok := compressor.Compressor.Compress(data)
	compressor.calls.Add(1)
	return compressed, ok
}
