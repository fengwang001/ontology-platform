// Package api is the public entry point: install one template, then
// match scenes against it. All state is in-process memory and the
// installed template is replaced atomically, so Match and
// SelfCheck are safe for concurrent goroutines.
package api

import (
	"errors"
	"fmt"
	"sync/atomic"

	"ontology/gh"
	"ontology/match"
)

// Point is the integer-lattice point type used by the API.
type Point = match.Point

func pt(x, y int64) Point { return Point{X: x, Y: y} }

// Judgeable sentinel rejections (pairwise distinct).
var (
	ErrTooFewPoints   = match.ErrTooFewPoints
	ErrCollinear      = match.ErrCollinear
	ErrSceneTooSmall  = match.ErrSceneTooSmall
	ErrDuplicatePoint = match.ErrDuplicatePoint
	ErrOutOfRange     = match.ErrOutOfRange
	ErrNoTemplate     = match.ErrNoTemplate
	ErrSelfCheck      = errors.New("api: self-check failed")
)

var installed atomic.Pointer[match.Template]

// NewTemplate validates pts fully before installing; a rejected
// template leaves the previously installed one untouched.
func NewTemplate(pts []Point) error {
	t, err := match.NewTemplate(pts)
	if err != nil {
		return err
	}
	installed.Store(t)
	return nil
}

// Match searches the scene for the installed template; it is a
// read-only operation and may run concurrently.
func Match(scene []Point) (mapping []int, found bool, err error) {
	t := installed.Load()
	if t == nil {
		return nil, false, ErrNoTemplate
	}
	return t.Match(scene)
}

func fail(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrSelfCheck, fmt.Sprintf(format, a...))
}

func identity(m []int) bool {
	for i, x := range m {
		if x != i {
			return false
		}
	}
	return true
}

// SelfCheck verifies the four invariants on built-in pairs; it uses
// only local templates and never mutates installed state.
func SelfCheck() error {
	sq := []Point{pt(0, 0), pt(2, 0), pt(2, 2), pt(0, 2)}
	want := []gh.Key{{U: 0, V: 0}, {U: 16, V: 0}, {U: 16, V: 16}, {U: 0, V: 16}}
	rot := []Point{pt(5, 5), pt(5, 7), pt(3, 7), pt(3, 5)} // R90 + (5,5), index-aligned
	for i := range sq {
		if gh.KeyOf(sq[0], sq[1], sq[i]) != want[i] {
			return fail("canonical key %d of template", i)
		}
		if gh.KeyOf(rot[0], rot[1], rot[i]) != want[i] {
			return fail("canonical key %d of rotated scene", i)
		}
	}
	t, err := match.NewTemplate(sq)
	if err != nil {
		return fail("template: %v", err)
	}
	if m, found, err := t.Match(rot); err != nil || !found || !identity(m) {
		return fail("rotated scene must match index-aligned")
	}
	sim := []Point{pt(20, 20), pt(20, 26), pt(14, 26), pt(14, 20)} // 3*R90 + (20,20)
	if m, found, _ := t.Match(sim); !found || !identity(m) {
		return fail("similarity-transformed scene must match")
	}
	// Mirror rejection needs a chiral set: the scalene triangle has
	// three distinct side lengths, forcing the point correspondence,
	// so its reflected copy cannot be superposed by rotation alone.
	chiral := []Point{pt(0, 0), pt(4, 0), pt(1, 3)}
	ct, _ := match.NewTemplate(chiral)
	mir := []Point{pt(5, 5), pt(9, 5), pt(6, 2)} // reflect across x-axis, then +(5,5)
	if _, found, _ := ct.Match(mir); found {
		return fail("mirror copy must not match")
	}
	tri := []Point{pt(0, 0), pt(4, 0), pt(0, 4)}
	tt, _ := match.NewTemplate(tri)
	noise := []Point{pt(-9, -9), pt(1, 8), pt(8, 1)} // isosceles but not similar to tri
	if _, found, _ := tt.Match(noise); found {
		return fail("noise-only scene must not match")
	}
	noisy := append(append([]Point{}, noise...), pt(12, 12), pt(12, 16), pt(8, 12))
	if _, found, _ := tt.Match(noisy); !found {
		return fail("embedded copy plus noise must match")
	}
	return checkRejections()
}

// checkRejections exercises the five distinct failure injections on
// local instances and confirms a rejection leaves no partial state.
func checkRejections() error {
	cases := []struct {
		name string
		pts  []Point
		want error
	}{
		{"too few", []Point{pt(0, 0), pt(1, 1)}, ErrTooFewPoints},
		{"collinear", []Point{pt(0, 0), pt(1, 1), pt(2, 2)}, ErrCollinear},
		{"duplicate", []Point{pt(0, 0), pt(1, 0), pt(0, 1), pt(0, 0)}, ErrDuplicatePoint},
		{"out of range", []Point{pt(0, 0), pt(1, 0), pt(0, 10001)}, ErrOutOfRange},
	}
	for _, c := range cases {
		if _, err := match.NewTemplate(c.pts); !errors.Is(err, c.want) {
			return fail("%s: got %v", c.name, err)
		}
	}
	t, _ := match.NewTemplate([]Point{pt(0, 0), pt(1, 0), pt(0, 1)})
	if _, _, err := t.Match([]Point{pt(0, 0), pt(1, 0)}); !errors.Is(err, ErrSceneTooSmall) {
		return fail("scene too small: got %v", err)
	}
	if m, found, err := t.Match([]Point{pt(0, 0), pt(1, 0), pt(0, 1)}); err != nil || !found || !identity(m) {
		return fail("template unusable after rejections")
	}
	return nil
}
