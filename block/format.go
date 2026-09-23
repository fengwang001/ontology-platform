package block

import (
	"encoding/binary"
	"hash/crc32"
)

const (
	magic       = "ONDIC"
	version     = 1
	hdrLen      = 24
	restartFlag = uint32(1) << 31
)

type header struct {
	k, n, restartCount, dataLen uint32
}

func putU32(b []byte, v uint32) { binary.LittleEndian.PutUint32(b, v) }

func u32(b []byte) uint32 { return binary.LittleEndian.Uint32(b) }

func parseHeader(raw []byte) (header, error) {
	if len(raw) < hdrLen || string(raw[:5]) != magic || raw[5] != version {
		return header{}, ErrHeader
	}
	return header{
		k:            uint32(binary.LittleEndian.Uint16(raw[6:8])),
		n:            u32(raw[8:12]),
		restartCount: u32(raw[12:16]),
		dataLen:      u32(raw[16:20]),
	}, nil
}

// Encode builds a compressed block. ss must be strictly increasing.
func Encode(ss []string, k int) ([]byte, error) {
	if k < 1 {
		k = 1
	}
	if err := CheckOrder(ss); err != nil {
		return nil, err
	}
	data := make([]byte, 0)
	restarts := make([]byte, 0)
	prev := ""
	for i, s := range ss {
		shared := 0
		if i%k != 0 {
			shared = commonPrefix(prev, s)
		}
		var h [8]byte
		sharedField := uint32(shared)
		if i%k == 0 {
			sharedField |= restartFlag
		}
		putU32(h[:4], sharedField)
		putU32(h[4:], uint32(len(s)-shared))
		if i%k == 0 {
			var ob [4]byte
			putU32(ob[:], uint32(len(data)))
			restarts = append(restarts, ob[:]...)
		}
		data = append(data, h[:]...)
		data = append(data, s[shared:]...)
		prev = s
	}
	out := make([]byte, 0, hdrLen+len(data)+len(restarts)+4)
	hdr := make([]byte, hdrLen)
	copy(hdr, magic)
	hdr[5] = version
	binary.LittleEndian.PutUint16(hdr[6:8], uint16(k))
	putU32(hdr[8:12], uint32(len(ss)))
	putU32(hdr[12:16], uint32(len(restarts)/4))
	putU32(hdr[16:20], uint32(len(data)))
	out = append(out, hdr...)
	out = append(out, data...)
	out = append(out, restarts...)
	var crcb [4]byte
	putU32(crcb[:], crc32.ChecksumIEEE(out))
	out = append(out, crcb[:]...)
	return out, nil
}

func commonPrefix(a, b string) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	i := 0
	for i < n && a[i] == b[i] {
		i++
	}
	return i
}
