# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

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

## 顺序预读窗口状态机（readahead.go）

包 `ontology` 提供带有限页缓存与抖动（thrashing）回退的顺序预读窗口状态机
`ReadAhead`，用于让每次读操作实际发起的需求读页、预读页与被淘汰页集合可精确复现。

### 构造

```go
ra, err := ontology.NewReadAhead(N, I, M, Cp)
```

- `N` 文件总页数（N ≥ 1）、`I` 初始窗口（I ≥ 1）、`M` 最大窗口（M ≥ I）、`Cp` 缓存容量（Cp ≥ 1）。
- 参数非法返回 `ErrInvalidArgument`，可用 `errors.Is` 判定。

初始状态：缓存为空（每页带「预读未读」标记 `pf`）、`prev = -1`、无窗口
（`wsz = 0`，此时无标记页 `mk`）。

### 顺序判定

`Read(p, n)` 读页 `[p, p+n)`。当且仅当 `p == prev+1` 时视为顺序访问；
首次读 `p = 0` 也满足（`0 == -1+1`）。参数先校验非法（`p < 0` 或 `n < 1`，
`ErrInvalidArgument`），再校验越界（`p+n > N`，`ErrOutOfRange`），只报第一个；
被拒绝的调用不改变任何状态。

### 同步预读（存在 missing 且顺序）

- `missing` 中的页按升序作为**需求读**发起。
- 新窗口大小 `s = min(M, max(I, 2*wsz, n))`（`wsz` 取本次读之前的值）。
- 预读起点 `rs = p+n`；`rs < N` 时对 `[rs, min(rs+s, N))` 中读之前未缓存的页
  发起**预读**，窗口记为 `(rs, s)`，标记页 `mk = rs + floor(s/2)`；
  `s` 不因被 `N` 截断而改变（`mk` 也可能落在 `[0,N)` 之外）。
- `rs >= N`：不预读且窗口清空。非顺序访问：不预读且窗口清空。

### 异步预读（全部命中）

仅当存在窗口且标记页落在请求区间 `p <= mk < p+n` 时触发：

- `s = min(M, max(I, 2*wsz))`，起点 `rs = ws + wsz`。
- `rs < N`：对 `[rs, min(rs+s, N))` 中未缓存的页预读，窗口更新为 `(rs, s)`，
  `mk = rs + floor(s/2)`；`rs >= N` 时窗口清空。
- `mk` 不在请求内则不做任何事，窗口保持不变。

### 缓存更新、LRU 淘汰与抖动收缩

每次读按固定次序更新缓存（决定可复现的淘汰结果）：

1. 按升序处理请求页：已缓存的页变为最近使用（MRU）并清除 `pf`；
   需求读的页以 MRU 插入且 `pf = false`。
2. 按升序把预读页以 MRU 插入且 `pf = true`。
3. 缓存页数超过 `Cp` 时，从最久未使用（LRU）端逐页淘汰直到不超过 `Cp`
   （本次刚读/刚预读的页也可能被淘汰）。
4. 若被淘汰页中至少一页 `pf = true`，且此刻 `wsz > 0`，则
   `wsz = max(1, floor(wsz/2))`（`ws`、`mk` 不变）；窗口已清空时淘汰
   `pf` 页不改变窗口。

最后 `prev = p+n-1`。`Read` 返回 `(需求读页, 预读页, 被淘汰页)`：
前两者升序，第三者按淘汰次序。

- `DropCache()` 清空缓存与全部 `pf`，但保留 `prev`、窗口与标记页。
- `State()` 返回 `prev`、窗口 `(ws, wsz)` 与标记页 `mk`。
- `Read`、`DropCache`、`State` 内部互斥，可并发调用，结果等价于某个串行顺序。

不变量：任何时刻缓存页数 ≤ `Cp`；需求读页与预读页互不相交且都在 `[0,N)` 内；
`wsz ≤ M`；两次 `DropCache` 之间同一页被再次发起读之前必已被淘汰；
相同读序列重放结果完全一致。

### 本地验证

```bash
# 全量测试
go test ./...

# 竞态检测
go test -race ./...

# 查看对照日志（输入、输出、判定依据）
go test -run TestCompareNaiveLogged -v
go test -run TestRandomCompareNaive2000 -v
```

测试覆盖：首次 `p=0` 顺序判定；I=4/M=16 下窗口 4→8→16 倍增封顶；
标记页边界（读它触发、读前一页不触发）；请求跨过标记页；`n > I`；
`rs >= N` 清窗；随机访问清窗后从 `max(I,n)` 重启；截断窗口保留未截断 `s`；
`DropCache` 后标记页走未命中分支；LRU 命中影响淘汰对象；本次刚读页被淘汰；
`pf` 命中后淘汰不收缩、未读即淘汰收缩；`wsz=1` 收缩地板；无窗口淘汰 `pf`
不改窗口；拒绝调用不改状态；并发安全；确定性重放；以及 2000 组随机序列与
独立朴素模拟器（`readahead_test.go` 中的 `naiveRA`）逐步对照。
