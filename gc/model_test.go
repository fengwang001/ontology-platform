package gc_test

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"testing"

	"ontology/gc"
)

// model is a deliberately naive, recompute-from-scratch implementation of
// the same spec, used as an oracle for the randomized differential test.
type modelImage struct {
	layers   []gc.Layer
	ready    bool
	lastUsed int64
	run      int
}

type model struct {
	capacity int64
	k        int
	maxNow   int64
	images   map[string]*modelImage
}

func newModel(capacity int64, k int) *model {
	return &model{capacity: capacity, k: k, maxNow: -1, images: make(map[string]*modelImage)}
}

func (m *model) layerSizes() map[string]int64 {
	sizes := make(map[string]int64)
	for _, im := range m.images {
		for _, l := range im.layers {
			sizes[l.ID] = l.Size
		}
	}
	return sizes
}

func (m *model) used() int64 {
	var total int64
	for _, s := range m.layerSizes() {
		total += s
	}
	return total
}

func modelValidatePull(img string, layers []gc.Layer, now int64) error {
	if img == "" || len(layers) == 0 || now < 0 {
		return gc.ErrInvalidArgument
	}
	seen := map[string]bool{}
	for _, l := range layers {
		if l.ID == "" || l.Size < 1 || l.Size > 1_000_000_000_000 || seen[l.ID] {
			return gc.ErrInvalidArgument
		}
		seen[l.ID] = true
	}
	return nil
}

func (m *model) checkClock(now int64) error {
	if now < m.maxNow {
		return gc.ErrClockSkew
	}
	return nil
}

func (m *model) beginPull(img string, layers []gc.Layer, now int64) error {
	if _, ok := m.images[img]; ok {
		return gc.ErrImageExists
	}
	existing := m.layerSizes()
	var added int64
	for _, l := range layers {
		if sz, ok := existing[l.ID]; ok {
			if sz != l.Size {
				return &gc.LayerConflictError{LayerID: l.ID}
			}
			continue
		}
		added += l.Size
	}
	if m.used()+added > m.capacity {
		return gc.ErrNoSpace
	}
	cp := make([]gc.Layer, len(layers))
	copy(cp, layers)
	m.images[img] = &modelImage{layers: cp}
	m.maxNow = now
	return nil
}

func (m *model) BeginPull(img string, layers []gc.Layer, now int64) error {
	if err := modelValidatePull(img, layers, now); err != nil {
		return err
	}
	if err := m.checkClock(now); err != nil {
		return err
	}
	return m.beginPull(img, layers, now)
}

func (m *model) Pull(img string, layers []gc.Layer, now int64) error {
	if err := modelValidatePull(img, layers, now); err != nil {
		return err
	}
	if err := m.checkClock(now); err != nil {
		return err
	}
	if err := m.beginPull(img, layers, now); err != nil {
		return err
	}
	im := m.images[img]
	im.ready = true
	im.lastUsed = now
	im.run = 0
	return nil
}

func (m *model) find(img string, now int64) (*modelImage, error) {
	if img == "" || now < 0 {
		return nil, gc.ErrInvalidArgument
	}
	if err := m.checkClock(now); err != nil {
		return nil, err
	}
	im, ok := m.images[img]
	if !ok {
		return nil, gc.ErrImageNotFound
	}
	return im, nil
}

func (m *model) CommitPull(img string, now int64) error {
	im, err := m.find(img, now)
	if err != nil {
		return err
	}
	if im.ready {
		return gc.ErrImageNotPulling
	}
	im.ready = true
	im.lastUsed = now
	im.run = 0
	m.maxNow = now
	return nil
}

func (m *model) AbortPull(img string, now int64) error {
	im, err := m.find(img, now)
	if err != nil {
		return err
	}
	if im.ready {
		return gc.ErrImageNotPulling
	}
	delete(m.images, img)
	m.maxNow = now
	return nil
}

func (m *model) Run(img string, now int64) error {
	im, err := m.find(img, now)
	if err != nil {
		return err
	}
	if !im.ready {
		return gc.ErrImagePulling
	}
	im.run++
	im.lastUsed = now
	m.maxNow = now
	return nil
}

func (m *model) Stop(img string, now int64) error {
	im, err := m.find(img, now)
	if err != nil {
		return err
	}
	if !im.ready {
		return gc.ErrImagePulling
	}
	if im.run == 0 {
		return gc.ErrNotRunning
	}
	im.run--
	im.lastUsed = now
	m.maxNow = now
	return nil
}

func modelRepo(img string) string {
	if i := strings.LastIndex(img, ":"); i >= 0 {
		return img[:i]
	}
	return img
}

func (m *model) GC(now int64, high, low int, minAge int64) (gc.GCResult, error) {
	if now < 0 || low <= 0 || low >= high || high > 100 || minAge < 0 {
		return gc.GCResult{}, gc.ErrInvalidArgument
	}
	if err := m.checkClock(now); err != nil {
		return gc.GCResult{}, err
	}
	m.maxNow = now
	used := m.used()
	if used*100 < int64(high)*m.capacity {
		return gc.GCResult{}, nil
	}
	need := used - m.capacity*int64(low)/100

	protected := map[string]bool{}
	byRepo := map[string][]*modelImage{}
	names := map[*modelImage]string{}
	for id, im := range m.images {
		names[im] = id
		if !im.ready {
			continue
		}
		repo := modelRepo(id)
		byRepo[repo] = append(byRepo[repo], im)
	}
	for _, group := range byRepo {
		sort.Slice(group, func(i, j int) bool {
			if group[i].lastUsed != group[j].lastUsed {
				return group[i].lastUsed > group[j].lastUsed
			}
			return names[group[i]] < names[group[j]]
		})
		for i := 0; i < m.k && i < len(group); i++ {
			protected[names[group[i]]] = true
		}
	}

	var cand []string
	for id, im := range m.images {
		if !im.ready || im.run != 0 || protected[id] || now-im.lastUsed < minAge {
			continue
		}
		cand = append(cand, id)
	}
	sort.Slice(cand, func(i, j int) bool {
		a, b := m.images[cand[i]], m.images[cand[j]]
		if a.lastUsed != b.lastUsed {
			return a.lastUsed < b.lastUsed
		}
		return cand[i] < cand[j]
	})

	res := gc.GCResult{Deleted: []string{}}
	for _, id := range cand {
		before := m.used()
		delete(m.images, id)
		res.Deleted = append(res.Deleted, id)
		res.Freed += before - m.used()
		if res.Freed >= need {
			break
		}
	}
	res.Insufficient = res.Freed < need
	return res, nil
}

func reasonOf(err error) string {
	if err == nil {
		return "ok"
	}
	var lc *gc.LayerConflictError
	if errors.As(err, &lc) {
		return "layer-conflict(" + lc.LayerID + ")"
	}
	switch {
	case errors.Is(err, gc.ErrInvalidArgument):
		return "invalid-argument"
	case errors.Is(err, gc.ErrClockSkew):
		return "clock-skew"
	case errors.Is(err, gc.ErrImageExists):
		return "image-exists"
	case errors.Is(err, gc.ErrImageNotFound):
		return "image-not-found"
	case errors.Is(err, gc.ErrImagePulling):
		return "image-pulling"
	case errors.Is(err, gc.ErrImageNotPulling):
		return "image-not-pulling"
	case errors.Is(err, gc.ErrNoSpace):
		return "no-space"
	case errors.Is(err, gc.ErrNotRunning):
		return "not-running"
	}
	return "unknown: " + err.Error()
}

func TestRandomAgainstModel(t *testing.T) {
	imageIDs := []string{"repoA:1", "repoA:2", "repoA:3", "repoB:1", "repoB:2", "repoC:1", "plain", "z:9"}
	layerIDs := []string{"L0", "L1", "L2", "L3", "L4", "L5"}
	layerSize := map[string]int64{}
	for i, id := range layerIDs {
		layerSize[id] = int64(10 + 7*i)
	}

	for seed := int64(0); seed < 2000; seed++ {
		rng := rand.New(rand.NewSource(seed))
		capacity := int64(50 + rng.Intn(450))
		k := rng.Intn(4)
		r, err := gc.NewReclaimer(capacity, k)
		if err != nil {
			t.Fatal(err)
		}
		m := newModel(capacity, k)

		var clock int64
		var log strings.Builder
		fmt.Fprintf(&log, "seed=%d C=%d K=%d\n", seed, capacity, k)
		failed := false

		check := func(op string, rErr, mErr error) {
			rr, mr := reasonOf(rErr), reasonOf(mErr)
			ru, mu := r.Used(), m.used()
			fmt.Fprintf(&log, "  %s -> impl=%s model=%s used=%d/%d\n", op, rr, mr, ru, mu)
			if rr != mr {
				t.Errorf("seed %d op %q: reason impl=%s model=%s\n%s", seed, op, rr, mr, log.String())
				failed = true
			}
			if ru != mu {
				t.Errorf("seed %d op %q: used impl=%d model=%d\n%s", seed, op, ru, mu, log.String())
				failed = true
			}
		}

		randImage := func() string { return imageIDs[rng.Intn(len(imageIDs))] }
		randLayers := func() []gc.Layer {
			n := 1 + rng.Intn(3)
			var ls []gc.Layer
			for i := 0; i < n; i++ {
				id := layerIDs[rng.Intn(len(layerIDs))]
				size := layerSize[id]
				if rng.Intn(10) == 0 { // occasionally provoke a conflict
					size++
				}
				ls = append(ls, gc.Layer{ID: id, Size: size})
			}
			return ls
		}
		randNow := func() int64 {
			switch rng.Intn(12) {
			case 0:
				return -1
			case 1, 2:
				return clock - int64(rng.Intn(30))
			default:
				clock += int64(rng.Intn(8))
				return clock
			}
		}

		ops := 30 + rng.Intn(30)
		for i := 0; i < ops && !failed; i++ {
			switch rng.Intn(100) {
			case 0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24:
				img, ls, now := randImage(), randLayers(), randNow()
				op := fmt.Sprintf("Pull(%s,%v,%d)", img, ls, now)
				check(op, r.Pull(img, ls, now), m.Pull(img, ls, now))
			case 25, 26, 27, 28, 29, 30, 31, 32, 33, 34, 35, 36, 37, 38, 39:
				img, ls, now := randImage(), randLayers(), randNow()
				op := fmt.Sprintf("BeginPull(%s,%v,%d)", img, ls, now)
				check(op, r.BeginPull(img, ls, now), m.BeginPull(img, ls, now))
			case 40, 41, 42, 43, 44, 45, 46, 47, 48, 49:
				img, now := randImage(), randNow()
				op := fmt.Sprintf("CommitPull(%s,%d)", img, now)
				check(op, r.CommitPull(img, now), m.CommitPull(img, now))
			case 50, 51, 52, 53, 54, 55, 56, 57:
				img, now := randImage(), randNow()
				op := fmt.Sprintf("AbortPull(%s,%d)", img, now)
				check(op, r.AbortPull(img, now), m.AbortPull(img, now))
			case 58, 59, 60, 61, 62, 63, 64, 65, 66, 67, 68, 69:
				img, now := randImage(), randNow()
				op := fmt.Sprintf("Run(%s,%d)", img, now)
				check(op, r.Run(img, now), m.Run(img, now))
			case 70, 71, 72, 73, 74, 75, 76, 77, 78, 79, 80, 81:
				img, now := randImage(), randNow()
				op := fmt.Sprintf("Stop(%s,%d)", img, now)
				check(op, r.Stop(img, now), m.Stop(img, now))
			default:
				now := randNow()
				high := 1 + rng.Intn(110)
				low := rng.Intn(110) - 1
				minAge := int64(rng.Intn(60) - 2)
				op := fmt.Sprintf("GC(%d,%d,%d,%d)", now, high, low, minAge)
				rRes, rErr := r.GC(now, high, low, minAge)
				mRes, mErr := m.GC(now, high, low, minAge)
				check(op, rErr, mErr)
				if failed {
					break
				}
				fmt.Fprintf(&log, "    result impl=%+v model=%+v\n", rRes, mRes)
				if !reflect.DeepEqual(normDeleted(rRes.Deleted), normDeleted(mRes.Deleted)) ||
					rRes.Freed != mRes.Freed || rRes.Insufficient != mRes.Insufficient {
					t.Errorf("seed %d op %q: GC impl=%+v model=%+v\n%s", seed, op, rRes, mRes, log.String())
					failed = true
				}
			}
		}
		if !failed {
			t.Logf("replayed sequence OK (used=%d):\n%s", r.Used(), log.String())
		}
	}
}

func normDeleted(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
