package lzw

// GIF LZW 保留码与码宽常量。
const (
	ClearCode = 256
	EndCode   = 257
	firstFree = 258
	minBits   = 9
	maxBits   = 12
	maxCode   = 1<<maxBits - 1 // 4095
)
