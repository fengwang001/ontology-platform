package dnsname

import "sync"

type Builder struct {
	mu      sync.Mutex
	message []byte
	base    int
	offsets map[string]int
}

// NewBuilder starts a message with the usual 12-byte DNS header.
func NewBuilder() *Builder {
	b, _ := NewBuilderAt(12)
	return b
}

// NewBuilderAt starts a message whose first writable offset is base.
func NewBuilderAt(base int) (*Builder, error) {
	if base < 0 || base > 65535 {
		return nil, ErrInvalidBase
	}
	return &Builder{
		message: make([]byte, base),
		base:    base,
		offsets: make(map[string]int),
	}, nil
}

// Bytes returns a copy of the message assembled so far.
func (b *Builder) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()

	result := make([]byte, len(b.message))
	copy(result, b.message)
	return result
}

// WriteName appends labels using suffix-compressed DNS name wire encoding.
func (b *Builder) WriteName(labels [][]byte) error {
	if err := validateLabels(labels); err != nil {
		return err
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	start := len(b.message)
	if len(labels) == 0 {
		if start+1 > 65535 {
			return ErrMessageTooLong
		}
		b.message = append(b.message, 0)
		return nil
	}

	hit := len(labels)
	for index := range labels {
		key := suffixKey(labels[index:])
		if _, ok := b.offsets[key]; ok {
			hit = index
			break
		}
	}

	written := 1
	if hit < len(labels) {
		written++
	}
	for index := 0; index < hit; index++ {
		written += 1 + len(labels[index])
	}
	if start+written > 65535 {
		return ErrMessageTooLong
	}

	suffixStart := start
	for index := 0; index < hit; index++ {
		if suffixStart <= 16383 {
			b.offsets[suffixKey(labels[index:])] = suffixStart
		}
		label := labels[index]
		b.message = append(b.message, byte(len(label)))
		b.message = append(b.message, label...)
		suffixStart += 1 + len(label)
	}

	if hit == len(labels) {
		b.message = append(b.message, 0)
		return nil
	}

	target := b.offsets[suffixKey(labels[hit:])]
	b.message = append(b.message, pointerBytes(target)...)
	return nil
}

func validateLabels(labels [][]byte) error {
	total := 1
	for _, label := range labels {
		if len(label) == 0 {
			return ErrEmptyLabel
		}
		if len(label) > 63 {
			return ErrLabelTooLong
		}
		total += 1 + len(label)
		if total > 255 {
			return ErrNameTooLong
		}
	}
	return nil
}

func suffixKey(labels [][]byte) string {
	key := make([]byte, 0, len(labels))
	for _, label := range labels {
		key = append(key, byte(len(label)))
		key = appendASCIILower(key, label)
	}
	return string(key)
}

func appendASCIILower(dst []byte, label []byte) []byte {
	for _, value := range label {
		if value >= 'A' && value <= 'Z' {
			value += 'a' - 'A'
		}
		dst = append(dst, value)
	}
	return dst
}

func pointerBytes(offset int) []byte {
	return []byte{byte(0xc0 | offset>>8), byte(offset)}
}
