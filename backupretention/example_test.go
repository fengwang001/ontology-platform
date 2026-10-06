package backupretention_test

import (
	"fmt"
	"os"

	"ontology/backupretention"
)

func Example() {
	svc := backupretention.NewService(backupretention.WithLogger(os.Stdout))

	// 2024-01-01 00:00 UTC（周一）登记全量，随后登记同链增量。
	full := int64(1704067200)
	t1 := full + 3600
	if err := svc.RegisterFull("F", 1024, full); err != nil {
		panic(err)
	}
	if err := svc.RegisterIncremental("I", 512, t1, "F"); err != nil {
		panic(err)
	}

	// 保留最近 7 个日周期、4 个周周期、3 个月周期。
	if err := svc.SetPolicy(backupretention.Policy{Daily: 7, Weekly: 4, Monthly: 3}, t1); err != nil {
		panic(err)
	}

	plan, err := svc.Plan(t1)
	if err != nil {
		panic(err)
	}
	for _, b := range plan.Retained {
		var direct []string
		for _, l := range b.Reason.DirectLayers {
			direct = append(direct, layerName(l))
		}
		fmt.Printf("KEEP %s direct=%v dependency=%t legal=%t\n",
			b.ID, direct, b.Reason.Dependency, b.Reason.LegalHold)
	}
	for _, b := range plan.Deletable {
		fmt.Printf("DELETE %s\n", b.ID)
	}
	// Output:
	// RegisterFull IN {id="F" kind=full size=1024 createdAt=1704067200} -> OK registered; clock=1704067200
	// RegisterIncremental IN {id="I" kind=incr size=512 createdAt=1704070800 parent="F"} -> OK registered; clock=1704070800
	// SetPolicy IN {daily=7 weekly=4 monthly=3 now=1704070800} -> OK old={0,0,0}; affects only future plans
	// Plan now=1704070800 KEEP id="F" createdAt=1704067200 direct=[-] dependency=true legal=false
	// Plan now=1704070800 KEEP id="I" createdAt=1704070800 direct=[daily,weekly,monthly] dependency=false legal=false
	// KEEP F direct=[] dependency=true legal=false
	// KEEP I direct=[daily weekly monthly] dependency=false legal=false
}

func layerName(l backupretention.Layer) string {
	switch l {
	case backupretention.LayerDaily:
		return "daily"
	case backupretention.LayerWeekly:
		return "weekly"
	default:
		return "monthly"
	}
}
