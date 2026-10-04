package offline

import (
	"math/rand"
	"testing"
)

// TestRandomAgainstNaive 用 1500 组随机操作序列逐步对照朴素全量重放模型。
// 每条日志通过 t.Logf 打印输入、输出与判定依据（go test -v 可见）。
func TestRandomAgainstNaive(t *testing.T) {
	const sequences = 1500
	const steps = 60
	for seq := 1; seq <= sequences; seq++ {
		rng := rand.New(rand.NewSource(int64(seq)))
		dmax := 1 + rng.Intn(3)
		omax := 1 + rng.Intn(3)
		cool := int64(1 + rng.Intn(120))
		lr := int64(1 + rng.Intn(400))
		lp := int64(1 + rng.Intn(80))

		m := NewManager(dmax, cool, lr, lp, omax)
		n := newNaive(dmax, omax, cool, lr, lp)

		var now int64
		devs := []string{}
		titles := []string{}

		// 固定账号先登记（也覆盖通过随机 AddAccount 登记另一个账号的情形概率为零，
		// 本测试聚焦单账号名额/许可语义）。
		runBoth(t, m, n, seq, 0, nEvent{op: "addAccount", now: 0, acct: "A"})

		for s := 1; s <= steps; s++ {
			// 时钟单调不减，偶尔停在同一时刻以覆盖“恰等”。
			if rng.Intn(5) != 0 {
				now += int64(rng.Intn(25))
			}
			op := pickOp(rng)

			// 保证影片/设备基础集合会建立起来。
			if len(titles) == 0 && op != "addTitle" {
				op = "addTitle"
			}

			e := nEvent{op: op, now: now, acct: "A"}
			switch op {
			case "addTitle":
				name := "T" + itoa(len(titles))
				end := now + int64(1+rng.Intn(500))
				e.title, e.end = name, end
			case "setEnd":
				e.title = titles[rng.Intn(len(titles))]
				// 一半提前（end 可能 <= now，触发参数非法或下架），一半延后。
				if rng.Intn(2) == 0 {
					e.end = now + int64(rng.Intn(3)) // 0..2：含 end==now 的非法
				} else {
					e.end = now + int64(1+rng.Intn(600))
				}
			case "register":
				if len(devs) > 0 && rng.Intn(3) == 0 {
					e.dev = devs[rng.Intn(len(devs))] // 可能已注册
				} else {
					e.dev = "D" + itoa(len(devs))
					devs = append(devs, e.dev)
				}
			case "deregister", "download", "play", "status":
				if len(devs) == 0 {
					e.dev = "D-ghost" // 触发设备未注册
				} else {
					e.dev = devs[rng.Intn(len(devs))]
				}
				e.title = titles[rng.Intn(len(titles))]
			}

			// 少量注入参数非法（空设备/账号），验证拒绝次序第一档。
			if rng.Intn(20) == 0 {
				e.dev = ""
			}
			if rng.Intn(40) == 0 {
				e.now = now - int64(1+rng.Intn(5)) // 注入时钟回退
			}

			runBoth(t, m, n, seq, s, e)

			if op == "addTitle" && e.title != "" && e.end > e.now {
				// 成功与否由 runBoth 以错误码为准；仅在朴素模型接受时补进候选集合。
				if contains(n.titles, e.title) {
					titles = appendUnique(titles, e.title)
				}
			}
		}

		if seq%100 == 0 {
			t.Logf("已完成 %d/%d 组随机序列（dmax=%d omax=%d cool=%d lr=%d lp=%d）",
				seq, sequences, dmax, omax, cool, lr, lp)
		}
	}
}

func pickOp(rng *rand.Rand) string {
	ops := []string{
		"addTitle", "addTitle",
		"setEnd", "setEnd",
		"register", "register", "register",
		"deregister", "deregister",
		"download", "download", "download",
		"play", "play", "play",
		"status", "status",
	}
	return ops[rng.Intn(len(ops))]
}

func contains(m map[string]bool, k string) bool { return m[k] }

func appendUnique(xs []string, x string) []string {
	for _, v := range xs {
		if v == x {
			return xs
		}
	}
	xs = append(xs, x)
	return xs
}
