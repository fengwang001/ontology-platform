# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## Inode 布局管理器

根包提供小文件数据与扩展属性共享 inode 区域的布局管理器：

```go
manager, err := ontology.NewManager(A, X, Bs, P)
```

四个构造参数分别是 inode 内区域大小 `A`、外部属性块容量 `X`、数据块大小 `Bs`、块池总块数 `P`，任一小于 1 都会返回 `ErrInvalidArgument`。

### 属性占用与放置

- 属性名为 1 到 255 字节，属性值为 0 到 4096 字节；同名 `SetXattr` 覆盖旧值。
- 单个属性占用 `4 + roundup4(len(name)) + roundup4(len(value))` 字节，`roundup4(0) == 0`。
- 属性按名字字节序升序尝试放入 inode；能完整放入当前剩余空间才放入。
- 一旦遇到第一个放不下的属性，该属性及其后的全部属性都放入外部属性集，即使后续属性更小。
- 内联模式的 inode 剩余空间是 `A-size`，块模式是 `A`。
- 外部属性集是非空 `(名字, 值)` 的有序序列；外部放置总字节必须不超过 `X`。
- 外部属性集逐字节相同的文件共享同一个外部块；空外部属性集不占块。

### 模式转换

- 新文件为内联模式，大小 0，无属性。
- 内联模式下，`Resize` 到不超过 `A` 且属性放置仍合法时保持内联；尺寸超过 `A` 或属性在 inode 内放置不下时转块模式。
- 块模式下只有 `Resize` 到 0 才回内联；缩小到不大于 `A` 不会自动回内联。
- `SetXattr`、`RemoveXattr` 后，若内联放置不合法则转块模式；块模式仍放不下时拒绝操作。
- 内联数据不占数据块；块模式数据占 `ceil(size/Bs)` 块，大小为 0 时为 0。

### 块池与共享

块池占用为所有文件的数据块数之和，加上全体文件中互不相同的非空外部属性集数量。`Stat` 返回：

- 当前模式、大小、数据块数。
- 外部属性放置字节数。
- 外部属性集编号：与当前文件外部属性集相同的最小文件编号；空集为 0。
- 按属性名字节序排列的 inode 内 / 外部落点。

每个变更操作完成后都会按共享后的结果重新计算池占用；占用恰等于 `P` 允许，超过则回滚整个操作，`Clone` 被拒绝时不消耗新文件编号。

### 错误顺序

错误哨兵可用 `errors.Is` 区分，按以下顺序只返回第一个：

1. `ErrInvalidArgument`：构造参数、大小、属性名或值长度非法。
2. `ErrFileNotFound`：文件编号不存在。
3. `ErrNoAttribute`：`GetXattr` 或 `RemoveXattr` 的属性不存在。
4. `ErrAttributeTooBig`：属性在允许模式下仍无法合法放置。
5. `ErrPoolFull`：操作完成后的共享块池占用超过 `P`。

所有公开操作都由管理器内部互斥保护，并发结果等价于某个串行执行顺序，拒绝操作不会留下部分状态。

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

# 若 Go 默认缓存目录只读，可显式指定缓存
GOCACHE=/tmp/go-cache go test -race -v ./...

# 单个包 / 单个用例
go test .
go test -run 'TestRandomSequencesAgainstNaiveModel' .

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
