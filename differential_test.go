package ontology

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"
)

type oracleReclaimer struct {
	retention int64
	maxNow    int64
	hasTime   bool
	objects   map[string]oracleObject
	tags      map[string]string
	log       []string
}

type oracleObject struct {
	kind      ObjectKind
	size      int64
	refs      []string
	deadSince int64
	alive     bool
	hasDead   bool
}

type oracleSnapshot struct {
	exists map[string]bool
	live   map[string]bool
	dead   map[string]int64
}

type randomOperation struct {
	kind   int
	now    int64
	digest string
	size   int64
	refs   []string
	name   string
	target string
}

func TestDifferentialRandomSequences(t *testing.T) {
	rng := rand.New(rand.NewSource(20261001))

	for sequence := 0; sequence < 2000; sequence++ {
		retention := int64(rng.Intn(8))
		actual, err := NewReclaimer(retention)
		if err != nil {
			t.Fatal(err)
		}
		oracle := newOracleReclaimer(retention)
		var log strings.Builder
		fmt.Fprintf(&log, "sequence=%d retention=%d\n", sequence, retention)

		now := int64(1)
		for opIndex := 0; opIndex < 30+rng.Intn(30); opIndex++ {
			now += int64(rng.Intn(3))
			op := randomOperation{kind: rng.Intn(8), now: now}
			buildRandomOperation(rng, &op)
			fmt.Fprintf(&log, "op=%d %s\n", opIndex, describeOperation(op))
			oracleLogStart := len(oracle.log)

			actualErr, actualDeleted := applyRandomOperation(actual, op)
			oracleErr, oracleDeleted := oracle.apply(op)
			for _, entry := range oracle.log[oracleLogStart:] {
				fmt.Fprintf(&log, "decision: %s\n", entry)
			}
			fmt.Fprintf(&log, "actual error=%v deleted=%v\noracle error=%v deleted=%v\n", actualErr, actualDeleted, oracleErr, oracleDeleted)
			if fmt.Sprint(actualErr) != fmt.Sprint(oracleErr) {
				t.Fatalf("sequence %d op %d error mismatch\n%s", sequence, opIndex, log.String())
			}
			if !sameStrings(actualDeleted, oracleDeleted) {
				t.Fatalf("sequence %d op %d deletion mismatch\n%sactual=%v\noracle=%v", sequence, opIndex, log.String(), actualDeleted, oracleDeleted)
			}
			compareSnapshots(t, actual, oracle, sequence, opIndex, log.String())
		}

		t.Logf("differential sequence %d:\n%s", sequence, log.String())
	}
}

func newOracleReclaimer(retention int64) *oracleReclaimer {
	return &oracleReclaimer{
		retention: retention,
		objects:   make(map[string]oracleObject),
		tags:      make(map[string]string),
	}
}

func (o *oracleReclaimer) checkTime(now int64) error {
	if o.hasTime && now < o.maxNow {
		return ErrClockSkew
	}
	return nil
}

func (o *oracleReclaimer) accept(now int64) {
	o.hasTime = true
	o.maxNow = now
}

func (o *oracleReclaimer) putLayer(now int64, digest string, size int64) error {
	if err := o.checkTime(now); err != nil {
		o.logf("PutLayer now=%d digest=%q size=%d rejected: %v", now, digest, size, err)
		return err
	}
	if digest == "" {
		err := ErrEmptyDigest
		o.logf("PutLayer now=%d digest=%q size=%d rejected: %v", now, digest, size, err)
		return err
	}
	if size <= 0 {
		err := ErrInvalidSize
		o.logf("PutLayer now=%d digest=%q size=%d rejected: %v", now, digest, size, err)
		return err
	}
	if existing, ok := o.objects[digest]; ok {
		if existing.kind != LayerObject {
			err := ErrObjectKindMismatch
			o.logf("PutLayer now=%d digest=%q size=%d rejected: %v", now, digest, size, err)
			return err
		}
		if existing.size != size {
			err := ErrObjectSizeMismatch
			o.logf("PutLayer now=%d digest=%q size=%d rejected: %v", now, digest, size, err)
			return err
		}
		o.accept(now)
		o.logf("PutLayer now=%d digest=%q size=%d duplicate accepted; deadSince=%d", now, digest, size, existing.deadSince)
		return nil
	}

	o.objects[digest] = oracleObject{kind: LayerObject, size: size, deadSince: now, hasDead: true}
	o.accept(now)
	o.recompute(now)
	o.logf("PutLayer now=%d digest=%q size=%d accepted", now, digest, size)
	return nil
}

func (o *oracleReclaimer) putReferencing(now int64, digest string, refs []string, kind, requiredKind ObjectKind, action string) error {
	if err := o.checkTime(now); err != nil {
		o.logf("%s now=%d digest=%q refs=%v rejected: %v", action, now, digest, refs, err)
		return err
	}
	if digest == "" {
		err := ErrEmptyDigest
		o.logf("%s now=%d digest=%q refs=%v rejected: %v", action, now, digest, refs, err)
		return err
	}
	if _, ok := o.objects[digest]; ok {
		err := ErrDigestExists
		o.logf("%s now=%d digest=%q refs=%v rejected: %v", action, now, digest, refs, err)
		return err
	}
	if len(refs) == 0 {
		err := ErrEmptyReferenceList
		o.logf("%s now=%d digest=%q refs=%v rejected: %v", action, now, digest, refs, err)
		return err
	}

	seen := make(map[string]struct{})
	for _, ref := range refs {
		if _, ok := seen[ref]; ok {
			err := ErrDuplicateReference
			o.logf("%s now=%d digest=%q refs=%v rejected: %v duplicate=%q", action, now, digest, refs, err, ref)
			return err
		}
		seen[ref] = struct{}{}
	}
	for _, ref := range refs {
		if _, ok := o.objects[ref]; !ok {
			err := ErrReferenceNotFound
			o.logf("%s now=%d digest=%q refs=%v rejected: %v missing=%q", action, now, digest, refs, err, ref)
			return err
		}
	}
	for _, ref := range refs {
		if o.objects[ref].kind != requiredKind {
			err := ErrInvalidReferenceKind
			o.logf("%s now=%d digest=%q refs=%v rejected: %v wrong=%q", action, now, digest, refs, err, ref)
			return err
		}
	}

	o.objects[digest] = oracleObject{kind: kind, refs: append([]string(nil), refs...), deadSince: now, hasDead: true}
	o.accept(now)
	o.recompute(now)
	o.logf("%s now=%d digest=%q refs=%v accepted", action, now, digest, refs)
	return nil
}

func (o *oracleReclaimer) tag(now int64, name, target string) error {
	if err := o.checkTime(now); err != nil {
		o.logf("Tag now=%d name=%q target=%q rejected: %v", now, name, target, err)
		return err
	}
	if name == "" {
		err := ErrEmptyTagName
		o.logf("Tag now=%d name=%q target=%q rejected: %v", now, name, target, err)
		return err
	}
	obj, ok := o.objects[target]
	if !ok {
		err := ErrTagTargetNotFound
		o.logf("Tag now=%d name=%q target=%q rejected: %v", now, name, target, err)
		return err
	}
	if obj.kind == LayerObject {
		err := ErrTagTargetIsLayer
		o.logf("Tag now=%d name=%q target=%q rejected: %v", now, name, target, err)
		return err
	}

	o.tags[name] = target
	o.accept(now)
	o.recompute(now)
	o.logf("Tag now=%d name=%q target=%q accepted", now, name, target)
	return nil
}

func (o *oracleReclaimer) untag(now int64, name string) error {
	if err := o.checkTime(now); err != nil {
		o.logf("Untag now=%d name=%q rejected: %v", now, name, err)
		return err
	}
	if name == "" {
		err := ErrEmptyTagName
		o.logf("Untag now=%d name=%q rejected: %v", now, name, err)
		return err
	}
	if _, ok := o.tags[name]; !ok {
		err := ErrTagNotFound
		o.logf("Untag now=%d name=%q rejected: %v", now, name, err)
		return err
	}

	delete(o.tags, name)
	o.accept(now)
	o.recompute(now)
	o.logf("Untag now=%d name=%q accepted", now, name)
	return nil
}

func (o *oracleReclaimer) gc(now int64) ([]string, error) {
	if err := o.checkTime(now); err != nil {
		o.logf("GC now=%d rejected: %v", now, err)
		return nil, err
	}
	o.accept(now)

	live := o.liveSet()
	deleted := make([]string, 0)
	for _, kind := range []ObjectKind{IndexObject, ManifestObject, LayerObject} {
		candidates := make([]string, 0)
		for digest, obj := range o.objects {
			if obj.kind == kind && !live[digest] && now-obj.deadSince >= o.retention {
				candidates = append(candidates, digest)
			}
		}
		sort.Strings(candidates)

		for _, digest := range candidates {
			obj, ok := o.objects[digest]
			if !ok || live[digest] || now-obj.deadSince < o.retention {
				continue
			}

			hasReferrer := false
			for referrer, candidate := range o.objects {
				if candidate.kind == LayerObject {
					continue
				}
				for _, referenced := range candidate.refs {
					if referenced == digest {
						hasReferrer = true
						o.logf("GC now=%d retains digest=%q because referrer=%q exists", now, digest, referrer)
						break
					}
				}
				if hasReferrer {
					break
				}
			}
			if hasReferrer {
				continue
			}

			delete(o.objects, digest)
			deleted = append(deleted, digest)
			o.logf("GC now=%d deletes digest=%q kind=%d deadSince=%d", now, digest, obj.kind, obj.deadSince)
		}
	}
	return deleted, nil
}

func (o *oracleReclaimer) recompute(now int64) {
	live := o.liveSet()
	for digest, obj := range o.objects {
		switch {
		case live[digest]:
			obj.alive = true
			obj.hasDead = false
			obj.deadSince = 0
		case !live[digest] && (obj.alive || !obj.hasDead):
			obj.alive = false
			obj.hasDead = true
			obj.deadSince = now
		}
		o.objects[digest] = obj
	}
}

func (o *oracleReclaimer) liveSet() map[string]bool {
	live := make(map[string]bool)
	queue := make([]string, 0)
	for _, target := range o.tags {
		if obj, ok := o.objects[target]; ok && obj.kind != LayerObject && !live[target] {
			live[target] = true
			queue = append(queue, target)
		}
	}
	for len(queue) > 0 {
		digest := queue[0]
		queue = queue[1:]
		for _, referenced := range o.objects[digest].refs {
			if !live[referenced] {
				live[referenced] = true
				queue = append(queue, referenced)
			}
		}
	}
	return live
}

func (o *oracleReclaimer) snapshot() oracleSnapshot {
	snapshot := oracleSnapshot{
		exists: make(map[string]bool),
		live:   make(map[string]bool),
		dead:   make(map[string]int64),
	}
	for digest, obj := range o.objects {
		snapshot.exists[digest] = true
		snapshot.live[digest] = obj.alive
		if !obj.alive {
			snapshot.dead[digest] = obj.deadSince
		}
	}
	return snapshot
}

func (o *oracleReclaimer) logf(format string, args ...any) {
	o.log = append(o.log, fmt.Sprintf(format, args...))
}

func (o *oracleReclaimer) apply(op randomOperation) (error, []string) {
	switch op.kind {
	case 0:
		return o.putLayer(op.now, op.digest, op.size), nil
	case 1:
		return o.putReferencing(op.now, op.digest, op.refs, ManifestObject, LayerObject, "PutManifest"), nil
	case 2:
		return o.putReferencing(op.now, op.digest, op.refs, IndexObject, ManifestObject, "PutIndex"), nil
	case 3:
		return o.tag(op.now, op.name, op.target), nil
	case 4:
		return o.untag(op.now, op.name), nil
	default:
		deleted, err := o.gc(op.now)
		return err, deleted
	}
}

func buildRandomOperation(rng *rand.Rand, op *randomOperation) {
	if rng.Intn(10) == 0 {
		op.now -= int64(1 + rng.Intn(5))
	}

	switch op.kind {
	case 0:
		op.digest = randomDigest(rng, "layer")
		op.size = int64(1 + rng.Intn(4))
		if rng.Intn(8) == 0 {
			op.digest = ""
		}
		if rng.Intn(8) == 0 {
			op.size = 0
		}
	case 1:
		op.digest = randomDigest(rng, "manifest")
		if rng.Intn(10) == 0 {
			op.digest = ""
		}
		count := 1 + rng.Intn(2)
		if rng.Intn(8) == 0 {
			count = 0
		}
		for i := 0; i < count; i++ {
			op.refs = append(op.refs, randomReference(rng, LayerObject))
		}
		if len(op.refs) > 1 && rng.Intn(5) == 0 {
			op.refs[1] = op.refs[0]
		}
	case 2:
		op.digest = randomDigest(rng, "index")
		if rng.Intn(10) == 0 {
			op.digest = ""
		}
		count := 1 + rng.Intn(2)
		if rng.Intn(8) == 0 {
			count = 0
		}
		for i := 0; i < count; i++ {
			op.refs = append(op.refs, randomReference(rng, ManifestObject))
		}
		if len(op.refs) > 1 && rng.Intn(5) == 0 {
			op.refs[1] = op.refs[0]
		}
	case 3:
		op.name = randomTag(rng)
		op.target = randomTarget(rng)
		if rng.Intn(12) == 0 {
			op.name = ""
		}
	case 4:
		op.name = randomTag(rng)
		if rng.Intn(12) == 0 {
			op.name = ""
		}
	}
}

func describeOperation(op randomOperation) string {
	switch op.kind {
	case 0:
		return fmt.Sprintf("PutLayer(now=%d digest=%q size=%d)", op.now, op.digest, op.size)
	case 1:
		return fmt.Sprintf("PutManifest(now=%d digest=%q layerDigests=%v)", op.now, op.digest, op.refs)
	case 2:
		return fmt.Sprintf("PutIndex(now=%d digest=%q manifestDigests=%v)", op.now, op.digest, op.refs)
	case 3:
		return fmt.Sprintf("Tag(now=%d name=%q target=%q)", op.now, op.name, op.target)
	case 4:
		return fmt.Sprintf("Untag(now=%d name=%q)", op.now, op.name)
	default:
		return fmt.Sprintf("GC(now=%d)", op.now)
	}
}

func applyRandomOperation(r *Reclaimer, op randomOperation) (error, []string) {
	switch op.kind {
	case 0:
		return r.PutLayer(op.now, op.digest, op.size), nil
	case 1:
		return r.PutManifest(op.now, op.digest, op.refs), nil
	case 2:
		return r.PutIndex(op.now, op.digest, op.refs), nil
	case 3:
		return r.Tag(op.now, op.name, op.target), nil
	case 4:
		return r.Untag(op.now, op.name), nil
	default:
		deleted, err := r.GC(op.now)
		return err, deleted
	}
}

func compareSnapshots(t *testing.T, actual *Reclaimer, oracle *oracleReclaimer, sequence, opIndex int, logText string) {
	t.Helper()
	actualState := snapshotActual(actual)
	oracleState := oracle.snapshot()

	for _, digest := range allRandomDigests() {
		if actualState.exists[digest] != oracleState.exists[digest] {
			t.Fatalf("sequence %d op %d digest %q Exists mismatch actual=%v oracle=%v\n%s", sequence, opIndex, digest, actualState.exists[digest], oracleState.exists[digest], logText)
		}
		if actualState.live[digest] != oracleState.live[digest] {
			t.Fatalf("sequence %d op %d digest %q Live mismatch actual=%v oracle=%v\n%s", sequence, opIndex, digest, actualState.live[digest], oracleState.live[digest], logText)
		}
		if actualState.dead[digest] != oracleState.dead[digest] {
			t.Fatalf("sequence %d op %d digest %q deadSince mismatch actual=%d oracle=%d\n%s", sequence, opIndex, digest, actualState.dead[digest], oracleState.dead[digest], logText)
		}
		if actual.Exists(digest) != oracleState.exists[digest] {
			t.Fatalf("sequence %d op %d digest %q public Exists mismatch\n%s", sequence, opIndex, digest, logText)
		}
		if actual.Live(digest) != oracleState.live[digest] {
			t.Fatalf("sequence %d op %d digest %q public Live mismatch\n%s", sequence, opIndex, digest, logText)
		}
	}
}

type actualSnapshot struct {
	exists map[string]bool
	live   map[string]bool
	dead   map[string]int64
}

func snapshotActual(r *Reclaimer) actualSnapshot {
	snapshot := actualSnapshot{
		exists: make(map[string]bool),
		live:   make(map[string]bool),
		dead:   make(map[string]int64),
	}
	for digest, obj := range r.objects {
		snapshot.exists[digest] = true
		snapshot.live[digest] = obj.alive
		if !obj.alive {
			snapshot.dead[digest] = obj.deadSince
		}
	}
	return snapshot
}

func randomDigest(rng *rand.Rand, prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, rng.Intn(6))
}

func randomTag(rng *rand.Rand) string {
	return fmt.Sprintf("tag-%d", rng.Intn(4))
}

func randomTarget(rng *rand.Rand) string {
	switch rng.Intn(4) {
	case 0:
		return randomDigest(rng, "layer")
	case 1:
		return randomDigest(rng, "manifest")
	case 2:
		return randomDigest(rng, "index")
	default:
		return fmt.Sprintf("missing-%d", rng.Intn(4))
	}
}

func randomReference(rng *rand.Rand, expected ObjectKind) string {
	if rng.Intn(5) != 0 {
		if expected == LayerObject {
			return randomDigest(rng, "layer")
		}
		return randomDigest(rng, "manifest")
	}
	switch rng.Intn(3) {
	case 0:
		return randomDigest(rng, "layer")
	case 1:
		return randomDigest(rng, "manifest")
	default:
		return fmt.Sprintf("missing-%d", rng.Intn(4))
	}
}

func allRandomDigests() []string {
	digests := make([]string, 0, 24)
	for _, prefix := range []string{"layer", "manifest", "index", "missing"} {
		for i := 0; i < 6; i++ {
			digests = append(digests, fmt.Sprintf("%s-%d", prefix, i))
		}
	}
	return digests
}
