package ontology

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// 差分测试：随机生成操作序列与随机中断点，
// 将产品实现的重放与查询结果同独立朴素模型逐一对照，
// 并记录每次查询与重放的输入、输出与所依据的日志序号范围。

var (
	diffUsers     = []UserID{AdminUser, "u1", "u2", "u3"}
	diffObjTypes  = []ObjectTypeID{"T1", "T2"}
	diffLinkTypes = []LinkTypeID{"L1", "L2"}
	diffObjects   = []ObjectID{"o1", "o2", "o3", "o4", "o5", "o6"}
)

func randomOp(r *rand.Rand) Operation {
	pick := func(ss []ObjectID) ObjectID { return ss[r.Intn(len(ss))] }
	switch r.Intn(8) {
	case 0:
		return Operation{Kind: OpDeclareObjectType, ObjectType: diffObjTypes[r.Intn(len(diffObjTypes))]}
	case 1:
		lt := diffLinkTypes[r.Intn(len(diffLinkTypes))]
		return Operation{Kind: OpDeclareLinkType, LinkSpec: &LinkType{
			ID:       lt,
			FromType: diffObjTypes[r.Intn(len(diffObjTypes))],
			ToType:   diffObjTypes[r.Intn(len(diffObjTypes))],
			Cost:     int64(1 + r.Intn(5)),
			Directed: r.Intn(2) == 0,
			// 小基数上限用于触发基数拒绝路径。
			MaxOutgoing: r.Intn(3),
			MaxIncoming: r.Intn(3),
		}}
	case 2:
		return Operation{
			Kind:       OpCreateObject,
			ObjectType: diffObjTypes[r.Intn(len(diffObjTypes))],
			Object:     pick(diffObjects),
		}
	case 3:
		return Operation{Kind: OpInvalidateObject, Object: pick(diffObjects)}
	case 4:
		return Operation{
			Kind:     OpCreateLink,
			LinkType: diffLinkTypes[r.Intn(len(diffLinkTypes))],
			From:     pick(diffObjects),
			To:       pick(diffObjects),
		}
	case 5:
		return Operation{
			Kind:     OpRemoveLink,
			LinkType: diffLinkTypes[r.Intn(len(diffLinkTypes))],
			From:     pick(diffObjects),
			To:       pick(diffObjects),
		}
	case 6, 7:
		kind := OpGrantPermission
		if r.Intn(2) == 0 {
			kind = OpRevokePermission
		}
		res := Resource{Kind: ResourceObjectType, ID: string(diffObjTypes[r.Intn(len(diffObjTypes))])}
		if r.Intn(2) == 0 {
			res = Resource{Kind: ResourceLinkType, ID: string(diffLinkTypes[r.Intn(len(diffLinkTypes))])}
		}
		action := ActionRead
		if r.Intn(2) == 0 {
			action = ActionWrite
		}
		return Operation{
			Kind:       kind,
			Permission: &Permission{User: diffUsers[r.Intn(len(diffUsers))], Action: action, Resource: res},
		}
	}
	panic("unreachable")
}

func rejectKindOf(err error) (RejectKind, bool) {
	if rej, ok := err.(*RejectError); ok {
		return rej.Kind, true
	}
	return "", false
}

func runDifferential(t *testing.T, seed int64) {
	r := rand.New(rand.NewSource(seed))
	dir := t.TempDir()
	logPath := filepath.Join(dir, "diff.log")
	svc := newReadyService(t, logPath)
	naive := newNaiveModel()

	var accepted []Operation
	var offsets []int64 // 每条被接受操作之后的文件偏移（日志项边界）

	const numOps = 150
	for i := 0; i < numOps; i++ {
		op := randomOp(r)
		caller := diffUsers[r.Intn(len(diffUsers))]
		_, svcErr := svc.Apply(op, caller)
		nvErr := naive.validate(op, caller)

		svcKind, svcRej := rejectKindOf(svcErr)
		switch {
		case svcErr == nil && nvErr == nil:
			naive.apply(op)
			accepted = append(accepted, op)
			off, err := svc.writer.offset()
			if err != nil {
				t.Fatal(err)
			}
			offsets = append(offsets, off)
		case svcRej && nvErr != nil:
			if svcKind != nvErr.Kind {
				t.Fatalf("seed=%d op=%d %+v caller=%s: reject kind %s vs naive %s",
					seed, i, op, caller, svcKind, nvErr.Kind)
			}
		default:
			t.Fatalf("seed=%d op=%d %+v caller=%s: service err=%v naive err=%v",
				seed, i, op, caller, svcErr, nvErr)
		}

		// 周期性执行随机查询并与朴素模型对照。
		if i%10 == 9 {
			start := diffObjects[r.Intn(len(diffObjects))]
			end := diffObjects[r.Intn(len(diffObjects))]
			qCaller := diffUsers[r.Intn(len(diffUsers))]
			got, _, err := svc.ShortestPath(start, end, qCaller)
			if err != nil {
				t.Fatal(err)
			}
			want := naive.shortestPath(start, end, qCaller)
			t.Logf("seed=%d query start=%s end=%s caller=%s logSeqRange=[1,%d] got=%+v want=%+v",
				seed, start, end, qCaller, len(accepted), got, want)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("seed=%d query mismatch: got %+v want %+v", seed, got, want)
			}
		}
	}
	svc.Close()

	full, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}

	// 随机中断点：任选日志项边界 k，另加 k+1 条写入到随机字节处的撕裂尾部。
	crash := func(k int, tornBytes int) {
		t.Helper()
		crashLog := filepath.Join(dir, fmt.Sprintf("crash-%d-%d.log", k, tornBytes))
		// 前 k 条被接受操作占据 [0, startOf(k+1))。
		var size int64
		if k > 0 {
			size = offsets[k-1]
		}
		if tornBytes > 0 && k < len(offsets) {
			// 第 k+1 条（0 基下标 k）写入到一半：帧区间 [size, offsets[k])。
			size += int64(tornBytes) % (offsets[k] - size)
		}
		if err := os.WriteFile(crashLog, full[:size], 0o644); err != nil {
			t.Fatal(err)
		}
		svc2 := New(crashLog)
		if err := svc2.Replay(); err != nil {
			t.Fatalf("replay crash k=%d torn=%d: %v", k, tornBytes, err)
		}
		defer svc2.Close()

		// 朴素模型只重放前 k 条被接受的操作。
		nm := newNaiveModel()
		for _, op := range accepted[:k] {
			nm.apply(op)
		}
		// 日志中不得含有 k 之后的任何记录。
		entries, _, err := readLog(crashLog)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != k {
			t.Fatalf("k=%d torn=%d: replayed %d entries", k, tornBytes, len(entries))
		}
		t.Logf("seed=%d replay crashAt=%d tornBytes=%d logSeqRange=[1,%d]",
			seed, k, tornBytes, k)

		// 随机查询组合对照：重放后的结果必须与朴素模型完全一致。
		for q := 0; q < 20; q++ {
			start := diffObjects[r.Intn(len(diffObjects))]
			end := diffObjects[r.Intn(len(diffObjects))]
			qCaller := diffUsers[r.Intn(len(diffUsers))]
			got, _, err := svc2.ShortestPath(start, end, qCaller)
			if err != nil {
				t.Fatal(err)
			}
			want := nm.shortestPath(start, end, qCaller)
			t.Logf("seed=%d post-crash query start=%s end=%s caller=%s logSeqRange=[1,%d] got=%+v want=%+v",
				seed, start, end, qCaller, k, got, want)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("seed=%d k=%d torn=%d query(%s,%s,%s): got %+v want %+v",
					seed, k, tornBytes, start, end, qCaller, got, want)
			}
		}
	}

	// 多种中断位置组合：完整边界、以及最后一条写到一半的撕裂。
	for _, k := range []int{0, len(accepted) / 3, len(accepted) / 2, len(accepted) - 1, len(accepted)} {
		if k < 0 {
			continue
		}
		crash(k, 0)
		if k < len(accepted) {
			crash(k, 1+r.Intn(64)) // 撕裂最后一条
		}
	}
}

func TestDifferentialRandomCrashPoints(t *testing.T) {
	for _, seed := range []int64{1, 2, 3, 4, 5} {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			runDifferential(t, seed)
		})
	}
}
