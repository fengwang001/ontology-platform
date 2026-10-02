# dirtyflush — 按首次弄脏 LSN 排序的缓冲池刷写管理器

`dirtyflush.Manager` 维护缓冲池中脏页的刷写次序与页间写依赖，支持
`Modify`、`SetFlushed`、`FlushStart`、`FlushDone`、`AddDep` 交错执行，
并能推导检查点 LSN（`Checkpoint`）与生成带前置依赖的刷写计划（`Plan`）。
所有操作以一把互斥锁串行化，因此并发调用的结果等价于某个串行顺序，且
被拒绝的操作不会改变任何状态（原子判定、原子生效）。

构造：`m, err := dirtyflush.New(D)`，`D` 为脏页上限（1..10^6）。
页号范围 0..10^6；LSN 范围 1..10^15（`SetFlushed` 的水位允许 0）。
被拒绝时返回的 `*dirtyflush.OpError` 携带可区分的 `Reason` 字段
（见 `manager.go` 中的 `Reason*` 常量），判定顺序严格按照下文规则。

## p.lsn 与 oldest 的区别

- `p.lsn`：页的**最新**一次被接受的 `Modify` LSN，每次成功 `Modify(p, lsn)`
  都会更新为 `lsn`。它用于刷写安全性检查（`p.lsn <= 已落盘水位`）。
- `oldest`：页在当前脏周期内的**首次弄脏 LSN**，决定该页在刷写链表中的
  位置。普通的再次修改不会改变 `oldest`，因此同一页反复修改不会“插队”。
- 不变量：`oldest <= p.lsn`；刷写链表只包含脏页（含在途页），按
  `(oldest, 页号)` 严格升序排列（页号作为同值时的确定性次序）。

## 在途（flush in flight）期间修改的处理

- `FlushStart(p)`：要求 `p` 脏、不在途、`p.lsn <= 已落盘水位`、所有前置页
  （存在边 `q→p` 的 `q`）此刻都不脏（在途也算脏）。成功后记录快照
  `snap = p.lsn` 并置在途；页仍留在链表中。
- 在途期间 `Modify(p, lsn)`：`p.lsn` 照常推进，`oldest` 不变；若
  `firstAfter` 为空则置为本次 `lsn`（记录在途期间的**第一次**修改）。
- `FlushDone(p)`（仅要求页在途）按比较结果二选一：
  - `p.lsn == snap`：刷写期间无新修改。页变干净、离开链表，`oldest`/
    `firstAfter` 清空，并删除所有从 `p` 发出的依赖边（入边自然失效，因为
    `p` 已不脏；若 `p` 之后再次变脏可重新登记）。
  - `p.lsn != snap`：刷写期间有新修改。页保持脏、退出在途，`oldest` 改为
    `firstAfter`，并按新 `oldest` **升序插回链表的相应位置**（可能插到
    链表中间，而不是末尾），`firstAfter` 清空。从 `p` 发出的边保留。

## 前置依赖的登记与解除

- `AddDep(a, b)` 表示“`a` 必须先于 `b` 刷写”：
  - `a == b` 属于参数非法；
  - 仅当 `a` 此刻为脏才登记；`a` 干净时返回成功但不落边；
  - 重复登记幂等；
  - 若新边会成环（已存在从 `b` 到 `a` 的路径，含间接路径）则以
    `ReasonCycle` 拒绝。
- 解除规则：依赖边只从脏页发出。页在 `FlushDone` 变干净时，其全部
  **出边**被删除；图始终保持无环。`FlushStart(b)` 时若任何前置 `q` 仍为
  脏（包括在途），以 `ReasonPredecessorDirty` 拒绝。

## 拒绝判定顺序（只报第一个）

- 参数非法最先：`New` 的 `D` 越界；页号越界；LSN/`target` 越界；
  `AddDep` 的 `a == b`。
- `Modify`：① LSN 不严格大于已接受的最大 Modify LSN（`SetFlushed`
  回退同属 `ReasonLSNTooSmall`）；② 脏页已满——仅当页干净且脏页数
  已等于 `D`（已脏页在满时仍可继续修改）。被拒 `Modify` 不推进最大 LSN。
- `FlushStart`：① 页不脏；② 页已在途；③ `p.lsn > 已落盘`（恰等于允许）；
  ④ 存在脏的前置页。
- `FlushDone`：页不在途（`ReasonNotInFlight`）。
- `AddDep`：成环（`ReasonCycle`）。

## Checkpoint 与 Plan 的推导

- `Checkpoint()`：返回链表首页的 `oldest`（含在途页）；链表为空时返回
  “已接受的最大 Modify LSN + 1”，从未 `Modify` 时即为 1。因此
  `Checkpoint` 不大于任何脏页的 `oldest`。
- `Plan(target)`（`target >= 1`，只读、不改任何状态）：
  1. 按链表次序遍历 `oldest < target` 且**不在途**的页；
  2. 对每个尚未入计划的页 `p` 执行 `emit(p)`：先收集 `p` 的所有“脏且
     不在途”的前置页 `q`，按**页号升序**逐个检查，轮到时仍未入计划才
     `emit(q)`（递归展开），然后把 `p` 追加进计划；
  3. 前置页即使 `oldest >= target` 也会被拉入；在途前置不被拉入（其刷写
     已在进行）；已入计划的页不会重复出现。

## 实现要点

- 链表用随机平衡树 treap（`treap.go`）维护 `(oldest, 页号)` 键，插入到
  任意位置、删除、取首均为 O(log D)；优先级生成器是确定性的，因此相同
  操作序列在任何机器上重放都得到完全相同的树形态、链表与计划。
- 依赖图同时保存出边邻接 `out` 与入边邻接 `in`；成环检测沿出边 DFS。
- 只读辅助：`Snapshot(p)`、`Order()`、`OrderEntries()`、`DirtyCount()`、
  `Flushed()`、`MaxModify()`、`OutEdges(a)`、`CheckInvariants()`。

## 本地验证

在仓库根目录（Go 1.26+；若 `go` 不在 PATH，先 `export PATH=$PATH:/usr/local/go/bin`）：

```bash
# 全部测试（含 2000 组与朴素模型逐操作对照的随机差分测试）
go test ./...

# 竞态检测
go test -race ./...

# 详细用例
go test -race -v ./dirtyflush

# 把 2000 组随机序列的“输入 / 输出 / 判定依据”完整日志落盘
DIRTYFLUSH_DIFFLOG_DIR=/tmp/difflogs go test -run TestRandomDifferential -v ./dirtyflush

go vet ./...
gofmt -l .
```

测试组成：

- `manager_test.go`：题述示例逐步复现 + 全量不变量校验。
- `directed_test.go`：脏页改 oldest 不变、在途修改后按 oldest 插回链表
  中间、无修改变干净并删出边、干净 `a` 不登记、幂等、直接/间接成环、
  `FlushStart` 四类拒绝先后、水位恰等于 `p.lsn` 允许、在途前置阻塞、
  空链表 Checkpoint、Plan 拉入高 oldest 前置/按页号展开/跳过在途与已入
  计划页、满员只拒干净页、被拒不改状态，以及 8 goroutine 的并发与不变量
  巡检（配合 `-race`）。
- `naive_test.go` + `diff_test.go`：与生产代码完全独立的朴素模拟
  （排序切片链表 + map 依赖图），对 2000 组随机操作序列逐步比对返回结果、
  拒绝原因、snap、Plan、Checkpoint，以及链表/每页状态/边/水位的完整快照。
