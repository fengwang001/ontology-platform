package ontology

// appendEscaped writes src using an order-preserving escaping scheme.
// Raw 0x00 and 0x01 are escaped; an unescaped 0x00 can therefore mark the end.
func appendEscaped(dst []byte, src []byte) []byte {
	for _, value := range src {
		switch value {
		case 0x00:
			dst = append(dst, 0x01, 0x01)
		case 0x01:
			dst = append(dst, 0x01, 0x02)
		default:
			dst = append(dst, value)
		}
	}
	return dst
}

// readEscaped reads until an unescaped 0x00. It returns the decoded payload and
// the bytes following the terminator.
func readEscaped(data []byte) (payload []byte, rest []byte, err error) {
	result := make([]byte, 0, len(data))
	for index := 0; index < len(data); index++ {
		value := data[index]
		switch {
		case value == 0x00:
			return result, data[index+1:], nil
		case value == 0x01:
			if index+1 >= len(data) {
				return nil, nil, errTruncatedEscape
			}
			index++
			switch data[index] {
			case 0x01:
				result = append(result, 0x00)
			case 0x02:
				result = append(result, 0x01)
			default:
				return nil, nil, errInvalidEscape
			}
		default:
			result = append(result, value)
		}
	}
	return nil, nil, errTruncated
}
