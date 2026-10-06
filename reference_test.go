package statusengine

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"sync"
	"testing"
)

func TestConcurrentBatchesAndQueriesAreSerializable(t *testing.T) {
	snapshot := map[string][]byte{}
	for i := 0; i < 64; i++ {
		snapshot[batchPath(i)] = []byte("base")
	}
	engine, err := NewEngine(snapshot, nil)
	if err != nil {
		t.Fatal(err)
	}

	var wait sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wait.Add(1)
		go func(worker int) {
			defer wait.Done()
			for round := 0; round < 100; round++ {
				left := (worker*13 + round) % 64
				right := (left + 1) % 64
				paths := []string{batchPath(left), batchPath(right)}
				if err := engine.WriteWorktree(batchPath(left), []byte("work")); err != nil {
					t.Errorf("write: %v", err)
					return
				}
				_ = engine.Stage(paths...)
				_ = engine.Unstage(paths...)
				_ = engine.Discard(true, paths...)
				entries := engine.List()
				for _, entry := range entries {
					if entry.Status == StatusConflict {
						t.Errorf("实际=%s 判定=非冲突", entry.Status)
						return
					}
				}
			}
		}(worker)
	}
	wait.Wait()
	t.Logf("输入=8 workers x 100 rounds 实际=%d entries 判定=无数据竞争且查询始终为完整快照", len(engine.List()))
}

func TestRejectedBatchNeverLeavesPartialState(t *testing.T) {
	engine, err := NewEngine(Snapshot{"a": []byte("base"), "b": []byte("base")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	for worker := 0; worker < 16; worker++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for round := 0; round < 100; round++ {
				_ = engine.WriteWorktree("a", []byte("x"))
				_ = engine.WriteWorktree("b", []byte("y"))
				before := engine.List()
				_ = engine.Stage("a", "missing")
				after := engine.List()
				if len(before) != len(after) {
					t.Errorf("实际=%d 判定=%d", len(after), len(before))
				}
			}
		}()
	}
	wait.Wait()
	t.Log("输入=并发拒绝批次 实际=列表长度稳定 判定=失败调用不可部分生效")
}

func batchPath(index int) string {
	return "p" + string(rune('a'+index%26)) + "/" + string(rune('a'+(index/26)%26)) + string(rune('a'+index%26))
}

type naiveModel struct {
	mu        sync.Mutex
	snapshot  map[string][]byte
	index     map[string][]byte
	worktree  map[string][]byte
	conflicts map[string]Stages
	commitID  int
}

func newNaiveModel(snapshot map[string][]byte) *naiveModel {
	initial := map[string][]byte{}
	for path, content := range snapshot {
		initial[path] = cloneBytes(content)
	}
	return &naiveModel{
		snapshot:  cloneContentMap(initial),
		index:     cloneContentMap(initial),
		worktree:  cloneContentMap(initial),
		conflicts: map[string]Stages{},
	}
}

func cloneContentMap(source map[string][]byte) map[string][]byte {
	result := map[string][]byte{}
	for path, content := range source {
		result[path] = cloneBytes(content)
	}
	return result
}

type naiveOp struct {
	name    string
	paths   []string
	content []byte
	force   bool
	empty   bool
}

func TestRandomSequenceAgainstNaiveModel(t *testing.T) {
	initial := map[string][]byte{"a": []byte("base"), "dir/b": []byte("base"), "c": []byte("base")}
	engine, err := NewEngine(initial, func(path string) bool { return path == "ignored" })
	if err != nil {
		t.Fatal(err)
	}
	reference := newNaiveModel(initial)
	random := rand.New(rand.NewSource(42))
	paths := []string{"a", "dir/b", "c", "ignored", "new", "dir/new", "missing"}

	for step := 0; step < 500; step++ {
		op := randomNaiveOp(random, paths)
		gotErr := engineApply(engine, op)
		wantErr := reference.apply(op)
		t.Logf("step=%d 输入=%s%v 实际错误=%v 判定错误=%v", step, op.name, op.paths, gotErr, wantErr)
		if !sameModelError(gotErr, wantErr) {
			t.Logf("debug snapshot=%v index=%v worktree=%v conflicts=%v", reference.snapshot, reference.index, reference.worktree, reference.conflicts)
			t.Fatalf("step=%d op=%s got=%v want=%v", step, op.name, gotErr, wantErr)
		}
		if !sameModelState(t, engine, reference) {
			t.Fatalf("step=%d state mismatch", step)
		}
	}
}

func randomNaiveOp(random *rand.Rand, paths []string) naiveOp {
	path := paths[random.Intn(len(paths))]
	second := paths[random.Intn(len(paths))]
	switch random.Intn(11) {
	case 0:
		return naiveOp{name: "write", paths: []string{path}, content: []byte(fmt.Sprintf("v%d", random.Intn(4)))}
	case 1:
		return naiveOp{name: "remove", paths: []string{path}}
	case 2:
		return naiveOp{name: "stage", paths: []string{path}}
	case 3:
		return naiveOp{name: "unstage", paths: []string{path}}
	case 4:
		return naiveOp{name: "discard", paths: []string{path}, force: random.Intn(2) == 0}
	case 5:
		return naiveOp{name: "stage-batch", paths: []string{path, second}}
	case 6:
		return naiveOp{name: "unstage-batch", paths: []string{path, second}}
	case 7:
		return naiveOp{name: "discard-batch", paths: []string{path, second}, force: random.Intn(2) == 0}
	case 8:
		return naiveOp{name: "stage-prefix", paths: []string{"dir"}}
	case 9:
		return naiveOp{name: "commit", empty: random.Intn(2) == 0}
	default:
		return naiveOp{name: "merge"}
	}
}

func engineApply(engine *Engine, op naiveOp) error {
	switch op.name {
	case "write":
		return engine.WriteWorktree(op.paths[0], op.content)
	case "remove":
		return engine.RemoveWorktree(op.paths[0])
	case "stage", "stage-batch", "stage-prefix":
		return engine.Stage(op.paths...)
	case "unstage", "unstage-batch":
		return engine.Unstage(op.paths...)
	case "discard", "discard-batch":
		return engine.Discard(op.force, op.paths...)
	case "commit":
		_, err := engine.Commit(op.empty)
		return err
	case "merge":
		return engine.BeginMerge(map[string]Stages{
			"a": {Base: []byte("base"), Ours: []byte("ours"), Theirs: []byte("theirs")},
		})
	default:
		return nil
	}
}

func (m *naiveModel) apply(op naiveOp) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	switch op.name {
	case "write":
		return m.write(op.paths[0], op.content)
	case "remove":
		return m.remove(op.paths[0])
	case "stage", "stage-batch", "stage-prefix":
		return m.stage(op.paths)
	case "unstage", "unstage-batch":
		return m.unstage(op.paths)
	case "discard", "discard-batch":
		return m.discard(op.force, op.paths)
	case "commit":
		return m.commit(op.empty)
	case "merge":
		if len(m.conflicts) > 0 {
			return &PathError{Path: "a", Err: ErrUnresolvedConflicts}
		}
		m.conflicts["a"] = Stages{Base: []byte("base"), Ours: []byte("ours"), Theirs: []byte("theirs")}
		delete(m.index, "a")
		return nil
	default:
		return nil
	}
}

func (m *naiveModel) write(path string, content []byte) error {
	if _, ok := m.normalize(path); !ok {
		return &PathError{Path: path, Err: ErrInvalidPath}
	}
	m.worktree[path] = cloneBytes(content)
	return nil
}

func (m *naiveModel) remove(path string) error {
	if _, ok := m.normalize(path); !ok {
		return &PathError{Path: path, Err: ErrInvalidPath}
	}
	if _, ok := m.worktree[path]; !ok {
		return &PathError{Path: path, Err: ErrPathNotFound}
	}
	delete(m.worktree, path)
	return nil
}

func (m *naiveModel) stage(paths []string) error {
	targets, err := m.expand(paths, expandOptions{worktree: true, index: true, conflicts: true})
	if err != nil {
		return err
	}
	for _, path := range sortedStringsSlice(targets) {
		if content, ok := m.worktree[path]; ok {
			m.index[path] = cloneBytes(content)
		} else {
			if _, indexed := m.index[path]; !indexed {
				if _, conflict := m.conflicts[path]; !conflict {
					errorPath := path
					if len(paths) == 1 {
						errorPath = paths[0]
					}
					return &PathError{Path: errorPath, Err: ErrPathNotFound}
				}
			}
			delete(m.index, path)
		}
		delete(m.conflicts, path)
	}
	return nil
}

func (m *naiveModel) unstage(paths []string) error {
	targets, err := m.expand(paths, expandOptions{snapshot: true, index: true, conflicts: true})
	if err != nil {
		return err
	}
	for _, path := range targets {
		if _, conflict := m.conflicts[path]; conflict {
			return &PathError{Path: path, Err: ErrConflictUnstage}
		}
	}
	for _, path := range targets {
		if content, ok := m.snapshot[path]; ok {
			m.index[path] = cloneBytes(content)
		} else {
			delete(m.index, path)
		}
	}
	return nil
}

func (m *naiveModel) discard(force bool, paths []string) error {
	targets, err := m.expand(paths, expandOptions{worktree: true, index: true, conflicts: true})
	if err != nil {
		return err
	}
	for _, path := range targets {
		_, inSnapshot := m.snapshot[path]
		_, inIndex := m.index[path]
		_, inWorktree := m.worktree[path]
		if !inSnapshot && !inIndex && inWorktree && !force {
			return &PathError{Path: path, Err: ErrUntrackedNeedsForce}
		}
	}
	for _, path := range targets {
		if content, ok := m.index[path]; ok {
			m.worktree[path] = cloneBytes(content)
		} else {
			delete(m.worktree, path)
		}
	}
	return nil
}

func (m *naiveModel) commit(allowEmpty bool) error {
	if len(m.conflicts) > 0 {
		return &PathError{Path: "a", Err: ErrUnresolvedConflicts}
	}
	if !allowEmpty && reflect.DeepEqual(m.snapshot, m.index) {
		return ErrNothingToCommit
	}
	m.snapshot = cloneContentMap(m.index)
	m.commitID++
	return nil
}

func (m *naiveModel) normalize(value string) (string, bool) {
	if value == "" || value == "." || value == ".." || bytes.HasPrefix([]byte(value), []byte("../")) || bytes.Contains([]byte(value), []byte("//")) || bytes.Contains([]byte(value), []byte("/./")) {
		return "", false
	}
	return value, true
}

type expandOptions struct {
	snapshot  bool
	index     bool
	worktree  bool
	conflicts bool
}

func (m *naiveModel) expand(paths []string, options expandOptions) ([]string, error) {
	seenInputs := map[string]struct{}{}
	normalized := []string{}
	for _, path := range paths {
		normal, ok := m.normalize(path)
		if !ok {
			return nil, &PathError{Path: path, Err: ErrInvalidPath}
		}
		if _, duplicate := seenInputs[normal]; duplicate {
			return nil, &PathError{Path: path, Err: ErrInvalidPath}
		}
		seenInputs[normal] = struct{}{}
		normalized = append(normalized, normal)
	}
	result := map[string]struct{}{}
	for _, input := range normalized {
		matched := map[string]struct{}{}
		add := func(source map[string][]byte) {
			for path := range source {
				if path == input || bytes.HasPrefix([]byte(path), []byte(input+"/")) {
					matched[path] = struct{}{}
				}
			}
		}
		if options.worktree {
			add(m.worktree)
		}
		if options.index {
			add(m.index)
		}
		if options.snapshot {
			add(m.snapshot)
		}
		if options.conflicts {
			for path := range m.conflicts {
				if path == input || bytes.HasPrefix([]byte(path), []byte(input+"/")) {
					matched[path] = struct{}{}
				}
			}
		}
		if len(matched) == 0 {
			return nil, &PathError{Path: input, Err: ErrPathNotFound}
		}
		for path := range matched {
			if _, duplicate := result[path]; duplicate {
				return nil, &PathError{Path: path, Err: ErrInvalidPath}
			}
			result[path] = struct{}{}
		}
	}
	return sortedStrings(result), nil
}

func sortedStrings(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func sortedStringsSlice(values []string) []string {
	sort.Strings(values)
	return values
}

func sameModelError(left, right error) bool {
	if left == nil || right == nil {
		return left == right
	}
	for _, sentinel := range []error{ErrInvalidPath, ErrPathNotFound, ErrConflictUnstage, ErrUntrackedNeedsForce, ErrUnresolvedConflicts, ErrNothingToCommit} {
		if errors.Is(left, sentinel) != errors.Is(right, sentinel) {
			return false
		}
	}
	var leftPath *PathError
	var rightPath *PathError
	if errors.As(left, &leftPath) && errors.As(right, &rightPath) {
		return leftPath.Path == rightPath.Path
	}
	return errors.As(left, &leftPath) == errors.As(right, &rightPath)
}

func sameModelState(t *testing.T, engine *Engine, reference *naiveModel) bool {
	t.Helper()
	engine.mu.RLock()
	defer engine.mu.RUnlock()
	reference.mu.Lock()
	defer reference.mu.Unlock()

	engineSnapshot := map[string][]byte{}
	for path, content := range engine.snapshot {
		engineSnapshot[string(path)] = cloneBytes(content)
	}
	engineIndex := map[string][]byte{}
	for path, content := range engine.index {
		engineIndex[string(path)] = cloneBytes(content)
	}
	engineWorktree := map[string][]byte{}
	for path, content := range engine.worktree {
		engineWorktree[string(path)] = cloneBytes(content)
	}
	engineConflicts := map[string]Stages{}
	for path, stages := range engine.conflicts {
		engineConflicts[string(path)] = stages
	}
	if !reflect.DeepEqual(engineSnapshot, reference.snapshot) {
		t.Logf("snapshot 实际=%v 判定=%v", engineSnapshot, reference.snapshot)
	}
	if !reflect.DeepEqual(engineIndex, reference.index) {
		t.Logf("index 实际=%v 判定=%v", engineIndex, reference.index)
	}
	if !reflect.DeepEqual(engineWorktree, reference.worktree) {
		t.Logf("worktree 实际=%v 判定=%v", engineWorktree, reference.worktree)
	}
	if !reflect.DeepEqual(engineConflicts, reference.conflicts) {
		t.Logf("conflicts 实际=%v 判定=%v", engineConflicts, reference.conflicts)
	}
	return reflect.DeepEqual(engineSnapshot, reference.snapshot) &&
		reflect.DeepEqual(engineIndex, reference.index) &&
		reflect.DeepEqual(engineWorktree, reference.worktree) &&
		reflect.DeepEqual(engineConflicts, reference.conflicts) &&
		engine.commitID == reference.commitID
}
