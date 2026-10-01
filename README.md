# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## constpool：字节码常量池驻留与合并链接器

`constpool` 包（`constpool/pool.go`）提供常量池 `Pool`，支持整数、浮点数
与字符串常量的驻留（Intern）、多池合并（Merge）与按下标取值（Get）。

### 去重口径

- 种类不同的常量永不相同：整数 `1` 与浮点 `1.0` 是两个常量。
- 浮点按位比较：`+0.0` 与 `-0.0` 是两个常量；所有 NaN 视为同一个常量，
  并规范为统一位模式 `0x7FF8000000000000`（`Get` 返回规范化后的常量）。
- 字符串按字节比较。

### 驻留与满池规则

- 构造时给定容量 `C`（`C >= 1`，否则报 `ErrInvalidCapacity`）。
- 首次出现的常量取当前池大小为下标，下标连续无空洞。
- 池满时新常量被拒绝（`ErrPoolFull`），已存在的常量仍可命中并返回原下标。
- 种类未知时报 `ErrUnknownKind`；被拒绝的驻留不改变池。

### 合并规则

- `Merge` 接受另一个池的快照（按源下标序的常量列表），返回由源下标到
  本池下标的重定位表；源快照中重复的常量映射到同一个本池下标。
- 先校验快照内是否存在未知种类（`ErrUnknownKind`），再统计其中在本池
  不存在的不同常量数，若加上当前大小超过 `C` 则整体拒绝
  （`ErrMergeExceedsCapacity`）；按此序只报第一个错误。
- 被拒绝的合并不得有任何新增，即使其中部分常量已存在于本池。
- `Get` 的下标为负或不小于池大小时报 `ErrIndexOutOfRange`。

### 并发与确定性

所有方法可并发调用，内部以互斥锁串行化，结果等价于某个串行顺序；
同一常量并发驻留只得到一个下标；相同的操作序列重放得到完全相同的
下标与重定位表。

### 本地验证

```bash
# 全部测试（含朴素模拟对照、并发与重放确定性）
go test ./constpool/

# 竞态检测 + 详细日志（日志打印每步输入、输出与判定依据）
go test -race -v ./constpool/
```

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
