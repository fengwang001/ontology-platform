# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 热门元素统计（`ontology` 包，包名 `hotcount`）

`ontology/sketch.go` 实现了一个近似热门元素统计结构：接收带计数的元素到达
流，维护出现次数最多的若干候选。所有方法（`Add` / `Estimate` /
`Candidates` / `SelfCheck`）均通过读写锁保护，可被多个执行体并发调用。

### 哈希定位

- 草图为 `Rows × Width` 的 `uint64` 二维表，见 `ontology/hash.go`。
- 每行使用相互独立、确定性的哈希：先把行号混入 FNV-1a-64 初始状态，再对
  元素的 8 个小端字节做 FNV-1a，最后对 `Width` 取模得到列号。
- 哈希不依赖随机种子，因此同一输入在任意进程中都定位到相同格子，候选
  结果可复现。

### 估计规则

- `Add(element, count)`：在每行定位到的格子上累加 `count`。
- `Estimate(element)`：取该元素各行格子计数的最小值。碰撞只会把他人的
  计数并入，因此估计值恒不低于真实累计次数（只高估、不低估）。

### 候选排名与维护

- 候选为 `{Element, Estimate}`，全序规则为：估计值降序；估计值相同时按
  元素值升序。`Candidates()` 返回该顺序的副本。
- 每次合法到达后：
  1. 元素已在候选中：仅刷新它的记录估计值并重新排序；
  2. 否则列表未满：直接插入并排序；
  3. 否则与当前最差（列表末尾）候选比较，仅当新元素排名更高时替换；
  4. 其余候选不会因为别的元素到达而刷新——碰撞导致的陈旧只会在其自身
     再次到达时更新。

### 边界与错误类别

以下错误互为不同的哨兵值，可用 `errors.Is` 区分；任何一次被拒绝的到达
都发生在写入之前，草图与候选列表保持不变（失败不留痕）：

- `ErrInvalidConfig`：`Rows`、`Width`、`MaxCandidates` 任一非正。
- `ErrElementOutOfRange`：元素大于 `Config.MaxElement`（`Add` 与
  `Estimate` 均会校验）。
- `ErrNonPositiveCount`：到达次数为 0。
- `ErrCountOverflow`：任一行格子加上本次计数会溢出 `uint64`；先做
  全部格子的溢出预检，通过后才写入。
- `ErrSelfCheck`：`SelfCheck` 发现内部不变量被破坏（表尺寸、候选数量、
  元素范围、重复候选、记录估计值大于实时估计值、排序错乱）。

### 测试与本地验证

测试（`ontology/sketch_test.go`）覆盖：

- 碰撞与重复到达：估计值逐点不低于精确计数；
- 并列排名：同估计值按元素升序，等权新元素不替换；
- 替换与刷新：超容时替换最差候选、低权到达不改变列表、已在候选中刷新
  并重排；
- 非法输入：构造参数非法、元素越界、零次数、溢出，五类错误互不相同，
  且拒绝后草图与候选状态保持不变；
- 候选与朴素线性参照逐步一致：每一步用相同的“仅刷新到达元素 / 未满插入 /
  否则比较替换”规则重放参照实现并逐元素比对；
- 并发：多执行体混合调用四个方法，配合 `-race` 检测。

日志中逐步打印每次输入、到达元素的估计值、是否进入候选的判定依据以及
候选快照。

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
