package retention

import (
	"math/rand"
	"testing"
)

type naiveRecord struct {
	record Record
}

type naiveModel struct {
	commits        map[ID]map[ID]bool
	commitContents map[ID][]ID
	contents       map[ID]Content
	refs           map[string][]naiveRecord
	existed        map[string]bool
	current        map[string]ID
	policy         Policy
	now            int64
}

func newNaiveModel(policy Policy) *naiveModel {
	return &naiveModel{
		commits:        map[ID]map[ID]bool{},
		commitContents: map[ID][]ID{},
		contents:       map[ID]Content{},
		refs:           map[string][]naiveRecord{},
		existed:        map[string]bool{},
		current:        map[string]ID{},
		policy:         policy,
	}
}

func (model *naiveModel) addContent(content Content) {
	model.contents[content.ID] = content
	model.now = max(model.now, content.Written)
}

func (model *naiveModel) addCommit(commit Commit) {
	parents := make(map[ID]bool, len(commit.Parents))
	for _, parent := range commit.Parents {
		parents[parent] = true
	}
	model.commits[commit.ID] = parents
	model.commitContents[commit.ID] = append([]ID(nil), commit.Contents...)
	model.now = max(model.now, commit.Written)
}

func (model *naiveModel) set(name string, commit ID, now int64, operator string) error {
	if model.commits[commit] == nil {
		if now < model.now {
			return ErrClockMovedBack
		}
		return ErrCommitNotFound
	}
	if now < model.now {
		return ErrClockMovedBack
	}
	model.now = now
	model.existed[name] = true
	model.refs[name] = append(model.refs[name], naiveRecord{Record{Old: model.current[name], New: commit, At: now, Operator: operator}})
	model.current[name] = commit
	return nil
}

func (model *naiveModel) delete(name string, now int64, operator string) error {
	if now < model.now {
		return ErrClockMovedBack
	}
	if !model.existed[name] {
		return ErrRefNotFound
	}
	model.now = now
	model.refs[name] = append(model.refs[name], naiveRecord{Record{Old: model.current[name], New: "", At: now, Operator: operator}})
	model.current[name] = ""
	return nil
}

func (model *naiveModel) activeRecords(name string) []naiveRecord {
	current := model.current[name]
	var active []naiveRecord
	for _, entry := range model.refs[name] {
		retention := model.policy.UnreachableRetention
		if current != "" && entry.record.Old != "" && model.ancestor(entry.record.Old, current) {
			retention = model.policy.ReachableRetention
		}
		if model.now-entry.record.At < retention {
			active = append(active, entry)
		}
	}
	return active
}

func (model *naiveModel) expire(now int64) {
	model.now = now
	for name := range model.refs {
		model.refs[name] = model.activeRecords(name)
	}
}

func (model *naiveModel) ancestor(maybeAncestor ID, descendant ID) bool {
	visited := map[ID]bool{}
	frontier := []ID{descendant}
	for len(frontier) > 0 {
		current := frontier[len(frontier)-1]
		frontier = frontier[:len(frontier)-1]
		if current == maybeAncestor {
			return true
		}
		if visited[current] {
			continue
		}
		visited[current] = true
		for parent := range model.commits[current] {
			frontier = append(frontier, parent)
		}
	}
	return false
}

func (model *naiveModel) collect(now int64) GCResult {
	model.now = now
	liveCommits := map[ID]bool{}
	liveContents := map[ID]bool{}
	var frontier []ID
	seed := func(commit ID) {
		if commit != "" && model.commits[commit] != nil && !liveCommits[commit] {
			liveCommits[commit] = true
			frontier = append(frontier, commit)
		}
	}
	for name, current := range model.current {
		seed(current)
		for _, entry := range model.activeRecords(name) {
			seed(entry.record.Old)
			seed(entry.record.New)
		}
	}
	for len(frontier) > 0 {
		commit := frontier[len(frontier)-1]
		frontier = frontier[:len(frontier)-1]
		for _, content := range model.commitContents[commit] {
			liveContents[content] = true
		}
		for parent := range model.commits[commit] {
			seed(parent)
		}
	}
	result := GCResult{}
	for commit := range model.commits {
		if liveCommits[commit] || now-0 < model.policy.FreshGrace {
			continue
		}
		delete(model.commits, commit)
		delete(model.commitContents, commit)
		result.CommitsDeleted++
	}
	for id, content := range model.contents {
		if liveContents[id] || now-content.Written < model.policy.FreshGrace {
			continue
		}
		delete(model.contents, id)
		result.ContentsDeleted++
		result.BytesDeleted += content.Size
	}
	return result
}

func TestRandomOperationsAgainstNaiveModel(t *testing.T) {
	policy := Policy{ReachableRetention: 20, UnreachableRetention: 8, FreshGrace: 0}
	store := newStore(policy)
	model := newNaiveModel(policy)
	commitIDs := []ID{"c0", "c1", "c2", "c3"}
	for index, id := range commitIDs {
		contentID := ID("x" + string(rune('0'+index)))
		store.AddContent(Content{ID: contentID, Size: int64(index + 1), Written: 0})
		model.addContent(Content{ID: contentID, Size: int64(index + 1), Written: 0})
		var parents []ID
		if index > 0 && index%2 == 0 {
			parents = []ID{commitIDs[index-2]}
		}
		commit := Commit{ID: id, Parents: parents, Contents: []ID{contentID}, Created: int64(index), Written: 0}
		if err := store.AddCommit(commit); err != nil {
			t.Fatal(err)
		}
		model.addCommit(commit)
	}
	random := rand.New(rand.NewSource(1516))
	now := int64(1)
	referenceNames := []string{"a", "b"}
	for step := 0; step < 300; step++ {
		name := referenceNames[random.Intn(len(referenceNames))]
		commit := commitIDs[random.Intn(len(commitIDs))]
		var storeErr error
		var modelErr error
		operation := random.Intn(6)
		switch operation {
		case 0, 1:
			storeErr = store.Set(name, commit, now, "op")
			modelErr = model.set(name, commit, now, "op")
		case 2:
			storeErr = store.Delete(name, now, "op")
			modelErr = model.delete(name, now, "op")
		case 3:
			storeErr = store.Expire(now)
			model.expire(now)
		case 4:
			got, err := store.Collect(now)
			storeErr = err
			want := model.collect(now)
			t.Logf("input random step=%d Collect(now=%d); actual=%+v want=%+v; criterion: naive reachability and log expiry match", step, now, got, want)
			if err != nil || got.CommitsDeleted != want.CommitsDeleted {
				t.Fatalf("actual %+v,%v; want %+v", got, err, want)
			}
		case 5:
			got, err := store.ExpireAndCollect(now)
			storeErr = err
			model.expire(now)
			want := model.collect(now)
			t.Logf("input random step=%d ExpireAndCollect(now=%d); actual=%+v want=%+v; criterion: expire-before-collect serial equivalence", step, now, got, want)
			if err != nil || got.CommitsDeleted != want.CommitsDeleted {
				t.Fatalf("actual %+v,%v; want %+v", got, err, want)
			}
		}
		if (storeErr == nil) != (modelErr == nil) {
			t.Fatalf("step=%d operation=%d now=%d actual err=%v naive err=%v", step, operation, now, storeErr, modelErr)
		}
		for ref := range model.existed {
			wantLogs := model.refs[ref]
			for index := 1; index <= len(wantLogs)+1; index++ {
				actual, actualErr := store.History(ref, index)
				if index <= len(wantLogs) {
					want := wantLogs[len(wantLogs)-index].record
					if actualErr != nil || actual != want {
						t.Fatalf("step=%d ref=%s index=%d actual=%+v,%v want=%+v", step, ref, index, actual, actualErr, want)
					}
				} else if actualErr != ErrRecordNotFound {
					t.Fatalf("step=%d ref=%s extra index=%d actual=%+v,%v", step, ref, index, actual, actualErr)
				}
			}
			if store.RefExisted(ref) != model.existed[ref] {
				t.Fatalf("step=%d ref=%s existence mismatch", step, ref)
			}
		}
		for _, ref := range referenceNames {
			if _, actualErr := store.History(ref, 1); model.existed[ref] {
				continue
			} else if actualErr != ErrRefNotFound {
				t.Fatalf("step=%d never-existed ref=%s actual=%v want ref-not-found", step, ref, actualErr)
			}
		}
		if random.Intn(3) == 0 {
			now += int64(random.Intn(9))
		} else {
			now++
		}
	}
}
