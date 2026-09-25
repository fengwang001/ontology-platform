// Package api 对外门面。依赖 dwr。
package api

import (
	"errors"
	"fmt"
	"maps"

	"ontology/dwr"
	"ontology/rec"
)

type API struct{ st *dwr.Store }

func New() *API { return &API{st: dwr.New()} }

func (a *API) Put(k, v string, ver int64) error              { return a.st.Put(k, v, ver) }
func (a *API) Del(k string, ver int64) error                 { return a.st.Del(k, ver) }
func (a *API) PutOne(side int, k, v string, ver int64) error { return a.st.PutOne(side, k, v, ver) }
func (a *API) DelOne(side int, k string, ver int64) error    { return a.st.DelOne(side, k, ver) }
func (a *API) Reconcile()                                    { a.st.Reconcile() }

// View 先对账再返回活值视图，墓碑键不出现。
func (a *API) View() map[string]string {
	a.st.Reconcile()
	return a.st.View()
}

// replicas 供测试与自检核验最终一致与幂等。
func (a *API) replicas() (x, y map[string]rec.Record) { return a.st.Replicas() }

// batch 权威模型：全部成功写按 Ver 逐键取最大，墓碑则键不存在。
func batch(writes [][2]interface{}) map[string]string {
	best := map[string]rec.Record{}
	for _, w := range writes {
		k := w[0].(string)
		r := w[1].(rec.Record)
		if cur, ok := best[k]; !ok || r.Ver > cur.Ver {
			best[k] = r
		}
	}
	out := map[string]string{}
	for k, r := range best {
		if !r.Tomb() {
			out[k] = r.Val
		}
	}
	return out
}

// seq 是一组内置写序列：op 0=Put 1=Del 2=PutOne(A) 3=PutOne(B) 4=DelOne(A) 5=DelOne(B)。
type wr struct {
	op      int
	k, v    string
	ver     int64
	wantErr error
}
type seq []wr

// run 执行序列，返回成功写（供批量模型重算）；任何实际错误与期望不符即失败。
func (a *API) run(s seq) ([][2]interface{}, error) {
	var okWrites [][2]interface{}
	for i, w := range s {
		var err error
		var r rec.Record
		switch w.op {
		case 0:
			err = a.Put(w.k, w.v, w.ver)
			r = rec.Record{Val: w.v, Ver: w.ver}
		case 1:
			err = a.Del(w.k, w.ver)
			r = rec.Record{Ver: w.ver, Del: true}
		case 2:
			err = a.PutOne(0, w.k, w.v, w.ver)
			r = rec.Record{Val: w.v, Ver: w.ver}
		case 3:
			err = a.PutOne(1, w.k, w.v, w.ver)
			r = rec.Record{Val: w.v, Ver: w.ver}
		case 4:
			err = a.DelOne(0, w.k, w.ver)
			r = rec.Record{Ver: w.ver, Del: true}
		case 5:
			err = a.DelOne(1, w.k, w.ver)
			r = rec.Record{Ver: w.ver, Del: true}
		}
		if err != w.wantErr {
			return nil, fmt.Errorf("api: write %d got err %v want %v", i, err, w.wantErr)
		}
		if err == nil {
			okWrites = append(okWrites, [2]interface{}{w.k, r})
		}
	}
	return okWrites, nil
}

var selfSeq = seq{
	{op: 0, k: "a", v: "a1", ver: 1},
	{op: 0, k: "b", v: "b1", ver: 2},
	{op: 0, k: "c", v: "c1", ver: 3},
	{op: 2, k: "a", v: "a2", ver: 4},
	{op: 5, k: "c", ver: 5},
	{op: 4, k: "b", ver: 6},
	{op: 2, k: "d", v: "d1", ver: 7},
	{op: 0, k: "", v: "x", ver: 8, wantErr: rec.ErrKey},   // 不变量4：被拒不留痕
	{op: 0, k: "z", v: "z1", ver: 7, wantErr: rec.ErrVer}, // 不严格递增
	{op: 0, k: "z", v: "", ver: 8, wantErr: rec.ErrVal},
	{op: 3, k: "e", v: "e1", ver: 8},
	{op: 1, k: "d", ver: 9},
}

// SelfCheck 对内置写序列核验四条不变量，全部通过返回 nil。
func (a *API) SelfCheck() error {
	fresh := New()
	okWrites, err := fresh.run(selfSeq)
	if err != nil {
		return err
	}
	fresh.Reconcile()
	// 不变量1：与批量重算一致
	if want := batch(okWrites); !maps.Equal(fresh.View(), want) {
		return fmt.Errorf("api: invariant1 view=%v want=%v", fresh.View(), want)
	}
	// 不变量2：最终一致
	if x, y := fresh.replicas(); !maps.Equal(x, y) {
		return errors.New("api: invariant2 replicas diverge after reconcile")
	}
	// 不变量3：幂等
	x0, y0 := fresh.replicas()
	fresh.Reconcile()
	if x1, y1 := fresh.replicas(); !maps.Equal(x0, x1) || !maps.Equal(y0, y1) {
		return errors.New("api: invariant3 reconcile not idempotent")
	}
	// 不变量4：被拒写不改变状态（序列中三个被拒写之后视图仍等于批量模型）
	if err := fresh.Put("k", "v", 0); err != rec.ErrVer {
		return errors.New("api: invariant4 non-positive ver accepted")
	}
	if want := batch(okWrites); !maps.Equal(fresh.View(), want) {
		return errors.New("api: invariant4 rejected write changed state")
	}
	return nil
}
