# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## btree：按字节占用判定下溢的两层 B+ 树

`btree` 包实现一个只有叶页一层的 B+ 树（`btree.New(capacity, pageLimit)`），
叶页按键序排成页序，支持并发调用，所有操作等价于某个串行顺序。

### 字节口径

- 页容量 `C`（8 到 10^6），页数上限 `P`（1 到 10^6）。
- 最小占用 `M = ceil(C/2)`，单条记录最大尺寸 `Emax = floor(C/4)`。
- 记录 = 非空字节串键 + 尺寸 `e`（1 到 Emax）；页的占用是其全部记录尺寸之和。
- 删除后页占用小于 `M` 即为下溢；只剩一页时下溢不处理。
- 除页序中最左页外，每页的分隔键恒等于该页当前首键，每次操作完成后重算。
- 页编号从 1 起按创建次序递增，已释放的编号永不复用。

### 切分与借位的取法

- **切分**：页占用超过 `C` 时，对含新记录的 `n` 条记录（`n>=2`）选切分位置
  `j`（左半为前 `j` 条），使左右两半占用之差的绝对值最小；差相等取较小的 `j`。
  右半成为新页（取下一个编号），紧接在原页之后；切分后允许任一页小于 `M`。
- **借位**：从借出方（左邻取尾部、右邻取头部）起取**最少的 t 条**（`t>=1`），
  使下溢页占用达到 `M`，且取走后借出方占用仍不小于 `M`（恰等于 `M` 允许）。
  左借的记录按原序放到下溢页开头，右借放到末尾。

### 删除再平衡的固定次序

下溢页 `U` 只按下列次序处理**一次、不级联**：

1. **左借**：`U` 有左邻 `L` 且能借，则从 `L` 尾部借。
2. **右借**：`U` 有右邻 `R` 且能借，则从 `R` 头部借。
3. **并左**：`L` 存在且两者占用之和不大于 `C`（恰等于 `C` 允许），
   `U` 的记录并入 `L` 末尾，`U` 被释放。
4. **并右**：`R` 存在且占用之和不大于 `C`，`R` 的记录并入 `U` 末尾，`R` 被释放。

合并后存活的总是页序中靠左的那页，编号不变。四步都不满足则保持下溢，不算错误。

### 拒绝顺序

参数非法（构造参数越界、键为空串、`e` 不在 1 到 Emax）统一最先拒绝；
其后按序只报第一个：Insert 键已存在（`ErrKeyExists`）、Delete 键不存在
（`ErrKeyNotFound`）、Insert 需要切分而页数已等于 `P`（`ErrPageLimit`）。
被拒绝的操作不改变任何页、分隔键与编号计数（页数不足时不消耗页编号）。

### 本地验证

```bash
# 全部测试（含 2000 组随机序列与朴素模拟对照）
go test ./btree/

# 详细日志：每组随机操作打印输入、输出与判定依据
go test -v -run TestRandomAgainstNaiveSimulation ./btree/

# 竞态检测
go test -race ./btree/
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
go test ./btree
go test -run TestPromptExample1 ./btree

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
