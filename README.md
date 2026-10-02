# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 两区缓冲池 LRU 管理器

`ontology.New(N, rho, Tol, T)` 创建一个线程安全的两区 LRU 缓冲池
（实现见 `ontology/lru.go`）：

- 构造参数：容量 `N`（4–10^6）、老年区目标百分比 `rho`（5–95）、容差
  `Tol`（0–N）、晋升停留时长 `T`（0–10^9）。
- 池内部分为年轻区 `Y` 与老年区 `O` 两段双向链表，整体 LRU 次序为
  `Y` 头到尾再接 `O` 头到尾。`Y` 的头是最近使用端（MRU），`O` 的尾是
  淘汰端。每页记录首次访问时刻 `first`（可为空）与钉住计数 `pin`
  （初值 0），且只存在于 `Y`、`O` 之一。
- 目标大小：池中页数 `len` 时 `tgt = floor(len*rho/100)`，恒保持
  `tgt <= |O| <= tgt+Tol`。

### Rebalance（双向规则与容差）

每次接受的结构变更（缺页插入后、老年页晋升后）恰好执行一次：

1. 若 `|O| < tgt`，反复把 `Y` 的尾移到 `O` 的头，直到 `|O| == tgt` 或
   `Y` 为空（此分支不保证达到下界，但插入序列下该情况不发生）。
2. 若 `|O| > tgt+Tol`，反复把 `O` 的头移到 `Y` 的尾，直到
   `|O| == tgt+Tol`。

`Tol == 0` 时新页可能在插入当次就被移到 `Y` 的尾；`Tol > 0` 时新页可
以留在 `O`。

### 首次访问时刻与晋升条件

- `Access` 缺页读入的新页放到 `O` 的头并令 `first = now`（停留时长从
  本次算起）。
- 命中于 `Y`：移到 `Y` 的头，不改 `first`。
- 命中于 `O`：`first` 为空（预取页的第一次命中）时只记录 `first=now`，
  不移动；否则当且仅当 `now-first >= T` 时晋升到 `Y` 的头并 Rebalance。
  `T == 0` 时老年页命中即晋升（`now` 相同也满足）。

### 预取与牺牲选择次序

- `Prefetch(page, now)`：页已在池中则什么都不做（仍推进时钟）；否则按
  缺页处理，但新页 `first` 为空。
- 池满缺页时先淘汰再插入（之后只 Rebalance 一次）。牺牲者选择：从
  `O` 的尾向头找第一个 `pin == 0` 的页；`O` 中没有再从 `Y` 的尾向头
  找；都没有则拒绝（`ErrAllPinned`），且不推进时钟、不改任何状态。
- `Pin`/`Unpin` 只增减 `pin`，不带 `now`，不推进也不检查时钟。被淘汰
  页的 `first` 与 `pin` 随删除一并清除。

### 拒绝原因（按此顺序只报第一个）

1. 参数非法：构造参数、页号 `0..10^6`、`now` 范围 `0..10^15`
   （`ErrInvalidArgument`）
2. 时钟回退：`now` 小于已接受操作的最大时刻（`ErrClockSkew`）
3. 页不在池中：仅 `Pin`/`Unpin`（`ErrNotFound`）
4. 解钉下溢：`Unpin` 时 `pin` 已为 0（`ErrPinUnderflow`）
5. 全部被钉：池满缺页且每页 `pin > 0`（`ErrAllPinned`）

被拒绝的操作不改变链表、`first`、`pin` 与最大 `now`。所有方法用单一互
斥锁串行化，可并发调用，结果等价于某个串行顺序。

### API

- `Access(page, now) (AccessResult, error)` / `Prefetch(page, now)`：
  `AccessResult{Hit, Evicted, Page}` 报告是否命中、是否发生淘汰及被淘汰
  页号，可精确复现淘汰序列。
- `Lists() (young, old []int)`：两段列表，均为头到尾。
- `Pin(page)` / `Unpin(page)`、`Len()`、`Evictions()`（淘汰序列副本）。

### 本地验证

```bash
# 全量测试（若 GOCACHE 在只读目录，可先 export GOCACHE=/tmp/gocache）
go test ./...

# 竞态检测（含 8 goroutine 并发压力测试）
go test -race ./...

# 单测详情与朴素模拟对照（2000 组随机序列）
go test -v ./ontology
go test -run TestDifferentialNaive ./ontology
```

`TestDifferentialNaive` 将每个操作的输入、双方输出与判定依据（命中分
区、`now-first` 与 `T` 比较、牺牲者来自 O/Y 尾扫描、拒绝类别等）写入
`ontology/lru_difftest.log`，并与按规则逐步实现的朴素切片模拟逐字对
照；每个序列结束后以相同操作序列重放一次，校验链表与淘汰序列完全一
致；同时每步校验“读入总次数 = 淘汰次数 + 当前池中页数”、页互不相同
且总数不超过 `N`、每页恰在一区、`tgt <= |O| <= tgt+Tol` 等不变量。

## 环境要求

- Go 1.26+（`go version` 确认）

## 运行

```bash
# 拉取依赖
go mod tidy

# 直接运行
go run ./cmd/server

# 编译后运行
go build -o bin/server ./cmd/server
./bin/server
```

## 测试

```bash
# 全量测试
go test ./...

# 带竞态检测与详细输出
go test -race -v ./...

# 单个包 / 单个用例
go test ./ontology
go test -run TestObjectType ./ontology

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
