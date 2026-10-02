package jit

import "math/bits"

// Counter is an unsigned 128-bit value used for exact counts and threshold products.
type Counter struct {
	// Lo is the lower 64 bits.
	Lo uint64
	// Hi is the upper 64 bits.
	Hi uint64
}

func counterFromInt64(value int64) Counter {
	return Counter{Lo: uint64(value)}
}

func shiftInt64Right(value int64, shift uint) int64 {
	if shift >= 63 {
		return 0
	}
	return value >> shift
}

func (c Counter) add(other Counter) Counter {
	lo, carry := bits.Add64(c.Lo, other.Lo, 0)
	return Counter{Lo: lo, Hi: c.Hi + other.Hi + carry}
}

func (c Counter) shiftRight(shift uint) Counter {
	if shift == 0 {
		return c
	}
	if shift >= 128 {
		return Counter{}
	}
	if shift >= 64 {
		return Counter{Lo: c.Hi >> (shift - 64)}
	}
	return Counter{
		Lo: c.Lo>>shift | c.Hi<<(64-shift),
		Hi: c.Hi >> shift,
	}
}

func (c Counter) less(other Counter) bool {
	if c.Hi != other.Hi {
		return c.Hi < other.Hi
	}
	return c.Lo < other.Lo
}

func (c Counter) atLeast(other Counter) bool {
	return !c.less(other)
}

func (c Counter) mul64(value uint64) Counter {
	hi, lo := bits.Mul64(c.Lo, value)
	hi += c.Hi * value
	return Counter{Lo: lo, Hi: hi}
}

func multiply64(left, right int64) Counter {
	hi, lo := bits.Mul64(uint64(left), uint64(right))
	return Counter{Lo: lo, Hi: hi}
}

func multiply3(left, middle, right int64) Counter {
	return multiply64(left, middle).mul64(uint64(right))
}
