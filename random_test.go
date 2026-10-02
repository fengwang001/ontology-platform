package ontology

import (
	"fmt"
	"math/rand"
	"testing"
)

type naiveNode struct {
	id        string
	parent    string
	container bool
	depth     int
	aces      []ACE
	protected bool
}

type naiveState struct {
	D       int
	K       int
	version uint64
	nodes   map[string]*naiveNode
}

func newNaive(D, K int) *naiveState {
	return &naiveState{
		D: D,
		K: K,
		nodes: map[string]*naiveNode{
			RootID: {id: RootID, container: true},
		},
	}
}

func copyNaiveACEs(aces []ACE) []ACE {
	result := make([]ACE, len(aces))
	for i, ace := range aces {
		result[i] = ACE{
			Allow:     ace.Allow,
			Principal: append([]byte(nil), ace.Principal...),
			Mask:      ace.Mask,
			Flags:     ace.Flags,
		}
	}
	return result
}

func (n *naiveState) add(id, parent string, container bool) error {
	if id == "" || parent == "" {
		return ErrInvalidArgument
	}
	parentNode, ok := n.nodes[parent]
	if !ok {
		return ErrNotFound
	}
	if _, exists := n.nodes[id]; exists || !parentNode.container {
		return ErrConflict
	}
	if parentNode.depth+1 > n.D {
		return ErrLimitExceeded
	}
	n.nodes[id] = &naiveNode{id: id, parent: parent, container: container, depth: parentNode.depth + 1}
	n.version++
	return nil
}

func (n *naiveState) setACL(id string, aces []ACE, protected bool) error {
	if id == "" {
		return ErrInvalidArgument
	}
	for _, ace := range aces {
		if !validACE(ace) {
			return ErrInvalidArgument
		}
	}
	target, ok := n.nodes[id]
	if !ok {
		return ErrNotFound
	}
	if len(aces) > n.K {
		return ErrLimitExceeded
	}
	target.aces = copyNaiveACEs(aces)
	target.protected = protected
	n.version++
	return nil
}

func (n *naiveState) children(parent string) []string {
	var result []string
	for id, current := range n.nodes {
		if current.parent == parent {
			result = append(result, id)
		}
	}
	return result
}

func (n *naiveState) descendant(ancestor, descendant string) bool {
	for current := descendant; current != ""; current = n.nodes[current].parent {
		if current == ancestor {
			return true
		}
	}
	return false
}

func (n *naiveState) maxDepth(root string) int {
	result := n.nodes[root].depth
	var walk func(string)
	walk = func(id string) {
		current := n.nodes[id]
		if current.depth > result {
			result = current.depth
		}
		for _, child := range n.children(id) {
			walk(child)
		}
	}
	walk(root)
	return result
}

func (n *naiveState) move(id, parent string) error {
	if id == "" || parent == "" {
		return ErrInvalidArgument
	}
	target, ok := n.nodes[id]
	if !ok {
		return ErrNotFound
	}
	destination, parentOK := n.nodes[parent]
	if !parentOK {
		return ErrNotFound
	}
	if id == RootID || !destination.container || parent == id || n.descendant(id, parent) || target.parent == parent {
		return ErrConflict
	}
	if destination.depth+1+(n.maxDepth(id)-target.depth) > n.D {
		return ErrLimitExceeded
	}

	target.parent = parent
	delta := destination.depth + 1 - target.depth
	var update func(string)
	update = func(root string) {
		n.nodes[root].depth += delta
		for _, child := range n.children(root) {
			update(child)
		}
	}
	update(id)
	n.version++
	return nil
}

func (n *naiveState) explicit(id string) []EffectiveACE {
	aces := n.nodes[id].aces
	result := make([]EffectiveACE, 0, len(aces))
	for index, ace := range aces {
		if !ace.Allow {
			result = append(result, EffectiveACE{ACE: ace, Source: []byte(id), Index: index})
		}
	}
	for index, ace := range aces {
		if ace.Allow {
			result = append(result, EffectiveACE{ACE: ace, Source: []byte(id), Index: index})
		}
	}
	return result
}

func (n *naiveState) effectiveRecursive(id string) ([]EffectiveACE, int) {
	if id == RootID || n.nodes[id].protected {
		return n.explicit(id), 1
	}
	parentEffective, visited := n.effectiveRecursive(n.nodes[id].parent)
	result := n.explicit(id)
	for _, ace := range parentEffective {
		flags, ok := inheritFlags(ace.Flags, n.nodes[id].container)
		if !ok {
			continue
		}
		copyACE := ace
		copyACE.Principal = append([]byte(nil), ace.Principal...)
		copyACE.Source = append([]byte(nil), ace.Source...)
		copyACE.Flags = flags
		result = append(result, copyACE)
	}
	return result, visited + 1
}

func naiveEval(aces []EffectiveACE, token map[string]struct{}, node string, request uint16) Decision {
	var granted uint16
	for i, ace := range aces {
		if ace.Flags&FlagIO != 0 {
			continue
		}
		if _, ok := token[string(ace.Principal)]; !ok {
			continue
		}
		if ace.Allow {
			granted |= ace.Mask & request
			if granted == request {
				return Decision{Allowed: true, Result: ResultGrant, DecisiveIndex: i, Source: append([]byte(nil), ace.Source...), Inherited: string(ace.Source) != node, Granted: granted}
			}
		} else if ace.Mask&request&^granted != 0 {
			return Decision{Allowed: false, Result: ResultDenyHit, DecisiveIndex: i, Source: append([]byte(nil), ace.Source...), Inherited: string(ace.Source) != node, Granted: granted}
		}
	}
	return Decision{Allowed: false, Result: ResultImplicitDeny, DecisiveIndex: -1, Granted: granted}
}

func sameEffectiveList(left, right []EffectiveACE) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i].Allow != right[i].Allow || string(left[i].Principal) != string(right[i].Principal) ||
			left[i].Mask != right[i].Mask || left[i].Flags != right[i].Flags ||
			string(left[i].Source) != string(right[i].Source) || left[i].Index != right[i].Index {
			return false
		}
	}
	return true
}

func sameDecision(left, right Decision) bool {
	return left.Allowed == right.Allowed && left.Result == right.Result &&
		left.DecisiveIndex == right.DecisiveIndex && string(left.Source) == string(right.Source) &&
		left.Inherited == right.Inherited && left.Granted == right.Granted
}

func errorCode(err error) string {
	if err == nil {
		return ""
	}
	if wrapped, ok := err.(*Error); ok {
		return wrapped.Kind
	}
	return err.Error()
}

func randomMask(r *rand.Rand) uint16 {
	mask := uint16(1) << uint(r.Intn(16))
	if r.Intn(2) == 0 {
		mask |= uint16(1) << uint(r.Intn(16))
	}
	return mask
}

func mapKeys(values map[string]*naiveNode) []string {
	result := make([]string, 0, len(values))
	for id := range values {
		result = append(result, id)
	}
	return result
}

func TestRandomNaiveComparison(t *testing.T) {
	r := rand.New(rand.NewSource(1181))
	a := mustNewACL(t, 6, 4)
	n := newNaive(6, 4)
	principals := []string{"usr", "grp", "svc", "other"}

	for step := 1; step <= 2000; step++ {
		switch r.Intn(10) {
		case 0, 1, 2, 3:
			var parents []string
			for id, current := range n.nodes {
				if current.container && current.depth < n.D {
					parents = append(parents, id)
				}
			}
			parent := parents[r.Intn(len(parents))]
			id := fmt.Sprintf("n%d", step)
			container := r.Intn(3) != 0
			wantErr := n.add(id, parent, container)
			gotErr := a.AddNode([]byte(id), []byte(parent), container)
			t.Logf("step=%d AddNode id=%q parent=%q container=%v out=%v basis=%s", step, id, parent, container, gotErr == nil, errorCode(gotErr))
			if errorCode(wantErr) != errorCode(gotErr) {
				t.Fatalf("AddNode got=%v want=%v", gotErr, wantErr)
			}
		case 4, 5, 6:
			id := mapKeys(n.nodes)[r.Intn(len(n.nodes))]
			aces := make([]ACE, r.Intn(n.K+1))
			for i := range aces {
				flags := uint8(r.Intn(16))
				if flags&(FlagIO|FlagNP) != 0 && flags&(FlagOI|FlagCI) == 0 {
					flags |= FlagCI
				}
				aces[i] = ACE{
					Allow:     r.Intn(2) == 0,
					Principal: []byte(principals[r.Intn(len(principals))]),
					Mask:      randomMask(r),
					Flags:     flags,
				}
			}
			protected := r.Intn(3) == 0
			wantErr := n.setACL(id, aces, protected)
			gotErr := a.SetACL([]byte(id), aces, protected)
			t.Logf("step=%d SetACL node=%q protected=%v count=%d out=%v basis=%s", step, id, protected, len(aces), gotErr == nil, errorCode(gotErr))
			if errorCode(wantErr) != errorCode(gotErr) {
				t.Fatalf("SetACL got=%v want=%v", gotErr, wantErr)
			}
		case 7:
			var movable []string
			var containers []string
			for id, current := range n.nodes {
				if current.container {
					containers = append(containers, id)
				}
				if id != RootID {
					movable = append(movable, id)
				}
			}
			if len(movable) == 0 {
				continue
			}
			id := movable[r.Intn(len(movable))]
			parent := containers[r.Intn(len(containers))]
			wantErr := n.move(id, parent)
			gotErr := a.Move([]byte(id), []byte(parent))
			t.Logf("step=%d Move node=%q newParent=%q out=%v basis=%s", step, id, parent, gotErr == nil, errorCode(gotErr))
			if errorCode(wantErr) != errorCode(gotErr) {
				t.Fatalf("Move got=%v want=%v", gotErr, wantErr)
			}
		default:
			id := mapKeys(n.nodes)[r.Intn(len(n.nodes))]
			token := make([][]byte, 1+r.Intn(3))
			tokenSet := make(map[string]struct{}, len(token))
			for i := range token {
				principal := principals[r.Intn(len(principals))]
				token[i] = []byte(principal)
				tokenSet[principal] = struct{}{}
			}
			request := randomMask(r)
			naiveList, visited := n.effectiveRecursive(id)
			want := naiveEval(naiveList, tokenSet, id, request)

			beforeEntries := a.evalEntryCount()
			beforeNodes := a.evalNodeCount()
			got, err := a.Eval(token, []byte(id), request)
			entriesDelta := a.evalEntryCount() - beforeEntries
			nodesDelta := a.evalNodeCount() - beforeNodes
			gotList, effectiveErr := a.Effective([]byte(id))
			if err != nil || effectiveErr != nil {
				t.Fatalf("Eval err=%v Effective err=%v", err, effectiveErr)
			}
			t.Logf("step=%d Eval node=%q token=%v R=%016b out=allowed:%v result:%d index:%d source:%q G=%016b basis=processed:%d visited:%d",
				step, id, token, request, got.Allowed, got.Result, got.DecisiveIndex, string(got.Source), got.Granted, entriesDelta, nodesDelta)
			if !sameEffectiveList(gotList, naiveList) {
				t.Fatalf("effective mismatch node=%s got=%#v want=%#v", id, gotList, naiveList)
			}
			if !sameDecision(got, want) {
				t.Fatalf("decision mismatch node=%s got=%#v want=%#v", id, got, want)
			}
			wantEntries := len(naiveList)
			if want.DecisiveIndex >= 0 {
				wantEntries = want.DecisiveIndex + 1
			}
			if int(entriesDelta) != wantEntries || int(nodesDelta) != visited {
				t.Fatalf("counter mismatch got processed=%d visited=%d want processed=%d visited=%d", entriesDelta, nodesDelta, wantEntries, visited)
			}
		}

		if a.Version() != n.version {
			t.Fatalf("version mismatch got=%d want=%d", a.Version(), n.version)
		}
	}
}
