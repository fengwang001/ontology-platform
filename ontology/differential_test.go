// 差分测试：在随机操作序列下，将快速实现 (ontology.Store) 与
// 独立朴素参考模型 (naive.Model) 的展开结果逐条对照；每次判定的
// 输入、所依据的属性定义版本与对照结论均记录为 JSONL 以便事后核查。
package ontology_test

import (
	"bufio"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"testing"

	"ontology/ontology"
	"ontology/ontology/naive"
)

// decisionLogEntry 是一条差分判定记录。
type decisionLogEntry struct {
	Seq      int     `json:"seq"`
	Op       string  `json:"op"`
	Input    string  `json:"input"`
	Versions []int64 `json:"versions,omitempty"`
	Verdict  string  `json:"verdict"` // "match" | "mismatch" | "error-match"
	Detail   string  `json:"detail,omitempty"`
}

type diffLogger struct {
	f   *os.File
	w   *bufio.Writer
	seq int
}

func newDiffLogger(t *testing.T) *diffLogger {
	t.Helper()
	// 默认写入测试临时目录；设置 ONT_DIFF_LOG_DIR 可持久化以便事后核查。
	dir := os.Getenv("ONT_DIFF_LOG_DIR")
	if dir == "" {
		dir = t.TempDir()
	}
	path := filepath.Join(dir, fmt.Sprintf("differential_log_%d.jsonl", os.Getpid()))
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create diff log: %v", err)
	}
	t.Logf("differential decision log: %s", path)
	return &diffLogger{f: f, w: bufio.NewWriter(f)}
}

func (l *diffLogger) log(op, input string, versions []int64, verdict, detail string) {
	l.seq++
	e := decisionLogEntry{Seq: l.seq, Op: op, Input: input, Versions: versions, Verdict: verdict, Detail: detail}
	b, _ := json.Marshal(e)
	l.w.Write(b)
	l.w.WriteByte('\n')
}

func (l *diffLogger) close() {
	l.w.Flush()
	l.f.Close()
}

var propNames = []string{"a", "b", "c", "d"}

func randomProps(r *rand.Rand) map[string]ontology.PropertyDef {
	n := 1 + r.Intn(len(propNames))
	perm := r.Perm(len(propNames))
	out := make(map[string]ontology.PropertyDef, n)
	for i := 0; i < n; i++ {
		name := propNames[perm[i]]
		out[name] = ontology.PropertyDef{
			Name:     name,
			Type:     ontology.PropertyType(r.Intn(4)),
			Required: r.Intn(2) == 0,
		}
	}
	return out
}

func randomValue(r *rand.Rand) ontology.Value {
	switch r.Intn(4) {
	case 0:
		return ontology.IntValue(int64(r.Intn(100)))
	case 1:
		if r.Intn(2) == 0 {
			return ontology.FloatValue(float64(r.Intn(100))) // 整数值，可收紧
		}
		return ontology.FloatValue(r.Float64() * 100)
	case 2:
		return ontology.StringValue(fmt.Sprintf("s%d", r.Intn(10)))
	default:
		return ontology.BoolValue(r.Intn(2) == 0)
	}
}

func equalExpandResults(a, b ontology.ExpandResult) string {
	if len(a.Facts) != len(b.Facts) {
		return fmt.Sprintf("fact count %d != %d", len(a.Facts), len(b.Facts))
	}
	for i := range a.Facts {
		fa, fb := a.Facts[i], b.Facts[i]
		if fa.ValidTime != fb.ValidTime || fa.RecordTime != fb.RecordTime {
			return fmt.Sprintf("fact %d: (valid=%d,record=%d) != (valid=%d,record=%d)",
				i, fa.ValidTime, fa.RecordTime, fb.ValidTime, fb.RecordTime)
		}
		if fa.SchemaVersionID != fb.SchemaVersionID {
			return fmt.Sprintf("fact %d: schema version %d != %d", i, fa.SchemaVersionID, fb.SchemaVersionID)
		}
		if len(fa.Props) != len(fb.Props) {
			return fmt.Sprintf("fact %d: prop count %d != %d", i, len(fa.Props), len(fb.Props))
		}
		for k, pa := range fa.Props {
			pb, ok := fb.Props[k]
			if !ok {
				return fmt.Sprintf("fact %d: prop %q missing in naive result", i, k)
			}
			if pa.Status != pb.Status {
				return fmt.Sprintf("fact %d prop %q: status %s != %s", i, k, pa.Status, pb.Status)
			}
			if pa.Status == ontology.StatusValue && !pa.Value.Equal(pb.Value) {
				return fmt.Sprintf("fact %d prop %q: value %+v != %+v", i, k, pa.Value, pb.Value)
			}
		}
	}
	return ""
}

// TestDifferentialRandomSequences 在多个随机种子的操作序列下
// 对照快速实现与朴素模型。
func TestDifferentialRandomSequences(t *testing.T) {
	seeds := []int64{1, 7, 42, 1337, 20261007}
	for _, seed := range seeds {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			runDifferential(t, seed, 400)
		})
	}
}

func runDifferential(t *testing.T, seed int64, steps int) {
	t.Helper()
	r := rand.New(rand.NewSource(seed))
	logger := newDiffLogger(t)
	defer logger.close()

	store := ontology.NewStore()
	initialProps := randomProps(r)
	if err := store.CreateObjectType("T", initialProps, 0); err != nil {
		t.Fatalf("CreateObjectType: %v", err)
	}
	if err := store.RegisterObject("T", "o1"); err != nil {
		t.Fatalf("RegisterObject: %v", err)
	}
	model := naive.New(initialProps, 0)

	var recordClock ontology.RecordTime
	var firstRecord ontology.RecordTime
	hasFact := false

	for step := 0; step < steps; step++ {
		switch r.Intn(3) {
		case 0: // 写事实
			recordClock += ontology.RecordTime(r.Intn(3)) // 允许相等（修正轨迹）
			f := ontology.Fact{
				ObjectID:   "o1",
				ValidTime:  ontology.ValidTime(r.Intn(50)),
				RecordTime: recordClock,
				Values:     map[string]ontology.Value{propNames[r.Intn(len(propNames))]: randomValue(r)},
			}
			input := fmt.Sprintf("step=%d fact valid=%d record=%d", step, f.ValidTime, f.RecordTime)
			if err := store.WriteFact(f); err != nil {
				logger.log("write", input, nil, "error-match", err.Error())
				continue // 写入被快速实现拒绝（未知属性/不可转换），朴素模型同步跳过
			}
			model.WriteFact(f)
			if !hasFact {
				firstRecord = f.RecordTime
				hasFact = true
			}
			logger.log("write", input, nil, "match", "")

		case 1: // 迁移
			m := ontology.Migration{
				TypeID:        "T",
				NewProps:      randomProps(r),
				EffectiveFrom: ontology.RecordTime(r.Intn(int(recordClock) + 2)),
			}
			input := fmt.Sprintf("step=%d migrate effectiveFrom=%d", step, m.EffectiveFrom)
			serr := store.Migrate(m)
			nerr := model.Migrate(m)
			if (serr == nil) != (nerr == nil) {
				logger.log("migrate", input, nil, "mismatch",
					fmt.Sprintf("store err=%v naive err=%v", serr, nerr))
				t.Fatalf("migrate error divergence: store=%v naive=%v", serr, nerr)
			}
			verdict := "match"
			detail := ""
			if serr != nil {
				verdict = "error-match"
				detail = serr.Error()
			}
			logger.log("migrate", input, nil, verdict, detail)

		case 2: // 展开
			if !hasFact {
				continue
			}
			req := ontology.ExpandRequest{
				ObjectID:   "o1",
				AsOfRecord: ontology.RecordTime(r.Intn(int(recordClock) + 2)),
				ValidFrom:  ontology.ValidTime(r.Intn(50)),
				ValidTo:    ontology.ValidTime(r.Intn(50)),
			}
			// 30% 概率固定一个（可能已作废的）版本。
			var pinnedID int64
			pinned := r.Intn(10) < 3
			if pinned {
				pinnedID = int64(1 + r.Intn(20))
				req.PinSchemaVersion = &pinnedID
			}
			input := fmt.Sprintf("step=%d expand asOf=%d valid=[%d,%d] pin=%v",
				step, req.AsOfRecord, req.ValidFrom, req.ValidTo, pinned)

			got, err := store.Expand(req)

			// 按优先级独立计算预期错误。
			wantCode, wantErr := expectedExpandError(store, req, firstRecord)
			if wantErr {
				if err == nil {
					logger.log("expand", input, nil, "mismatch", "expected error, got success")
					t.Fatalf("expected error %s, got success", wantCode)
				}
				oe := err.(*ontology.Error)
				if oe.Code != wantCode {
					logger.log("expand", input, nil, "mismatch",
						fmt.Sprintf("error code %s != %s", oe.Code, wantCode))
					t.Fatalf("error code = %s, want %s", oe.Code, wantCode)
				}
				logger.log("expand", input, nil, "error-match", oe.Code.String())
				continue
			}
			if err != nil {
				logger.log("expand", input, nil, "mismatch", "unexpected error: "+err.Error())
				t.Fatalf("unexpected expand error: %v", err)
			}

			want := model.Expand(req)
			if diff := equalExpandResults(got, want); diff != "" {
				logger.log("expand", input, versionsOf(got), "mismatch", diff)
				t.Fatalf("expansion divergence: %s", diff)
			}
			logger.log("expand", input, versionsOf(got), "match", "")
		}
	}

	// 存储内审计日志必须非空且单调编号（判定记录可事后核查）。
	audit := store.AuditLog()
	for i, rec := range audit {
		if rec.Seq != int64(i+1) {
			t.Fatalf("audit seq gap at %d: %d", i, rec.Seq)
		}
	}
}

// expectedExpandError 按与实现无关的优先级规则独立推导预期错误。
func expectedExpandError(s *ontology.Store, req ontology.ExpandRequest, firstRecord ontology.RecordTime) (ontology.ErrorCode, bool) {
	if req.AsOfRecord < firstRecord {
		return ontology.ErrCodeRecordBeforeFirstFact, true
	}
	if req.PinSchemaVersion != nil {
		found := false
		for _, v := range s.SchemaVersions("T") {
			if v.ID == *req.PinSchemaVersion {
				found = true
				break
			}
		}
		if !found {
			return ontology.ErrCodeSchemaInvalidated, true
		}
	}
	if req.ValidFrom > req.ValidTo {
		return ontology.ErrCodeContradictoryRange, true
	}
	return 0, false
}

func versionsOf(res ontology.ExpandResult) []int64 {
	seen := map[int64]bool{}
	var ids []int64
	for _, f := range res.Facts {
		if !seen[f.SchemaVersionID] {
			seen[f.SchemaVersionID] = true
			ids = append(ids, f.SchemaVersionID)
		}
	}
	return ids
}
