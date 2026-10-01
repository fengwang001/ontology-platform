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

## 常量池（`constpool`）

`constpool` 包实现线程安全的字节码常量池驻留与合并链接器。

### 去重口径

- 常量带种类（`KindInt` / `KindFloat` / `KindString`），**种类不同永不相同**：
  整数 `1` 与浮点 `1.0` 是两个常量。
- 整数按 64 位值比较。
- 浮点**按位比较**（`math.Float64bits`）：`+0.0` 与 `-0.0` 是两个常量；
  **所有 NaN 视为同一个常量**，取值时统一规范为位模式 `0x7FF8000000000001`。
- 字符串按字节比较。
- 首次出现的常量取当前池大小为下标，因此下标连续、无空洞。

### 满池与合并规则

- `New(C)`：`C < 1` 返回 `ErrInvalidCapacity`。
- `Intern(c)`：先查命中，命中则返回原下标（**池满也能命中**）；
  未命中且池已满返回 `ErrPoolFull`；种类非法返回 `ErrUnknownKind`
  （种类校验先于容量校验）。被拒绝的操作不改变池。
- `Get(i)`：`i < 0` 或 `i >= Len()` 返回 `ErrIndexOutOfRange`，
  否则返回规范化后的常量。
- `Merge(snapshot)`：
  1. 先按源下标序校验，快照内含未知种类则报 `ErrUnknownKind`（只报第一个）；
  2. 统计快照中**本池尚不存在的不同常量**个数 `M`；
  3. 若 `Len() + M > C`，整体拒绝并返回 `ErrPoolFull`，**即使部分常量已在本池中也不新增任何条目**；
  4. 否则按源下标序逐个驻留，返回与快照等长的重定位表（源下标 → 本池下标）；
     快照内重复的源常量映射到同一个本池下标。

### 并发语义

`Intern` / `Merge` / `Get` 由互斥锁串行化，结果等价于某个串行顺序；
同一常量并发驻留只产生一个下标；相同操作序列重放得到完全相同的下标与重定位表。

### 本地验证

```bash
# 常量池全部用例（含竞态检测，日志打印输入、输出与判定依据）
go test -race -v ./constpool

# 全量测试与静态检查
go test -race ./...
go vet ./...
gofmt -l .
```

测试覆盖：整数 `1` 与浮点 `1.0` 分开、`+0.0`/`-0.0` 分开、
不同位模式 NaN 合并为一个、满池命中已有常量与拒绝新常量、
合并部分命中部分新增（恰好放下与超一个）、重定位表对源内重复常量的映射、
并发驻留与混合并发，以及与按相同规则实现的朴素模拟器的 3000 步随机重放对照
（逐步核对下标、重定位表、错误与池内容）。
