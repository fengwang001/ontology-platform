package dnsname

// Decode reads a DNS name at offset. The consumed count ends after the first
// two-byte pointer, or after the terminating zero when no pointer is used.
func Decode(message []byte, offset int, base int) (name [][]byte, consumed int, err error) {
	if base < 0 || base > 65535 {
		return nil, 0, ErrInvalidBase
	}

	start := offset
	current := offset
	totalLength := 0
	followedPointer := false
	seen := make(map[int]struct{})

	for {
		if current < 0 || current >= len(message) {
			return nil, 0, ErrTruncated
		}

		length := int(message[current])
		switch length & 0xc0 {
		case 0x00:
			if length == 0 {
				if totalLength+1 > 255 {
					return nil, 0, ErrNameTooLong
				}
				if !followedPointer {
					consumed = current - start + 1
				}
				result := make([][]byte, len(name))
				for index, label := range name {
					result[index] = append([]byte(nil), label...)
				}
				return result, consumed, nil
			}

			if current+1+length > len(message) {
				return nil, 0, ErrTruncated
			}
			totalLength += 1 + length
			if totalLength > 255 {
				return nil, 0, ErrNameTooLong
			}

			label := append([]byte(nil), message[current+1:current+1+length]...)
			name = append(name, label)
			current += 1 + length
		case 0xc0:
			if current+1 >= len(message) {
				return nil, 0, ErrTruncated
			}

			if !followedPointer {
				consumed = current - start + 2
			}

			target := int(message[current]&0x3f)<<8 | int(message[current+1])
			if target < base {
				return nil, 0, ErrPointerBeforeBase
			}
			if target >= current {
				return nil, 0, ErrForwardPointer
			}
			if _, ok := seen[target]; ok {
				return nil, 0, ErrForwardPointer
			}
			seen[current] = struct{}{}
			current = target
			followedPointer = true
		default:
			return nil, 0, ErrReservedLabel
		}
	}
}
