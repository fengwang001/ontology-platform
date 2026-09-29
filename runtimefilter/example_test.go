package runtimefilter_test

import (
	"fmt"
	"time"

	"ontology/runtimefilter"
)

type probeRow struct{ key *int64 }

func ExampleCoordinator() {
	keyOf := func(r runtimefilter.Row) (int64, bool) {
		row := r.(probeRow)
		if row.key == nil {
			return 0, false
		}
		return *row.key, true
	}

	c, err := runtimefilter.NewCoordinator[int64](runtimefilter.Config[int64]{
		JoinType:    runtimefilter.JoinInner,
		ShardCount:  2,
		MaxDistinct: 100,
		ReadyWait:   5 * time.Millisecond,
		Logger:      discardLogger{},
	})
	if err != nil {
		panic(err)
	}
	sc := c.NewScanner("probe-0", keyOf)

	// 就绪前：整批放行（打印 reason=not-ready-or-timeout）。
	sc.FilterBatch([]runtimefilter.Row{probeRow{key: ptr(7)}}, time.Time{})

	_ = c.Report(runtimefilter.ShardReport[int64]{
		Shard: 0, Min: 1, Max: 9, Distinct: map[int64]struct{}{1: {}, 7: {}, 9: {}},
	})
	_ = c.Report(runtimefilter.ShardReport[int64]{Shard: 1, Empty: true})

	// 就绪后：7 命中放行，空键与 42 丢弃。
	out, _ := sc.FilterBatch([]runtimefilter.Row{
		probeRow{key: ptr(7)},
		probeRow{},
		probeRow{key: ptr(42)},
	}, time.Time{})
	fmt.Println("passed rows:", len(out))
	fmt.Println(sc.Stats())
	// Output:
	// passed rows: 1
	// {4 2 2}
}

func ptr(v int64) *int64 { return &v }

type stdoutLogger struct{}

type discardLogger struct{}

func (discardLogger) Logf(string, ...any) {}
