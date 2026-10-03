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

## 协同编辑锚点追踪器（`tracker` 包）

`tracker` 在带修订号的编辑历史上追踪点锚点与区间锚点，支持任意历史
修订的位置查询与检查点压缩。所有方法可并发调用，结果等价于某个串行
顺序；相同操作序列重放得到完全相同的返回。

### 偏向与依附语义

- 位置是 `0` 到 `L` 的整数“缝隙”（字符之间）。
- 点锚点带偏向：`Left` 依附其左侧字符，`Right` 依附其右侧字符。
- 区间锚点 `[s,e)` 分两种：`Tight` 起点为 `Right`、终点为 `Left`；
  `Loose` 起点为 `Left`、终点为 `Right`。

### 编辑映射

- `Replace(p, d, n)`：在 `p` 删 `d` 个、插 `n` 个。点 `x`：`x<p` 不变；
  `x>p+d` 平移 `n-d`；`p≤x≤p+d` 时 `Left→p`、`Right→p+n`。
- `Move(p, len, q)`：把 `[p,p+len)` 搬到缝隙 `q`（`q<p` 或 `q>p+len`，
  否则 `ErrInvalid`）；`q'=q`（`q<p`）或 `q-len`。属于被搬区间的点
  （`p<x<p+len`，或 `x=p` 且 `Right`，或 `x=p+len` 且 `Left`）变为
  `x-p+q'`；其余点先按删除平移，再按插入点 `q'` 调整，`y=q'` 时
  `Left→q'`、`Right→q'+len`。
- 区间每次编辑后分别映射两端，若 `s'>e'` 则令 `s'=e'`（塌缩是破坏性
  写回的）；`Collapsed` 即当前 `s==e`，非粘滞状态，可因后续插入重新
  展开。

### 修订、查询与拒绝优先级

- 每次成功编辑 `rev` 递增；`Pos(id, asRev)` / `Range(id, asRev)` 从锚点
  的检查点修订起逐个编辑重放（`Apply` 本身不遍历锚点）。
- 查询拒绝只报第一个：`ErrNoAnchor` > `ErrWrongKind` > `ErrFuture` >
  `ErrNotYet` > `ErrCompacted`。
- 编辑拒绝（`ErrInvalid` 优先于 `ErrTooLarge`）不改变 `rev`、文档长度
  与任何锚点。

### 检查点压缩

`Compact(newFloor)`（要求 `floor≤newFloor≤rev`，否则 `ErrBadFloor`）先把
检查点修订小于 `newFloor` 的锚点物化到 `newFloor`（重放总量恰为各被物
化锚点的 `newFloor` 减检查点修订之和），再丢弃修订号小于 `newFloor`
的编辑；创建修订不早于 `newFloor` 的锚点检查点不变。之后对
`asRev<newFloor` 的查询报 `ErrCompacted`。

### 本地验证

```bash
# 单元测试：规格示例、d=0/d>0 四边界、Move 双向全缝隙表、塌缩再插入、
# Compact 前后查询、错误优先级、replayed 计数器
go test ./tracker/

# 随机对照：2000+ 组随机编辑与锚点，对照“逐字符依附”朴素模拟，
# -v 下打印每次操作的输入、输出与判定依据；含并发（-race）与确定性重放
go test -race -v ./tracker/
```
