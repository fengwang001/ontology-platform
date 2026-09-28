package lwwset

import (
	"fmt"
	"sort"
	"strings"
	"testing"
)

// dump 打印某一步之后的输入结果、全部记录与每条记录的成员判定依据。
func dump(t *testing.T, step string, r *Replica) {
	t.Helper()
	recs := r.Records()
	elems := make([]string, 0, len(recs))
	for e := range recs {
		elems = append(elems, e)
	}
	sort.Strings(elems)
	var b strings.Builder
	for _, e := range elems {
		rec := recs[e]
		fmt.Fprintf(&b, " %s{a=%d,d=%d,member=%t}", e, rec.AddTime, rec.RemoveTime, rec.present())
	}
	t.Logf("[%s] replica#%d seq=%d records:%s; members=%v",
		step, r.ID(), r.Seq(), b.String(), r.Elements())
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func recordsEqual(a, b *Replica) (bool, string) {
	ra, rb := a.Records(), b.Records()
	if len(ra) != len(rb) {
		return false, fmt.Sprintf("record count %d != %d\nra=%v\nrb=%v", len(ra), len(rb), ra, rb)
	}
	for e, va := range ra {
		vb, ok := rb[e]
		if !ok || va != vb {
			return false, fmt.Sprintf("element %q differs: %+v vs %+v", e, va, vb)
		}
	}
	return true, ""
}

// naive 是朴素参照实现：每个元素独立保存最大添加/删除时间，
// 成员规则与 CRDT 完全相同（并列偏删除）。
type naive map[string][2]int64

func (n naive) apply(element string, add, remove int64) {
	cur := n[element]
	if add > cur[0] {
		cur[0] = add
	}
	if remove > cur[1] {
		cur[1] = remove
	}
	n[element] = cur
}

func (n naive) merge(o naive) {
	for e, v := range o {
		n.apply(e, v[0], v[1])
	}
}

func (n naive) members() []string {
	out := make([]string, 0)
	for e, v := range n {
		if v[0] > 0 && v[1] < v[0] {
			out = append(out, e)
		}
	}
	sort.Strings(out)
	return out
}

func snapshotRef(r *Replica) naive {
	n := naive{}
	for e, rec := range r.Records() {
		n[e] = [2]int64{rec.AddTime, rec.RemoveTime}
	}
	return n
}
