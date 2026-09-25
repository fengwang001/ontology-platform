# MCS 队列自旋锁 NOTES

## 六步分步表（A、B、C 依次 Acquire 再依次 Release）

| 步 | 操作 | tail | 队列链接 | 持有者 | 正在自旋 |
|---|---|---|---|---|---|
| 1 | A Acquire | na | na(locked=false) | A | 无 |
| 2 | B Acquire | nb | na→nb | A | nb |
| 3 | C Acquire | nc | na→nb→nc | A | nb, nc |
| 4 | A Release(na) | nc | nb→nc（na 脱离） | B | nc |
| 5 | B Release(nb) | nc | nc（nb 脱离） | C | 无 |
| 6 | C Release(nc) | nil | 空 | 无 | 无 |

- (甲) 第 4 步授予 B（FIFO 队首）。若用单标志 test-and-set：B、C 都抢同一标志，谁先 CAS 成功归谁，可能 C 先于 B，授予顺序 A→C→B，乱序。
- (乙) 若 A 读到 `na.next==nil` 就直接返回：B 已 swap tail（tail=nb）但尚未链 `na.next`，A 返回后 B 链上 `na.next=nb` 并自旋 `nb.locked`，而它永远为 true——无人清，B 饿死，C 随之饿死。正确实现：A 的 `CAS(tail: na→nil)` 失败（tail 已是 nb），于是自旋等 `na.next` 变非 nil，再置 `nb.locked=false`，交接成功。
- (丙) 交接清的是后继的 `locked`。若 Release 清自己的 `locked`：na.locked 本就是 false，清了等于没清；nb.locked 仍 true，B 永远自旋——等待者只读自己的 locked，只有前驱的 Release 能清它，前驱清错对象即饿死。

## 四条不变量落点

1. 与朴素参照一致：`api.SelfCheck` 内逐操作对比 sync.Mutex 版参照；测试 `TestGrantOrderMatchesNaive`。
2. 互斥：获锁唯一路径是「swap 到 nil」或「前驱清我的 locked」，见 `qlock.Acquire`；测试 `TestConcurrentMutualExclusion`。
3. FIFO 公平：授予严格沿 next 链交接，见 `qlock.Release`；测试 `TestFIFOFairness`。
4. 失败不留痕：所有校验先于任何写，见 `qlock.Release`/`Acquire` 前置检查；测试 `TestRejectedOpsLeaveNoTrace`。
