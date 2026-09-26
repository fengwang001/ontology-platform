// Package match builds the template hash table, votes over ordered
// scene bases and verifies with exact integer rationals; the
// oriented perp frame admits no reflection (no conjugate).
package match

import (
	"errors"

	"ontology/gh"
)

type Point = gh.Point

var ErrTooFewPoints = errors.New("match: template needs at least 3 points")
var ErrCollinear = errors.New("match: template points are all collinear")
var ErrSceneTooSmall = errors.New("match: scene has fewer points than template")
var ErrDuplicatePoint = errors.New("match: point set contains a duplicate point")
var ErrOutOfRange = errors.New("match: coordinate outside [-10000,10000]")
var ErrNoTemplate = errors.New("match: no template installed")

const maxCoord = 10000

type frame struct{ nu, nv, den int64 }

// Template is immutable. One ordered basis (0,1) suffices: a frame
// mapping every point to distinct scene points is a full similarity.
type Template struct {
	pts []Point
	tab map[gh.Key][]int // template hash table: key -> point indices
	fr  []frame          // fr[p]: canonical data of point p in basis (0,1)
}

type session struct{ votes int64 } // unexported vote counter, same-package tests only

func validate(pts []Point, tmplSide bool, k int) error {
	if tmplSide && len(pts) < 3 {
		return ErrTooFewPoints
	}
	if !tmplSide && len(pts) < k {
		return ErrSceneTooSmall
	}
	seen := make(map[Point]struct{}, len(pts))
	a := pts[0]
	dx, dy := pts[1].X-a.X, pts[1].Y-a.Y
	coll := tmplSide
	for _, p := range pts {
		if p.X < -maxCoord || p.X > maxCoord || p.Y < -maxCoord || p.Y > maxCoord {
			return ErrOutOfRange
		}
		if _, ok := seen[p]; ok {
			return ErrDuplicatePoint
		}
		seen[p] = struct{}{}
		if (p.X-a.X)*dy-(p.Y-a.Y)*dx != 0 {
			coll = false
		}
	}
	if coll {
		return ErrCollinear
	}
	return nil
}

// NewTemplate fully validates first; a rejection leaves nothing.
func NewTemplate(pts []Point) (*Template, error) {
	if err := validate(pts, true, 0); err != nil {
		return nil, err
	}
	cp := append([]Point(nil), pts...)
	k := len(cp)
	fr, tab := make([]frame, k), make(map[gh.Key][]int, k)
	for p := 0; p < k; p++ {
		nu, nv, den := gh.BasisUV(cp[0], cp[1], cp[p])
		u, v := gh.Quantize(nu, nv, den)
		fr[p] = frame{nu: nu, nv: nv, den: den}
		tab[gh.Key{U: u, V: v}] = append(tab[gh.Key{U: u, V: v}], p)
	}
	return &Template{pts: cp, tab: tab, fr: fr}, nil
}

// VoteBound is the per-run budget: n(n-1) bases times k-2 points,
// O(n^2) for fixed k, never C(n,k).
func VoteBound(k, n int) int64 { return int64(k-2) * int64(n*(n-1)) }

// Match returns the mapping of the globally maximal basis; k-2 is
// the largest possible support, so the first basis reaching it wins.
func (t *Template) Match(scene []Point) ([]int, bool, error) {
	return t.matchIn(&session{}, scene)
}

func (t *Template) matchIn(s *session, scene []Point) ([]int, bool, error) {
	k, n := len(t.pts), len(scene)
	if err := validate(scene, false, k); err != nil {
		return nil, false, err
	}
	at := make(map[Point]int, n) // scene table: point -> index
	for i := range scene {
		at[scene[i]] = i
	}
	for ai := 0; ai < n; ai++ {
		for bi := 0; bi < n; bi++ {
			if bi == ai {
				continue
			}
			m := make([]int, k)
			used := map[int]bool{ai: true, bi: true}
			m[0], m[1] = ai, bi
			sup, rem := 0, k-2
		nextPoint:
			for p := 2; p < k; p++ {
				s.votes++ // project point, query scene table
				id, hit := vote(t.fr[p], p, scene[ai], scene[bi], t.tab, at)
				rem--
				if hit && !used[id] {
					used[id], m[p], sup = true, id, sup+1
				} else if sup+rem < k-2 {
					break nextPoint // cannot reach threshold
				}
			}
			if sup == k-2 {
				return m, true, nil
			}
		}
	}
	return nil, false, nil
}

// vote maps point p through frame (sa,sb): its exact rational image
// must be a scene point whose quantized key is p's template bucket.
func vote(fr frame, p int, sa, sb Point, tab map[gh.Key][]int, at map[Point]int) (int, bool) {
	den, dx, dy := fr.den, sb.X-sa.X, sb.Y-sa.Y
	xn := sa.X*den + fr.nu*dx - fr.nv*dy // perp(d)=(-dy,dx)
	yn := sa.Y*den + fr.nu*dy + fr.nv*dx
	if xn%den != 0 || yn%den != 0 {
		return 0, false
	}
	q := Point{X: xn / den, Y: yn / den}
	id, ok := at[q]
	if !ok {
		return 0, false
	}
	for _, c := range tab[gh.KeyOf(sa, sb, q)] {
		if c == p {
			return id, true
		}
	}
	return 0, false
}
