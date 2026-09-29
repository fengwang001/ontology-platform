package fanout_test

import (
	"context"
	"errors"
	"fmt"
	"time"

	"ontology/fanout"
)

type demoClient struct{}

func (demoClient) Query(ctx context.Context, shard fanout.ShardSpec, agg fanout.Aggregation, k int, emit func(fanout.ShardResult)) error {
	switch shard.Name {
	case "s1":
		emit(fanout.ShardResult{Count: 12})
		return nil
	case "s2":
		return errors.New("connection reset")
	default: // s3 never replies before the deadline
		<-ctx.Done()
		return ctx.Err()
	}
}

func ExampleExecute() {
	req := fanout.Request{
		Shards: []fanout.ShardSpec{
			{Name: "s1", MaxRows: 50, MaxValue: 9},
			{Name: "s2", MaxRows: 80, MaxValue: 9},
			{Name: "s3", MaxRows: 20, MaxValue: 4},
		},
		Agg:         fanout.AggCount,
		Concurrency: 2,
		Deadline:    50 * time.Millisecond,
		Client:      demoClient{},
	}
	ans, stats, err := fanout.Execute(context.Background(), req)
	if err != nil {
		fmt.Println("rejected:", err)
		return
	}
	fmt.Printf("count range: [%d, %d] exact=%v conclusive=%v\n", ans.Lower, ans.Upper, ans.Exact, ans.Conclusive)
	fmt.Printf("succeeded=%d failed=%d timedOut=%d boundViolation=%d duplicate=%d peak=%d\n",
		stats.Succeeded, stats.Failed, stats.TimedOut, stats.BoundViolation, stats.Duplicate, stats.PeakConcurrent)
	// Output:
	// count range: [12, 112] exact=false conclusive=true
	// succeeded=1 failed=1 timedOut=1 boundViolation=0 duplicate=0 peak=2
}

func ExampleExecute_rejection() {
	_, _, err := fanout.Execute(context.Background(), fanout.Request{
		Shards:      []fanout.ShardSpec{{Name: "a"}, {Name: "a"}},
		Agg:         fanout.AggSum,
		Concurrency: 4,
		Deadline:    time.Second,
		Client:      demoClient{},
	})
	var re *fanout.RejectError
	if errors.As(err, &re) {
		fmt.Println("reject kind:", re.Kind)
	}
	// Output:
	// reject kind: duplicate_shard_name
}
