// Package gh is the geometry-hashing core: canonical coordinates of
// a point relative to an ordered basis, fixed-step quantization and
// hash keys. It depends on no other package in the module.
package gh

// Point is an integer-lattice point.
type Point struct{ X, Y int64 }

// stepBits fixes the quantization cell: one cell is 2^-stepBits in
// canonical (u,v) space. Applied identically to template and scene,
// so one similarity copy quantizes to the same keys on both sides.
const stepBits = 4

const scale = int64(1) << stepBits

// Perp is the counter-clockwise 90-degree rotation of v.
func Perp(v Point) Point { return Point{-v.Y, v.X} }

// BasisUV returns the exact rational canonical coordinates of p in
// the frame with origin a and unit axis pointing from a to b:
//
//	u = <p-a, b-a>/|b-a|^2
//	v = <p-a, perp(b-a)>/|b-a|^2
//
// Coordinates are returned as (nu/den, nv/den) with den = |b-a|^2 > 0.
// A degenerate basis (a == b) yields den == 0 and must be rejected.
func BasisUV(a, b, p Point) (nu, nv, den int64) {
	dx, dy := b.X-a.X, b.Y-a.Y
	px, py := p.X-a.X, p.Y-a.Y
	nu = px*dx + py*dy    // <p-a, b-a>
	nv = px*(-dy) + py*dx // <p-a, perp(b-a)>
	den = dx*dx + dy*dy
	return
}

// roundScaled is mathematical rounding of num*scale/den (den > 0).
func roundScaled(num, den int64) int64 {
	x := num * scale
	if x >= 0 {
		return (x + den/2) / den
	}
	return -((-x + den/2) / den)
}

// Quantize rounds rational canonical coordinates to the fixed integer
// lattice. Template and scene always go through this one function,
// which is what keeps a transformed copy on identical keys.
func Quantize(nu, nv, den int64) (int64, int64) {
	return roundScaled(nu, den), roundScaled(nv, den)
}

// Key is a quantized canonical-coordinate cell.
type Key struct{ U, V int64 }

// KeyOf quantizes p relative to the ordered basis (a, b).
func KeyOf(a, b, p Point) Key {
	nu, nv, den := BasisUV(a, b, p)
	u, v := Quantize(nu, nv, den)
	return Key{u, v}
}
