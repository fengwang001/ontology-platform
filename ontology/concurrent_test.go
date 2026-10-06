package ontology

import (
	"fmt"
	"sync"
	"testing"
)

// TestConcurrentSerializability 并发提交+撤销，验证无竞争、拒绝符合规则、
// 存活笔数守恒，且最终年度总账等于对存活笔按受理顺序逐笔重放的结果。
func TestConcurrentSerializability(t *testing.T) {
	spec := baseSpec()
	spec.OOPCap = 100000
	var lines = []Line{ln("A", CatInpatient, 100), ln("B", CatOutpatient, 50)}
	run := func() (int64, int64, int) {
		e := NewEngine()
		_ = e.RegisterPolicy("p", spec)
		var wg sync.WaitGroup
		var mu sync.Mutex
		survivors := map[string]bool{}
		cancelOK := 0
		const workers = 8
		const perWorker = 60
		for w := 0; w < workers; w++ {
			wg.Add(1)
			go func(w int) {
				defer wg.Done()
				for k := 0; k < perWorker; k++ {
					id := fmt.Sprintf("w%d-%d", w, k)
					c := claim(id, 10, lines...)
					_, err := e.Submit("p", c)
					if err != nil {
						t.Errorf("submit %s: %v", id, err)
						return
					}
					mu.Lock()
					survivors[id] = true
					mu.Unlock()
					// 尝试撤销：仅当本笔恰为末笔时成功，否则必须报“非末笔”。
					if k%2 == 1 {
						err := e.Cancel("p", id)
						switch {
						case err == nil:
							mu.Lock()
							delete(survivors, id)
							cancelOK++
							mu.Unlock()
						case !errorsIs(err, ErrNotLast):
							t.Errorf("cancel %s unexpected err=%v", id, err)
						}
					}
				}
			}(w)
		}
		wg.Wait()
		_, ded, oop, _ := e.YearSnapshot("p", 10)
		return ded, oop, len(survivors)
	}

	ded, oop, live := run()
	// 年度免赔上限 1000：前 10 笔各扣 100，其后免赔 0。
	wantDed := int64(live) * 100
	if wantDed > 1000 {
		wantDed = 1000
	}
	if ded != wantDed {
		t.Fatalf("免赔累计=%d 存活=%d 期望 %d", ded, live, wantDed)
	}
	// 前 10 笔自付125；免赔填满后每笔：住院100*80%=80赔(自付20)+门诊25赔(自付25)=45。
	wantOOP := int64(125*10 + 45*(live-10))
	if live < 10 {
		wantOOP = int64(live) * 125
	}
	if oop != wantOOP {
		t.Fatalf("自付累计=%d 存活=%d 期望 %d", oop, live, wantOOP)
	}
	t.Logf("并发完成: 存活=%d 免赔=%d 自付=%d", live, ded, oop)
}

func errorsIs(err, target error) bool { return err != nil && err.Error() == target.Error() }
