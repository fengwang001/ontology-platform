package ontology

import "math"

const (
	tagNull   = 0x00
	tagNumber = 0x02
	tagString = 0x03
)

// Encoder converts one row of sort keys into an order-preserving byte string.
// Its counters are valid after Encode or EncodedLen.
type Encoder struct {
	nullCount int
}

// NewEncoder creates a process-local encoder. No state is shared.
func NewEncoder() *Encoder {
	return &Encoder{}
}

// Encode encodes keys. Every direction defaults to ascending when nil is given.
// The input slice and its strings are never modified.
func (encoder *Encoder) Encode(keys []Key, directions []Direction) ([]byte, error) {
	if len(directions) > 0 && len(directions) != len(keys) {
		return nil, &KeyError{Reason: "directions length does not match keys"}
	}
	encoder.nullCount = 0
	result := make([]byte, 0, estimateSize(keys))
	for index, key := range keys {
		switch value := key.(type) {
		case nil:
			encoder.nullCount++
			result = appendNull(result, len(directions) > 0 && directions[index] == Desc)
		case int64:
			result = appendTypedKey(result, tagNumber, index, directions,
				func(dst []byte) []byte { return appendIntegerBody(dst, value) })
		case float64:
			if math.IsNaN(value) {
				encoder.nullCount++
				result = appendNull(result, len(directions) > 0 && directions[index] == Desc)
			} else {
				result = appendTypedKey(result, tagNumber, index, directions,
					func(dst []byte) []byte { return appendFloatBody(dst, value) })
			}
		case string:
			result = appendTypedKey(result, tagString, index, directions, func(dst []byte) []byte {
				dst = appendEscaped(dst, []byte(value))
				return append(dst, 0x00)
			})
		default:
			return nil, &KeyError{KeyIndex: index, Reason: "unsupported key type"}
		}
	}
	return result, nil
}

// EncodedLen returns the exact encoded length without retaining the result.
func (encoder *Encoder) EncodedLen(keys []Key, directions []Direction) (int, error) {
	encoded, err := encoder.Encode(keys, directions)
	return len(encoded), err
}

// NullCount reports nil keys plus NaN float64 keys from the latest encoding.
func (encoder *Encoder) NullCount() int {
	return encoder.nullCount
}

// Encode is a stateless convenience wrapper. It cannot return the NaN count.
func Encode(keys []Key, directions []Direction) ([]byte, error) {
	return NewEncoder().Encode(keys, directions)
}

func appendNull(dst []byte, descending bool) []byte {
	if descending {
		return append(dst, 0x00)
	}
	return append(dst, tagNull)
}

func appendTypedKey(dst []byte, tag byte, index int, directions []Direction,
	body func([]byte) []byte) []byte {
	descending := len(directions) > 0 && directions[index] == Desc
	if descending {
		dst = append(dst, 0x01)
		payload := body([]byte{tag})
		return appendInverted(dst, payload)
	}
	return body(append(dst, tag))
}

func estimateSize(keys []Key) int {
	total := 0
	for _, key := range keys {
		switch value := key.(type) {
		case string:
			total += len(value) + 2
		default:
			total += 12
		}
	}
	return total
}
