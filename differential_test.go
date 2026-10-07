package ontology

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// auditRecord 是差异测试中每一次判定的审计记录：
// 输入（实例、截止时刻）、所依据的继承规则版本与对照结论。
type auditRecord struct {
	Case         int     `json:"case"`
	Instance     string  `json:"instance"`
	Cutoff       int64   `json:"cutoff"`
	RuleVersions []int64 `json:"rule_versions"`
	Events       int     `json:"events"`
	OptimizedErr string  `json:"optimized_err,omitempty"`
	NaiveErr     string  `json:"naive_err,omitempty"`
	Match        bool    `json:"match"`
}

// auditLogger 将审计记录追加写入 JSONL 文件。
// 目录由 ONTOLOGY_AUDIT_DIR 指定，缺省为测试临时目录。
type auditLogger struct {
	f    *os.File
	enc  *json.Encoder
	path string
}

func newAuditLogger(t *testing.T, name string) *auditLogger {
	t.Helper()
	dir := os.Getenv("ONTOLOGY_AUDIT_DIR")
	if dir == "" {
		dir = t.TempDir()
	}
	path := filepath.Join(dir, name)
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	t.Logf("审计日志: %s", path)
	return &auditLogger{f: f, enc: json.NewEncoder(f), path: path}
}

func (l *auditLogger) log(rec auditRecord) {
	if err := l.enc.Encode(rec); err != nil {
		panic(err)
	}
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// compareRebuild 对同一实例同一截止时刻，比较优化路径（Store.Rebuild）
// 与朴素模型（NaiveReplay）的结果，并落审计记录。
func compareRebuild(t *testing.T, l *auditLogger, caseNo int, s *Store, rules *RuleStore, id string, cutoff int64) {
	t.Helper()
	events, err := s.Events(id)
	if err != nil {
		t.Fatal(err)
	}
	gotState, _, gotErr := s.Rebuild(id, cutoff)
	wantState, wantErr := NaiveReplay(events, cutoff, rules)

	match := reflect.DeepEqual(gotState, wantState) &&
		(gotErr == nil) == (wantErr == nil) &&
		(gotErr == nil || errors.Is(gotErr, wantErr) || gotErr.Error() == wantErr.Error() ||
			errors.Is(gotErr, rootSentinel(wantErr)))

	versions := []int64{}
	for _, v := range rules.Versions() {
		versions = append(versions, v.ValidFrom)
	}
	l.log(auditRecord{
		Case: caseNo, Instance: id, Cutoff: cutoff, RuleVersions: versions,
		Events: len(events), OptimizedErr: errString(gotErr), NaiveErr: errString(wantErr),
		Match: match,
	})
	if gotErr == nil && wantErr == nil {
		if !reflect.DeepEqual(gotState, wantState) {
			t.Fatalf("case %d 实例 %s cutoff %d 状态分叉:\n优化路径 %+v\n朴素模型 %+v",
				caseNo, id, cutoff, gotState, wantState)
		}
		return
	}
	// 两侧都报错时，必须归为同一哨兵错误。
	for _, sentinel := range []error{ErrAmbiguousOrder, ErrCutoffBeforeFirstEvent, ErrUndefinedTargetType, ErrInvalidEvolutionPath} {
		if errors.Is(gotErr, sentinel) != errors.Is(wantErr, sentinel) {
			t.Fatalf("case %d 实例 %s cutoff %d 错误分类分叉: 优化路径 %v / 朴素模型 %v",
				caseNo, id, cutoff, gotErr, wantErr)
		}
	}
}

func rootSentinel(err error) error {
	for _, s := range []error{ErrAmbiguousOrder, ErrCutoffBeforeFirstEvent, ErrUndefinedTargetType, ErrInvalidEvolutionPath} {
		if errors.Is(err, s) {
			return s
		}
	}
	return err
}

// chainRules 三级链规则库：Root score[0,100] -> Mid score[0,50] -> Leaf score[0,10]，
// Leaf 另有新增属性 flag{x,y}。
func chainRules() *RuleStore {
	rs := NewRuleStore()
	err := rs.AddVersion(RuleVersion{ValidFrom: 0, Types: map[string]ObjectType{
		"Root": {ID: "Root", Props: map[string]Range{"score": IntRange{Min: 0, Max: 100}}},
		"Mid":  {ID: "Mid", Parent: "Root", Props: map[string]Range{"score": IntRange{Min: 0, Max: 50}}},
		"Leaf": {ID: "Leaf", Parent: "Mid", Props: map[string]Range{
			"score": IntRange{Min: 0, Max: 10},
			"flag":  StrSet{Allowed: []string{"x", "y"}},
		}},
	}})
	if err != nil {
		panic(err)
	}
	return rs
}

// TestExhaustiveDirectionSwitching 穷举三级链上长度 1..5 的全部类型演变序列
// （含子->父、父->子、跨级、来回切换），每个序列在不同深度插入赋值，
// 并对每个前缀截止时刻比对优化路径与朴素模型。
func TestExhaustiveDirectionSwitching(t *testing.T) {
	rules := chainRules()
	types := []string{"Root", "Mid", "Leaf"}
	values := []int64{5, 30, 80} // 分别落在 Leaf/Mid/Root 的独占区间内
	logger := newAuditLogger(t, "exhaustive_audit.jsonl")
	caseNo := 0

	var enumerate func(prefix []string)
	enumerate = func(prefix []string) {
		if len(prefix) > 0 {
			caseNo++
			s := NewStore(rules)
			id := fmt.Sprintf("inst-%d", caseNo)
			events := []Event{Created(10, prefix[0])}
			tm := int64(10)
			for i, typ := range prefix[1:] {
				tm += 10
				events = append(events, Set(tm, "score", Int(values[i%len(values)])))
				tm += 10
				events = append(events, Set(tm, "flag", Text("x"))) // 非 Leaf 时被忽略
				tm += 10
				events = append(events, Evolve(tm, typ))
			}
			if err := s.ImportEvents(id, events); err != nil {
				t.Fatal(err)
			}
			// 对每个前缀截止时刻做对照。
			for _, ev := range events {
				compareRebuild(t, logger, caseNo, s, rules, id, ev.Time)
			}
			compareRebuild(t, logger, caseNo, s, rules, id, tm+1000)
		}
		if len(prefix) >= 5 {
			return
		}
		for _, typ := range types {
			enumerate(append(append([]string(nil), prefix...), typ))
		}
	}
	enumerate(nil)
	t.Logf("穷举方向切换用例数: %d", caseNo)
}

// TestRandomDifferential 随机操作序列下，优化路径与朴素模型逐条对照。
// 操作包括：随机追加规则版本、创建实例、属性赋值、类型演变、随机截止时刻重建。
func TestRandomDifferential(t *testing.T) {
	rng := rand.New(rand.NewSource(20261007))
	rules := NewRuleStore()
	s := NewStore(rules)
	logger := newAuditLogger(t, "random_audit.jsonl")

	types := []string{"Root", "Mid", "Leaf"}
	props := []string{"score", "flag"}
	// 初始版本。
	if err := rules.AddVersion(RuleVersion{ValidFrom: 0, Types: map[string]ObjectType{
		"Root": {ID: "Root", Props: map[string]Range{"score": IntRange{Min: 0, Max: 100}}},
		"Mid":  {ID: "Mid", Parent: "Root", Props: map[string]Range{"score": IntRange{Min: 0, Max: 50}}},
		"Leaf": {ID: "Leaf", Parent: "Mid", Props: map[string]Range{
			"score": IntRange{Min: 0, Max: 10},
			"flag":  StrSet{Allowed: []string{"x", "y"}},
		}},
	}}); err != nil {
		t.Fatal(err)
	}

	clock := int64(0)
	nextTime := func() int64 {
		clock += 1 + int64(rng.Intn(5))
		return clock
	}
	instances := []string{}
	caseNo := 0

	for op := 0; op < 2000; op++ {
		switch rng.Intn(10) {
		case 0: // 追加新规则版本（随机收窄或保持各层范围）
			lo := int64(rng.Intn(10))
			mid := lo + int64(rng.Intn(40))
			hi := mid + int64(rng.Intn(50))
			err := rules.AddVersion(RuleVersion{ValidFrom: clock + 1000, Types: map[string]ObjectType{
				"Root": {ID: "Root", Props: map[string]Range{"score": IntRange{Min: 0, Max: hi}}},
				"Mid":  {ID: "Mid", Parent: "Root", Props: map[string]Range{"score": IntRange{Min: 0, Max: mid}}},
				"Leaf": {ID: "Leaf", Parent: "Mid", Props: map[string]Range{
					"score": IntRange{Min: 0, Max: lo},
					"flag":  StrSet{Allowed: []string{"x", "y"}},
				}},
			}})
			if err != nil {
				t.Fatalf("随机规则版本不合法: %v", err)
			}
			clock += 1000
		case 1, 2: // 新实例
			id := fmt.Sprintf("inst-%d", len(instances))
			if err := s.Append(id, Created(nextTime(), types[rng.Intn(len(types))])); err != nil {
				t.Fatal(err)
			}
			instances = append(instances, id)
		default: // 对既有实例追加赋值或演变
			if len(instances) == 0 {
				continue
			}
			id := instances[rng.Intn(len(instances))]
			var ev Event
			if rng.Intn(2) == 0 {
				prop := props[rng.Intn(len(props))]
				if prop == "score" {
					ev = Set(nextTime(), prop, Int(int64(rng.Intn(110))))
				} else {
					ev = Set(nextTime(), prop, Text([]string{"x", "y", "z"}[rng.Intn(3)]))
				}
			} else {
				ev = Evolve(nextTime(), types[rng.Intn(len(types))])
			}
			_ = s.Append(id, ev) // 追加失败（越界/路径矛盾等）属于预期行为
		}
		// 周期性做对照重建。
		if len(instances) > 0 && op%7 == 0 {
			caseNo++
			id := instances[rng.Intn(len(instances))]
			cutoff := int64(rng.Int63n(clock + 1))
			compareRebuild(t, logger, caseNo, s, rules, id, cutoff)
		}
	}
	// 终态全量对照。
	for _, id := range instances {
		caseNo++
		compareRebuild(t, logger, caseNo, s, rules, id, clock+100000)
	}
	t.Logf("随机差异对照用例数: %d, 实例数: %d", caseNo, len(instances))
}
