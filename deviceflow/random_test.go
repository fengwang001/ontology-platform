package deviceflow

import (
	"fmt"
	"math/rand"
	"testing"
)

// scriptedGen 返回一个按 script 顺序循环取值的 gen，每个实例独立计数，
// 保证真实实现与朴素模拟看到完全相同的 gen 返回序列。
func scriptedGen(script []string) func() string {
	idx := 0
	return func() string {
		s := script[idx%len(script)]
		idx++
		return s
	}
}

// genPool 混合合规码（含连字符、小写写法）与不合规码，迫使 Start 重试。
var genPool = []string{
	"ABCD-0001", "wxyz-1234", "QWER-TYUI", "1234-ABCD", "zzzz-9999",
	"KLMN-5678", "pqrs-2345", "HFJD-7382", "0000-AAAA", "mnbv-0987",
	"ab", "ABC!", "", "a-b-c", "abcdefghijklmnopq", "1!2@3#",
}

func randomConfig(r *rand.Rand) Config {
	i0 := 1 + r.Int63n(10)
	d := 1 + r.Int63n(10)
	imax := i0 + r.Int63n(40)
	return Config{
		E:    1 + r.Int63n(300),
		I0:   i0,
		D:    d,
		Imax: imax,
		H:    1 + r.Int63n(120),
		Cmax: 1 + r.Intn(5),
		Z:    1 + r.Intn(5),
	}
}

func kindOf(err error) Kind {
	k, _ := AsKind(err)
	return k
}

func checkErrEqual(t *testing.T, op string, errA, errB error) {
	t.Helper()
	if (errA == nil) != (errB == nil) {
		t.Fatalf("%s: error mismatch: real=%v naive=%v", op, errA, errB)
	}
	if errA == nil {
		return
	}
	ea, eb := errA.(*Error), errB.(*Error)
	if ea.Kind != eb.Kind || ea.S != eb.S || ea.U != eb.U {
		t.Fatalf("%s: error mismatch: real=%v naive=%v", op, ea, eb)
	}
}

// TestRandomizedAgainstNaive 用 2000 组随机发起、批准、轮询序列对照
// 真实实现与朴素模拟，日志打印输入、输出与判定依据。
func TestRandomizedAgainstNaive(t *testing.T) {
	const groups = 2000
	clients := []string{"c1", "c2", "c3"}
	// 固定客户端集合，迫使名额与限流生效
	for g := 0; g < groups; g++ {
		r := rand.New(rand.NewSource(int64(g)*7919 + 13))
		cfg := randomConfig(r)
		real, err := New(Config{
			E: cfg.E, I0: cfg.I0, D: cfg.D, Imax: cfg.Imax, H: cfg.H,
			Cmax: cfg.Cmax, Z: cfg.Z, Gen: scriptedGen(genPool),
		})
		if err != nil {
			t.Fatalf("group %d: New: %v", g, err)
		}
		naive := newNaive(cfg, scriptedGen(genPool))

		var devices []string
		var userCodes []string
		scriptNow := int64(0)
		ops := 20 + r.Intn(30)
		t.Logf("group=%d cfg=%+v ops=%d", g, cfg, ops)

		for i := 0; i < ops; i++ {
			// 生成 now：多数前进，少数原地，偶尔回退或越界。
			scriptNow += r.Int63n(30)
			now := scriptNow
			switch x := r.Intn(100); {
			case x < 8:
				now = scriptNow - r.Int63n(20)
				if now < 0 {
					now = 0
				}
			case x < 10:
				now = -1 - r.Int63n(5)
			case x < 12:
				now = maxNowValue + 1 + r.Int63n(5)
			}

			pickClient := func() string {
				if r.Intn(100) < 4 {
					return ""
				}
				return clients[r.Intn(len(clients))]
			}
			pickDevice := func() string {
				if r.Intn(100) < 3 {
					return ""
				}
				if len(devices) > 0 && r.Intn(100) < 75 {
					return devices[r.Intn(len(devices))]
				}
				return fmt.Sprintf("d%d", r.Intn(len(devices)+3))
			}
			pickUserCode := func() string {
				if len(userCodes) > 0 && r.Intn(100) < 75 {
					uc := userCodes[r.Intn(len(userCodes))]
					switch r.Intn(3) {
					case 0:
						return uc
					case 1:
						return "a-b-c-d" // 可能命中也可能未找到
					}
					return uc
				}
				return genPool[r.Intn(len(genPool))]
			}

			switch r.Intn(100) {
			case 0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24:
				cl := pickClient()
				resA, errA := real.Start(cl, now)
				resB, errB := naive.Start(cl, now)
				op := fmt.Sprintf("Start(%q,%d)", cl, now)
				checkErrEqual(t, op, errA, errB)
				if errA == nil {
					if *resA != *resB {
						t.Fatalf("group=%d op=%d %s: real=%+v naive=%+v", g, i, op, resA, resB)
					}
					if real.lastGenCalls != naive.genCalls {
						t.Fatalf("group=%d op=%d %s: genCalls real=%d naive=%d", g, i, op, real.lastGenCalls, naive.genCalls)
					}
					devices = append(devices, resA.DeviceCode)
					userCodes = append(userCodes, resA.UserCode)
					t.Logf("group=%d op=%d %s -> %+v (依据: s+1 次 gen 调用=%d)", g, i, op, resA, real.lastGenCalls)
				} else {
					t.Logf("group=%d op=%d %s -> 拒绝 %v", g, i, op, errA)
				}
			case 25, 26, 27, 28, 29, 30, 31, 32, 33, 34, 35, 36, 37, 38, 39, 40, 41, 42, 43, 44, 45, 46, 47, 48, 49, 50, 51, 52, 53, 54, 55, 56, 57, 58, 59:
				dc := pickDevice()
				resA, errA := real.Poll(dc, now)
				resB, errB := naive.Poll(dc, now)
				op := fmt.Sprintf("Poll(%q,%d)", dc, now)
				checkErrEqual(t, op, errA, errB)
				if errA == nil {
					if *resA != *resB {
						t.Fatalf("group=%d op=%d %s: real=%+v naive=%+v", g, i, op, resA, resB)
					}
					info, _ := real.Interval(dc)
					t.Logf("group=%d op=%d %s -> %+v (依据: status=%s interval=%d nextAllowed=%d)",
						g, i, op, resA, info.Status, info.Interval, info.NextAllowed)
				} else {
					t.Logf("group=%d op=%d %s -> 拒绝 %v", g, i, op, errA)
				}
			case 60, 61, 62, 63, 64, 65, 66, 67, 68, 69, 70, 71, 72, 73, 74:
				uc := pickUserCode()
				approve := r.Intn(2) == 0
				errA := real.Authorize(uc, approve, now)
				errB := naive.Authorize(uc, approve, now)
				op := fmt.Sprintf("Authorize(%q,%v,%d)", uc, approve, now)
				checkErrEqual(t, op, errA, errB)
				t.Logf("group=%d op=%d %s -> err=%v", g, i, op, errA)
			case 75, 76, 77, 78, 79, 80, 81, 82, 83, 84:
				dc := pickDevice()
				resA, errA := real.Interval(dc)
				resB, errB := naive.Interval(dc)
				op := fmt.Sprintf("Interval(%q)", dc)
				checkErrEqual(t, op, errA, errB)
				if errA == nil && *resA != *resB {
					t.Fatalf("group=%d op=%d %s: real=%+v naive=%+v", g, i, op, resA, resB)
				}
				t.Logf("group=%d op=%d %s -> %+v err=%v", g, i, op, resA, errA)
			default:
				cl := pickClient()
				resA, errA := real.ClientInterval(cl, now)
				resB, errB := naive.ClientInterval(cl, now)
				op := fmt.Sprintf("ClientInterval(%q,%d)", cl, now)
				checkErrEqual(t, op, errA, errB)
				if errA == nil && *resA != *resB {
					t.Fatalf("group=%d op=%d %s: real=%+v naive=%+v", g, i, op, resA, resB)
				}
				t.Logf("group=%d op=%d %s -> %+v err=%v", g, i, op, resA, errA)
			}

			// 回收弹出次数不超过本次到期的授权数加一。
			expired := 0
			for _, a := range naive.auths {
				if a.expiresAt <= naive.maxNow {
					expired++
				}
			}
			if real.lastReapPops > expired+1 {
				t.Fatalf("group=%d op=%d: reap pops %d > expired %d + 1", g, i, real.lastReapPops, expired)
			}
		}
	}
}
