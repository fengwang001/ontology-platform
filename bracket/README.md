# bracket — 带轮空与退赛级联的单败淘汰赛

`package bracket` 实现标准折叠种子位的单败淘汰赛对阵树，支持轮空、人工登记/更正
胜者、退赛自动判定与级联晋级，并提供可并发调用的查询接口。

## 种子折叠位置

- 选手为种子号 `1..N`（`2 <= N <= 64`）；`B` 为不小于 `N` 的最小 2 的幂。
- 位置序列递归定义：`order(2)=[1,2]`；`order(2m)` 为对 `order(m)` 中每个 `s`
  依次写出 `s, 2m+1-s`。例：
  - `B=4`: `[1,4,2,3]`
  - `B=8`: `[1,8,4,5,2,7,3,6]`
- 首轮第 `i` 场（从 1 起）由序列第 `2i-1`、第 `2i` 个位置对阵；种子号大于 `N`
  者缺席。该折叠保证种子 1 与 2 只可能在决赛相遇。

## 轮空与晋级位置

- 首轮恰好一个位置缺席时，同场选手直接晋级；该场为**轮空场**（`KindBye`），
  胜者已确定且不可 `Report`/`Correct`。
- 共 `log2(B)` 轮。第 `r` 轮第 `i` 场胜者进入第 `r+1` 轮第 `ceil(i/2)` 场：
  `i` 为奇数进左位、偶数进右位。最后一轮胜者为冠军。
- 两个位置都有人（非轮空）才算**就绪**（`Ready`）；只有就绪场能被 `Report`。

## 操作

- `New(n)`：`n` 不在 `2..64` 返回 `ErrSeedOutOfRange`。
- `Report(r,i,w)`：为就绪且无结果的场次人工登记胜者 `w`。
- `Correct(r,i,w)`：仅对**人工**结果生效，把胜者换成另一名参赛者 `w`，新胜者
  进入下一轮同一位置；仅当下一轮对应场次尚无结果时允许，决赛无下一轮、总是允许。
- `Withdraw(s)`：选手 `s` 退赛并留在原位（不重排）。
- `Bracket()`：返回每场 `Match` 快照（`Round/Index/Left/Right/Winner/Kind/Ready`）。
- `Champion()`：决赛有结果时返回冠军，否则 `0`。

拒绝原因（哨兵错误，按顺序只报第一个）：

- `Report`：`ErrMatchNotFound` → `ErrByeMatch` → `ErrAlreadyDecided` →
  `ErrNotReady` → `ErrNotAParticipant`。
- `Correct`：`ErrMatchNotFound` → `ErrByeMatch` → `ErrNoResult` →
  `ErrTechnicalResult` → `ErrNotAParticipant` → `ErrAlreadyWinner` →
  `ErrNextRoundDecided`。
- `Withdraw`：`ErrSeedOutOfRange` → `ErrAlreadyWithdrawn` →
  `ErrAlreadyEliminated` → `ErrChampionDecided`。
- 被拒绝的操作不改变任何场次、退赛标记与判定结果。

## 退赛级联判定

每次 `Report`、`Correct` 或 `Withdraw` 成功后，反复扫描所有**就绪且未登记**的
场次直到不动点：

- 只有一方已退赛 → 另一方晋级；
- 双方都已退赛 → 种子号小者晋级。

自动判定结果标记为 `KindTechnical`（技术判定），其败者记为已淘汰；技术判定场
不可 `Correct`。判定产生的胜者会立即填入下一轮位置并继续触发新就绪场次的判定。

## 并发语义

所有读写都在同一把互斥锁下完成，并发调用的结果等价于某个串行顺序；每场至多
一个胜者且胜者必为该场参赛者。

## 本地验证

```bash
# 全量测试
go test ./...

# 竞态检测（并发测试）
go test -race ./...

# 查看对拍日志：输入操作、接受/拒绝输出与判定依据
go test -run TestRandomDifferential2000 -v ./bracket/

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -func=coverage.out

# 格式与静态检查
gofmt -l .
go vet ./...
```

测试覆盖：

- `N = 2, 3, 6, 8, 64` 的首轮与轮空（含 N=6 次轮
  “1 vs (4/5 胜者)”“2 vs (3/6 胜者)”）；
- 从首轮登记到冠军的完整流程、决赛更正；
- `Correct` 下一轮无结果时新胜者进入同一位置、有结果时被拒；
- 对手未定时退赛，对手一就绪即自动晋级并继续级联；双方退赛种子小者晋级；
- 技术判定不可更正、被拒绝操作不改变状态；
- `N=2..64` 逐场与折叠公式及朴素参考实现对照；
- 2000 组随机 `Report`/`Correct`/`Withdraw` 序列与独立参考实现对拍
  （`TestRandomDifferential2000`，固定随机种子可重放）；
- 高并发争用的不变量检查（配合 `-race`）。
