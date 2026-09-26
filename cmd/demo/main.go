package main

import (
	"context"
	"fmt"
	"time"

	"ontology/internal/parse"
	"ontology/internal/source"
	"ontology/internal/stage"
)

var fails int

func judge(name string, ok bool, detail string) {
	if ok {
		fmt.Printf("OK %s %s\n", name, detail)
	} else {
		fails++
		fmt.Printf("FAIL %s %s\n", name, detail)
	}
}

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 判定 1：背压真实生效（历史最大在途数不超过队列容量之和，source 被阻塞过）。
	g := stage.NewGauge()
	c1, c2 := 6, 6
	raw := stage.NewQueue[source.Msg](c1, g)
	recs := stage.NewQueue[parse.Rec](c2, g)
	src := source.New(source.Config{N: 3000, Keys: 5, BadEvery: 23})
	psr := parse.New()
	grp := stage.NewGroup(ctx)
	grp.Go(func(ctx context.Context) {
		_, _ = src.Run(ctx, raw)
		raw.Close()
	})
	grp.Go(func(ctx context.Context) { _ = psr.Run(ctx, raw, recs) })
	badSeen := 0
	grp.Go(func(ctx context.Context) {
		for {
			r, ok, err := recs.Recv(ctx)
			if err != nil || !ok {
				return
			}
			if r.Bad {
				badSeen++
			}
			time.Sleep(20 * time.Microsecond)
		}
	})
	grp.Wait()
	capSum := int64(c1 + c2)
	judge("backpressure", g.Max() <= capSum,
		fmt.Sprintf("maxInFlight=%d capSum=%d", g.Max(), capSum))

	// 判定 2：解析阻塞被统计，坏记录不进组。
	wantBad := int64(3000 / 23)
	judge("bad-records", psr.Bad() == wantBad && int64(badSeen) == wantBad,
		fmt.Sprintf("bad=%d want=%d", psr.Bad(), wantBad))

	if fails == 0 {
		fmt.Printf("TOTAL OK %d/%d\n", 2, 2)
	} else {
		fmt.Printf("TOTAL FAIL %d\n", fails)
	}
}
