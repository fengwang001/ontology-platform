package ontology

// Decode reconstructs exactly keyCount keys. It rejects truncated input,
// trailing bytes, and illegal type markers instead of returning partial data.
func Decode(data []byte, keyCount int, directions []Direction) ([]Key, error) {
	if len(directions) > 0 && len(directions) != keyCount {
		return nil, &DecodeError{Reason: "directions length does not match key count"}
	}
	keys := make([]Key, 0, keyCount)
	rest := data
	for index := 0; index < keyCount; index++ {
		if len(rest) == 0 {
			return nil, keyError(index, "truncated byte string")
		}
		descending := len(directions) > 0 && directions[index] == Desc
		var key Key
		var err error
		if descending {
			key, rest, err = decodeDescending(rest)
		} else {
			key, rest, err = decodeAscending(rest)
		}
		if err != nil {
			return nil, keyError(index, err.Error())
		}
		keys = append(keys, key)
	}
	if len(rest) != 0 {
		return nil, keyError(keyCount, "unexpected trailing bytes")
	}
	return keys, nil
}

func decodeAscending(data []byte) (Key, []byte, error) {
	switch data[0] {
	case tagNull:
		return nil, data[1:], nil
	case tagNumber:
		return decodeNumericBody(data[1:])
	case tagString:
		value, rest, err := readEscaped(data[1:])
		if err != nil {
			return nil, nil, err
		}
		return string(value), rest, nil
	default:
		return nil, nil, &DecodeError{Reason: "invalid type marker"}
	}
}

func decodeDescending(data []byte) (Key, []byte, error) {
	switch data[0] {
	case 0x00:
		return nil, data[1:], nil
	case 0x01:
		inverted := make([]byte, len(data)-1)
		for index, value := range data[1:] {
			inverted[index] = value ^ 0xff
		}
		return decodeAscendingInverted(inverted, data)
	default:
		return nil, nil, &DecodeError{Reason: "invalid descending marker"}
	}
}

func decodeAscendingInverted(inverted, original []byte) (Key, []byte, error) {
	if len(inverted) == 0 {
		return nil, nil, errTruncated
	}
	tag := inverted[0]
	switch tag {
	case tagNumber:
		key, numericRest, err := decodeNumericBody(inverted[1:])
		if err != nil {
			return nil, nil, err
		}
		consumed := len(inverted) - len(numericRest)
		return key, original[1+consumed:], nil
	case tagString:
		value, consumed, err := readEscaped(inverted[1:])
		if err != nil {
			return nil, nil, err
		}
		consumedBytes := len(inverted) - len(consumed)
		return string(value), original[consumedBytes+1:], nil
	default:
		return nil, nil, &DecodeError{Reason: "invalid inverted type marker"}
	}
}

func keyError(index int, reason string) error {
	return &DecodeError{KeyIndex: index, Reason: reason}
}
