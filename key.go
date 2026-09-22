package ontology

// Key is one sort key. The dynamic type must be one of:
// nil, int64, float64, or string.
type Key any

// Direction controls whether one key position is ascending or descending.
type Direction int

const (
	// Asc is ascending order.
	Asc Direction = iota
	// Desc reverses non-null values. Null ordering is unaffected.
	Desc
)

// DecodeError is returned for malformed encoded keys. KeyIndex is zero based.
type DecodeError struct {
	KeyIndex int
	Reason   string
}

func (err *DecodeError) Error() string {
	return "ontology: decode error at key " + itoa(err.KeyIndex) + ": " + err.Reason
}

// KeyError is returned when a caller supplies an unsupported Go value.
type KeyError struct {
	KeyIndex int
	Reason   string
}

func (err *KeyError) Error() string {
	return "ontology: invalid key " + itoa(err.KeyIndex) + ": " + err.Reason
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	negative := value < 0
	if negative {
		value = -value
	}
	var digits [24]byte
	size := 0
	for value > 0 {
		digits[size] = byte('0' + value%10)
		size++
		value /= 10
	}
	result := make([]byte, 0, size+1)
	if negative {
		result = append(result, '-')
	}
	for index := size - 1; index >= 0; index-- {
	result = append(result, digits[index])
	}
	return string(result)
}
