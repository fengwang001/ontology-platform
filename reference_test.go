package ontology

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"testing"
)

type refID struct {
	kind  byte
	left  *refID
	right *refID
}

type refEvent struct {
	kind  byte
	n     int
	left  *refEvent
	right *refEvent
}

type refStamp struct {
	id    *refID
	event *refEvent
}

type refReference struct {
	replicas map[string]refStamp
}

const (
	refZero = 0
	refOne  = 1
	refPair = 2
)

var (
	refZeroID = &refID{kind: refZero}
	refOneID  = &refID{kind: refOne}
	refZeroEv = &refEvent{kind: refZero, n: 0}

	refErrEmpty   = fmt.Errorf("empty name")
	refErrExists  = fmt.Errorf("exists")
	refErrUnknown = fmt.Errorf("unknown")
	refErrSame    = fmt.Errorf("same")
	refErrOverlap = fmt.Errorf("overlap")
)

func TestRandomSequencesAgainstNaiveReference(t *testing.T) {
	random := rand.New(rand.NewPCG(1081, 4242))
	for iteration := range 2000 {
		registry := NewRegistry()
		reference := newReference()

		seedName := fmt.Sprintf("root-%d", iteration)
		got, gotErr := registry.Seed(seedName)
		want, wantErr := reference.seed(seedName)
		t.Logf("input=Seed(%q) output=%q basis=%q decision=initial stamp must be (1;0)", seedName, got, want)
		assertRefResult(t, got, gotErr, want, wantErr)

		names := []string{"", seedName, "a", "b", "c", "ghost"}
		for range 12 + random.IntN(20) {
			switch random.IntN(6) {
			case 0:
				name := ""
				got, gotErr := registry.Seed(name)
				want, wantErr := reference.seed(name)
				t.Logf("input=Seed(%q) output=%q error=%v basis=%q basis-error=%v decision=compare error class and stamp", name, got, gotErr, want, wantErr)
				assertRefResult(t, got, gotErr, want, wantErr)
			case 1:
				name := names[random.IntN(len(names))]
				child := names[random.IntN(len(names))]
				gotFirst, gotSecond, gotErr := registry.Fork(name, child)
				wantFirst, wantSecond, wantErr := reference.fork(name, child)
				gotOutput := gotFirst + "|" + gotSecond
				wantOutput := wantFirst + "|" + wantSecond
				t.Logf("input=Fork(%q,%q) output=%q error=%v basis=%q basis-error=%v decision=compare both stamps and error class", name, child, gotOutput, gotErr, wantOutput, wantErr)
				assertRefResult(t, gotOutput, gotErr, wantOutput, wantErr)
			case 2:
				name := names[random.IntN(len(names))]
				got, gotErr := registry.Event(name)
				want, wantErr := reference.event(name)
				t.Logf("input=Event(%q) output=%q error=%v basis=%q basis-error=%v decision=fill when changed otherwise grow", name, got, gotErr, want, wantErr)
				assertRefResult(t, got, gotErr, want, wantErr)
			case 3:
				name := names[random.IntN(len(names))]
				got, gotErr := registry.Peek(name)
				want, wantErr := reference.peek(name)
				t.Logf("input=Peek(%q) output=%q error=%v basis=%q basis-error=%v decision=snapshot id is zero and event tree only", name, got, gotErr, want, wantErr)
				assertRefResult(t, got, gotErr, want, wantErr)
			case 4:
				name := names[random.IntN(len(names))]
				other := names[random.IntN(len(names))]
				got, gotErr := registry.Join(name, other)
				want, wantErr := reference.join(name, other)
				t.Logf("input=Join(%q,%q) output=%q error=%v basis=%q basis-error=%v decision=sum ids and join events only when disjoint", name, other, got, gotErr, want, wantErr)
				assertRefResult(t, got, gotErr, want, wantErr)
			case 5:
				name := names[random.IntN(len(names))]
				other := names[random.IntN(len(names))]
				got, gotErr := registry.Compare(name, other)
				want, wantErr := reference.compare(name, other)
				gotOutput := string(got)
				wantOutput := string(want)
				t.Logf("input=Compare(%q,%q) output=%q error=%v basis=%q basis-error=%v decision=two event leq checks", name, other, gotOutput, gotErr, wantOutput, wantErr)
				assertRefResult(t, gotOutput, gotErr, wantOutput, wantErr)
			}
		}

		assertReferenceIdentityInvariant(t, reference)
	}
}

func assertReferenceIdentityInvariant(t *testing.T, reference *refReference) {
	t.Helper()
	alive := make([]refStamp, 0, len(reference.replicas))
	for _, stamp := range reference.replicas {
		alive = append(alive, stamp)
	}
	total := refZeroID
	for _, stamp := range alive {
		merged, ok := refSum(total, stamp.id)
		if !ok {
			t.Fatal("alive identities overlap before summing to one")
		}
		total = merged
	}
	if total.kind != refOne {
		t.Fatalf("alive identities do not sum to one, total=%s", refIDString(total))
	}
	for i := range alive {
		for j := i + 1; j < len(alive); j++ {
			if _, ok := refSum(alive[i].id, alive[j].id); !ok {
				t.Fatalf("overlapping identities %s and %s", refIDString(alive[i].id), refIDString(alive[j].id))
			}
		}
	}
}

func newReference() *refReference {
	return &refReference{replicas: map[string]refStamp{}}
}

func refPairID(left, right *refID) *refID {
	if left.kind == refZero && right.kind == refZero {
		return refZeroID
	}
	if left.kind == refOne && right.kind == refOne {
		return refOneID
	}
	return &refID{kind: refPair, left: left, right: right}
}

func refScalar(n int) *refEvent {
	return &refEvent{kind: refZero, n: n}
}

func refNode(n int, left, right *refEvent) *refEvent {
	return &refEvent{kind: refPair, n: n, left: left, right: right}
}

func refFork(id *refID) (*refID, *refID) {
	switch id.kind {
	case refZero:
		return refZeroID, refZeroID
	case refOne:
		return refPairID(refOneID, refZeroID), refPairID(refZeroID, refOneID)
	case refPair:
		if id.left.kind == refZero {
			first, second := refFork(id.right)
			return refPairID(refZeroID, first), refPairID(refZeroID, second)
		}
		if id.right.kind == refZero {
			first, second := refFork(id.left)
			return refPairID(first, refZeroID), refPairID(second, refZeroID)
		}
		return refPairID(id.left, refZeroID), refPairID(refZeroID, id.right)
	}
	panic("bad reference id")
}

func refSum(left, right *refID) (*refID, bool) {
	if left.kind == refZero {
		return right, true
	}
	if right.kind == refZero {
		return left, true
	}
	if left.kind == refPair && right.kind == refPair {
		sumLeft, ok := refSum(left.left, right.left)
		if !ok {
			return nil, false
		}
		sumRight, ok := refSum(left.right, right.right)
		if !ok {
			return nil, false
		}
		return refPairID(sumLeft, sumRight), true
	}
	return nil, false
}

func refMin(event *refEvent) int {
	if event.kind == refZero {
		return event.n
	}
	left := refMin(event.left)
	right := refMin(event.right)
	return event.n + min(left, right)
}

func refMax(event *refEvent) int {
	if event.kind == refZero {
		return event.n
	}
	left := refMax(event.left)
	right := refMax(event.right)
	return event.n + max(left, right)
}

func refLift(amount int, event *refEvent) *refEvent {
	if event.kind == refZero {
		return refScalar(event.n + amount)
	}
	return refNode(event.n+amount, event.left, event.right)
}

func refNorm(n int, left, right *refEvent) *refEvent {
	if left.kind == refZero && right.kind == refZero && left.n == right.n {
		return refScalar(n + left.n)
	}
	offset := min(refMin(left), refMin(right))
	return refNode(n+offset, refLift(-offset, left), refLift(-offset, right))
}

func refFill(id *refID, event *refEvent) *refEvent {
	if id.kind == refZero || event.kind == refZero {
		return event
	}
	if id.kind == refOne {
		return refScalar(refMax(event))
	}
	if id.left.kind == refOne {
		right := refFill(id.right, event.right)
		return refNorm(event.n, refScalar(max(refMax(event.left), refMin(right))), right)
	}
	if id.right.kind == refOne {
		left := refFill(id.left, event.left)
		return refNorm(event.n, left, refScalar(max(refMax(event.right), refMin(left))))
	}
	return refNorm(event.n, refFill(id.left, event.left), refFill(id.right, event.right))
}

func refGrow(id *refID, event *refEvent) (*refEvent, int) {
	if id.kind == refOne && event.kind == refZero {
		return refScalar(event.n + 1), 0
	}
	if event.kind == refZero {
		grown, cost := refGrow(id, refNode(event.n, refZeroEv, refZeroEv))
		return grown, cost + 1000000
	}
	if id.left.kind == refZero {
		right, cost := refGrow(id.right, event.right)
		return refNorm(event.n, event.left, right), cost + 1
	}
	if id.right.kind == refZero {
		left, cost := refGrow(id.left, event.left)
		return refNorm(event.n, left, event.right), cost + 1
	}
	left, leftCost := refGrow(id.left, event.left)
	right, rightCost := refGrow(id.right, event.right)
	if leftCost < rightCost {
		return refNorm(event.n, left, event.right), leftCost + 1
	}
	return refNorm(event.n, event.left, right), rightCost + 1
}

func refAsNode(event *refEvent) *refEvent {
	if event.kind == refPair {
		return event
	}
	return refNode(event.n, refZeroEv, refZeroEv)
}

func refJoin(left, right *refEvent) *refEvent {
	if left.kind == refZero && right.kind == refZero {
		return refScalar(max(left.n, right.n))
	}
	leftNode := refAsNode(left)
	rightNode := refAsNode(right)
	if leftNode.n > rightNode.n {
		leftNode, rightNode = rightNode, leftNode
	}
	offset := rightNode.n - leftNode.n
	return refNorm(
		leftNode.n,
		refJoin(leftNode.left, refLift(offset, rightNode.left)),
		refJoin(leftNode.right, refLift(offset, rightNode.right)),
	)
}

func refLE(left, right *refEvent) bool {
	if left.kind == refZero && right.kind == refZero {
		return left.n <= right.n
	}
	if left.kind == refZero {
		return left.n <= right.n
	}
	if right.kind == refZero {
		return left.n <= right.n &&
			refLE(refLift(left.n, left.left), refScalar(right.n)) &&
			refLE(refLift(left.n, left.right), refScalar(right.n))
	}
	return left.n <= right.n &&
		refLE(refLift(left.n, left.left), refLift(right.n, right.left)) &&
		refLE(refLift(left.n, left.right), refLift(right.n, right.right))
}

func refIDString(id *refID) string {
	switch id.kind {
	case refZero:
		return "0"
	case refOne:
		return "1"
	case refPair:
		return "(" + refIDString(id.left) + "," + refIDString(id.right) + ")"
	}
	panic("bad reference id")
}

func refEventString(event *refEvent) string {
	if event.kind == refZero {
		return fmt.Sprint(event.n)
	}
	return fmt.Sprintf("(%d,%s,%s)", event.n, refEventString(event.left), refEventString(event.right))
}

func refStampString(stamp refStamp) string {
	return "(" + refIDString(stamp.id) + ";" + refEventString(stamp.event) + ")"
}

func assertRefResult(t *testing.T, got string, gotErr error, want string, wantErr error) {
	t.Helper()
	if got != want || refErrorKind(gotErr) != refErrorKind(wantErr) {
		t.Fatalf("mismatch got=(%q,%v) want=(%q,%v)", got, gotErr, want, wantErr)
	}
}

func refErrorKind(err error) string {
	if err == nil {
		return "<nil>"
	}
	switch {
	case errors.Is(err, ErrEmptyName):
		return refErrEmpty.Error()
	case errors.Is(err, ErrReplicaExists):
		return refErrExists.Error()
	case errors.Is(err, ErrUnknownReplica):
		return refErrUnknown.Error()
	case errors.Is(err, ErrSameReplica):
		return refErrSame.Error()
	case errors.Is(err, ErrIdentityOverlap):
		return refErrOverlap.Error()
	}
	return fmt.Sprint(err)
}

func (r *refReference) seed(name string) (string, error) {
	if name == "" {
		return "", refErrEmpty
	}
	if _, ok := r.replicas[name]; ok {
		return "", refErrExists
	}
	stamp := refStamp{id: refOneID, event: refScalar(0)}
	r.replicas[name] = stamp
	return refStampString(stamp), nil
}

func (r *refReference) fork(name, child string) (string, string, error) {
	if name == "" || child == "" {
		return "", "", refErrEmpty
	}
	parent, ok := r.replicas[name]
	if !ok {
		return "", "", refErrUnknown
	}
	if _, ok := r.replicas[child]; ok {
		return "", "", refErrExists
	}
	firstID, secondID := refFork(parent.id)
	first := refStamp{id: firstID, event: parent.event}
	second := refStamp{id: secondID, event: parent.event}
	r.replicas[name] = first
	r.replicas[child] = second
	return refStampString(first), refStampString(second), nil
}

func (r *refReference) event(name string) (string, error) {
	if name == "" {
		return "", refErrEmpty
	}
	current, ok := r.replicas[name]
	if !ok {
		return "", refErrUnknown
	}
	filled := refFill(current.id, current.event)
	if refEventString(filled) != refEventString(current.event) {
		current.event = filled
	} else {
		current.event, _ = refGrow(current.id, current.event)
	}
	r.replicas[name] = current
	return refStampString(current), nil
}

func (r *refReference) peek(name string) (string, error) {
	if name == "" {
		return "", refErrEmpty
	}
	current, ok := r.replicas[name]
	if !ok {
		return "", refErrUnknown
	}
	return refStampString(refStamp{id: refZeroID, event: current.event}), nil
}

func (r *refReference) join(name, other string) (string, error) {
	if name == "" || other == "" {
		return "", refErrEmpty
	}
	if name == other {
		return "", refErrSame
	}
	first, ok := r.replicas[name]
	if !ok {
		return "", refErrUnknown
	}
	second, ok := r.replicas[other]
	if !ok {
		return "", refErrUnknown
	}
	id, ok := refSum(first.id, second.id)
	if !ok {
		return "", refErrOverlap
	}
	merged := refStamp{id: id, event: refJoin(first.event, second.event)}
	r.replicas[name] = merged
	delete(r.replicas, other)
	return refStampString(merged), nil
}

func (r *refReference) compare(name, other string) (Comparison, error) {
	if name == "" || other == "" {
		return "", refErrEmpty
	}
	first, ok := r.replicas[name]
	if !ok {
		return "", refErrUnknown
	}
	second, ok := r.replicas[other]
	if !ok {
		return "", refErrUnknown
	}
	firstLE := refLE(first.event, second.event)
	secondLE := refLE(second.event, first.event)
	switch {
	case firstLE && secondLE:
		return Equal, nil
	case firstLE:
		return Before, nil
	case secondLE:
		return After, nil
	default:
		return Concurrent, nil
	}
}
