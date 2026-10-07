package ontology

import (
	"fmt"
	"reflect"
	"testing"
)

// crashHook 返回一个故障注入钩子：当阶段点等于 target 时触发一次崩溃。
func crashHook(target StagePoint) func(StagePoint) bool {
	return func(p StagePoint) bool { return p == target }
}

// queueHook 返回一个按队列依次触发崩溃的钩子（用于恢复过程再次被中断）。
func queueHook(queue ...StagePoint) func(StagePoint) bool {
	remaining := append([]StagePoint(nil), queue...)
	return func(p StagePoint) bool {
		if len(remaining) > 0 && p == remaining[0] {
			remaining = remaining[1:]
			return true
		}
		return false
	}
}

// seedInstances 通过单实例写入构造初始状态。
func seedInstances(t *testing.T, e *Engine, seeds map[InstanceID]map[string]string) {
	t.Helper()
	for id, props := range seeds {
		if err := e.Write(id, props); err != nil {
			t.Fatalf("seed write %s: %v", id, err)
		}
	}
}

// expectInstances 由初始状态计算批次完整生效后的期望状态。
func expectInstances(pre []Instance, id BatchID, muts []Mutation) []Instance {
	mem := make(map[InstanceID]Instance, len(pre))
	for _, in := range pre {
		mem[in.ID] = in.Clone()
	}
	for _, m := range muts {
		in := mem[m.Instance]
		in.Version++
		in.LastBatch = id
		in.Props = mergeProps(in.Props, m.Props)
		mem[in.ID] = in
	}
	out := make([]Instance, 0, len(mem))
	for _, in := range mem {
		out = append(out, in)
	}
	sortInstances(out)
	return out
}

func sortInstances(s []Instance) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j].ID < s[j-1].ID; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// assertState 比较两个实例视图是否完全一致（ID/版本/批次戳/属性）。
func assertState(t *testing.T, what string, got, want []Instance) {
	t.Helper()
	sortInstances(got)
	sortInstances(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s 状态不一致:\n got=%v\nwant=%v", what, fmtInstances(got), fmtInstances(want))
	}
}

func fmtInstances(s []Instance) string {
	out := ""
	for _, in := range s {
		out += fmt.Sprintf("{id=%s v=%d batch=%d props=%v} ", in.ID, in.Version, in.LastBatch, in.Props)
	}
	return out
}

// commitPointIndex 返回 Commit 阶段在 BatchPoints 序列中的下标。
// 注入点下标 <= 该值时 COMMIT 记录尚未落盘（恢复应归为未生效）。
func commitPointIndex(nMut int) int {
	for i, p := range BatchPoints(nMut) {
		if p.Phase == PhaseCommit {
			return i
		}
	}
	panic("no commit point")
}

// recordingDisk 包装 SimDisk，记录 ReadFile 访问过的文件名，
// 用于验证恢复扫描范围不随历史批次总数增长（测试局部，不改公开 API）。
type recordingDisk struct {
	inner *SimDisk
	reads []string
}

func newRecordingDisk(d *SimDisk) *recordingDisk {
	return &recordingDisk{inner: d}
}

func (r *recordingDisk) ReadFile(name string) ([]byte, error) {
	r.reads = append(r.reads, name)
	return r.inner.ReadFile(name)
}

func (r *recordingDisk) WriteFile(name string, data []byte)  { r.inner.WriteFile(name, data) }
func (r *recordingDisk) AppendFile(name string, data []byte) { r.inner.AppendFile(name, data) }
func (r *recordingDisk) Sync(name string)                    { r.inner.Sync(name) }
func (r *recordingDisk) Delete(name string)                  { r.inner.Delete(name) }
func (r *recordingDisk) Rename(old, new string)              { r.inner.Rename(old, new) }
func (r *recordingDisk) Exists(name string) bool             { return r.inner.Exists(name) }
func (r *recordingDisk) List(prefix string) []string         { return r.inner.List(prefix) }
func (r *recordingDisk) Crash()                              { r.inner.Crash() }

func (r *recordingDisk) reset() { r.reads = nil }
