package ontology

import (
	"bytes"
	"fmt"
	"sync"
	"testing"
)

func TestErrorPriorityOrdering(t *testing.T) {
	_, st := twoLevelSchema(t)
	mkInstance(t, st, "d1", "Dept", map[string]string{"code": "D1"})
	mkInstance(t, st, "m1", "Manager", nil)

	// 源实例与目标实例同时不存在：必须报告优先级最高的源实例不存在。
	_, err := st.AddLink("memberOf", "ghostFrom", "ghostTo")
	if ErrorKindOf(err) != KindSourceNotFound {
		t.Fatalf("want source-not-found, got %v", err)
	}
	// 链接类型未注册（不支持派生索引）。
	_, err = st.AddLink("nope", "m1", "d1")
	if ErrorKindOf(err) != KindLinkTypeNotSupported {
		t.Fatalf("want link-not-supported, got %v", err)
	}
	// 写不存在的实例：源实例不存在。
	_, err = st.SetAttribute("ghost", "code", PropertyValue{Val: "x", Has: true})
	if ErrorKindOf(err) != KindSourceNotFound {
		t.Fatalf("want source-not-found on write, got %v", err)
	}
	// 查询非派生属性：链接类型不支持。
	_, err = st.QueryIndex("Dept", "code", "D1")
	if ErrorKindOf(err) != KindLinkTypeNotSupported {
		t.Fatalf("want link-not-supported on query, got %v", err)
	}
	// classify 同时收到多类错误时，固定取最高优先级。
	got := classify([]error{
		newError(KindDownstreamUpdateFailed, "low"),
		newError(KindNotUnique, "mid"),
		newError(KindSourceNotFound, "high"),
		newError(KindCyclicDerivation, "mid2"),
	})
	if ErrorKindOf(got) != KindSourceNotFound {
		t.Fatalf("classify priority broken: %v", got)
	}
}

func TestConcurrentReadersAndWriters(t *testing.T) {
	_, st := twoLevelSchema(t)
	mkInstance(t, st, "d1", "Dept", map[string]string{"code": "D1"})
	for i := 0; i < 20; i++ {
		mkInstance(t, st, idf("m%d", i), "Manager", nil)
		addLink(t, st, "memberOf", idf("m%d", i), "d1")
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				default:
				}
				val := idf("V%d-%d", seed, i%3)
				if _, err := st.SetAttribute("d1", "code",
					PropertyValue{Val: val, Has: true}); err != nil {
					t.Errorf("write: %v", err)
					return
				}
			}
		}(w)
	}
	for r := 0; r < 8; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				// 每个读事务（单次 QueryIndex）必须自洽：返回行的载荷值
				// 必须恰好等于所查询的键，且不得出现重复实例。
				for _, want := range []string{"V0-0", "V0-1", "V0-2"} {
					rows, err := st.QueryIndex("Manager", "deptCode", want)
					if err != nil {
						t.Errorf("query: %v", err)
						return
					}
					seen := map[ObjectID]bool{}
					for _, row := range rows {
						if row.Value.Val != want || !row.Value.Has {
							t.Errorf("row payload %q != queried key %q", row.Value.Val, want)
							return
						}
						if seen[row.ID] {
							t.Errorf("duplicate row %s", row.ID)
							return
						}
						seen[row.ID] = true
					}
				}
			}
		}()
	}
	// 让写者跑一小段后停止，等待全部 goroutine 收尾。
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	close(stop)
	// 写者在 stop 后立即退出；读者循环有限次，整体必须收敛。
	wg.Wait()
	<-done
}

func TestJournalRecordsInputsAndDownstreams(t *testing.T) {
	var buf bytes.Buffer
	_, st := twoLevelSchema(t)
	st.Journal().SetOutput(&buf)
	mkInstance(t, st, "d1", "Dept", map[string]string{"code": "D1"})
	mkInstance(t, st, "m1", "Manager", nil)
	addLink(t, st, "memberOf", "m1", "d1")
	buf.Reset()
	if _, err := st.SetAttribute("d1", "code",
		PropertyValue{Val: "D2", Has: true}); err != nil {
		t.Fatal(err)
	}
	logged := buf.String()
	for _, want := range []string{"set_attribute", "m1", "source attribute changed", "D2"} {
		if !bytes.Contains(buf.Bytes(), []byte(want)) {
			t.Fatalf("journal missing %q in: %s", want, logged)
		}
	}
}

func idf(format string, args ...any) string {
	return fmt.Sprintf(format, args...)
}
