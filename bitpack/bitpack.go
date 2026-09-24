package bitpack

import "errors"

var ErrWidth = errors.New("bitpack: width must be between 1 and 64")

func Pack(values []uint64, width int) []byte {
	if width < 1 || width > 64 {
		return nil
	}
	totalBits := len(values) * width
	out := make([]byte, (totalBits+7)/8)
	var bit uint
	for _, value := range values {
		v := value
		if width < 64 {
			v &= (uint64(1) << width) - 1
		}
		for b := 0; b < width; b++ {
			if v&1 != 0 {
				out[bit/8] |= 1 << (bit % 8)
			}
			v >>= 1
			bit++
		}
	}
	return out
}

func Unpack(data []byte, count, width int) ([]uint64, error) {
	if width < 1 || width > 64 {
		return nil, ErrWidth
	}
	needBits := 0
	if count > 0 {
		needBits = (count-1)*width + width
	}
	needBytes := (needBits + 7) / 8
	if len(data) < needBytes {
		return nil, errors.New("bitpack: truncated data")
	}
	values := make([]uint64, count)
	var bit uint
	for i := 0; i < count; i++ {
		var v uint64
		for b := 0; b < width; b++ {
			pos := bit + uint(b)
			if data[pos/8]&(1<<(pos%8)) != 0 {
				if b < 63 {
					v |= uint64(1) << b
				}
			}
		}
		if width == 64 {
			lo := bit / 8
			v = uint64(data[lo]) | uint64(data[lo+1])<<8 |
				uint64(data[lo+2])<<16 | uint64(data[lo+3])<<24 |
				uint64(data[lo+4])<<32 | uint64(data[lo+5])<<40 |
				uint64(data[lo+6])<<48 | uint64(data[lo+7])<<56
		}
		values[i] = v
		bit += uint(width)
	}
	return values, nil
}

func ZigZag(v int64) uint64 { return uint64(v<<1) ^ uint64(v>>63) }

func UnZigZag(v uint64) int64 { return int64(v>>1) ^ -int64(v&1) }
