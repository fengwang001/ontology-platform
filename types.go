package blockstore

type StorageForm uint8
type FallbackReason uint8

const (
	StoredRaw StorageForm = iota + 1
	StoredCompressed
)

const (
	NoFallback FallbackReason = iota
	CompressionFailed
	InsufficientGain
)

type Config struct {
	BlockSize     int
	MinGain       int
	CacheCapacity int
}

type Compressor interface {
	Compress(data []byte) ([]byte, bool)
}

type Decompressor interface {
	Decompress(data []byte) ([]byte, bool)
}

type Stats struct {
	LogicalBytes            int64
	PhysicalBytes           int64
	CompressedBlocks        int64
	InsufficientGainBlocks  int64
	CompressionFailedBlocks int64
	CompressorCalls         int64
	DecompressorCalls       int64
	CacheHits               int64
}

type ReadResult struct {
	Data []byte
	EOF  bool
}
