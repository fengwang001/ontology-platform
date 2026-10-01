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

## 重命名检测器（`rename` 包）

`rename.Detector` 在一次变更的「删除文件」与「新增文件」之间，按固定的
两个阶段与确定性排序规则配对重命名。结果只取决于登记内容，与登记顺序无关。

### 用法

```go
d, err := rename.NewDetector(T, Cap) // T∈[1,100]，Cap>=1
err = d.RegisterDeleted("old/path", []byte("..."))
err = d.RegisterAdded("new/path", []byte("..."))
result := d.Detect() // 首次调用计算并冻结会话，之后返回同一 *Result
// result.Renames         []Rename{{Source, Target, Score}} 按 Source 升序
// result.UnpairedDeleted / UnpairedAdded 各自升序，永不为 nil
```

### 行切分与相似分

- 内容按 `'\n'` 切行，每行**包含**结尾的 `'\n'`；没有结尾换行的最后一段
  也算一行，因此 `"a\n"` 与 `"a"` 是不同的行；空内容为零行。
- 对每种不同的行，取两侧出现次数的较小值乘以该行字节数，求和得公共字节数 C。
- 相似分 `s = floor(C × 100 / max(len(删除), len(新增)))`（整数除法，向下取整，
  所以 `floor(99.9)=99`）。

### 阶段一：精确配对（分记 100）

1. 按逐字节相同的内容分组。
2. 组内先按文件名（路径中最后一个 `'/'` 之后的部分）分子组；仅两侧都出现的
   文件名子组参与配对，子组内两侧各按**完整路径字节序**排列后依次一一配对，
   直到一侧用完。
3. 组内剩余的删除/新增文件再各自按完整路径字节序排列，依次一一配对。

### 阶段二：相似配对

只考察阶段一后仍未配对的全部删除×新增组合：

1. 丢弃 `s < T` 的组合（`s == T` 保留）。
2. 候选按排序键 `(s 降序, 文件名相同优先于不同, 删除路径升序, 新增路径升序)`
   稳定排序，依次贪心取出；任一端已被占用则跳过。

### 剪枝依据

因为 `C ≤ min(len(删除), len(新增))`，所以
`s ≤ floor(min×100/max)`。当该上界已经 `< T` 时，该组合不可能入选，
**不计算 C**（两文件都为空时也跳过）。包内非导出计数器
`commonBytesCalls` 记录 C 的实际计算次数，测试证明它恰等于未被剪枝的
阶段二组合数；`computeCalls` 证明 `Detect` 结果只计算一次。

### 错误与优先级

- 构造：`T` 不在 `[1,100]` 返回 `ErrThresholdOutOfRange`，先于
  `Cap < 1` 的 `ErrCapOutOfRange`。
- 登记按以下顺序只返回第一个错误：会话已冻结 `ErrFrozen` → 路径为空
  `ErrEmptyPath` → 路径已登记（含另一侧）`ErrPathExists` → 总数已达 Cap
  `ErrCapacityExceeded`。被拒绝的登记不改变会话。
- 所有方法可并发调用（内部互斥），语义等价于某个串行顺序；重复或并发
  `Detect` 返回同一个 `*Result`。没有任何登记时 `Detect` 返回空结果而非错误。

### 本地验证

```bash
# 规则用例 + 并发 + 2000 组随机对拍（对拍含每组输入/输出/判定日志）
go test -race -v ./rename

# 仅看 2000 组对拍及其剪枝统计
go test -v ./rename -run TestDifferential2000

go vet ./...
gofmt -l .
```

对拍基线 `naiveDetect` 是按同一规则独立编写的朴素实现，阶段二**不做剪枝**；
每组随机输入都同时校验：优化实现与朴素实现结果一致、打乱登记顺序结果一致、
`commonBytesCalls` 等于未被剪枝的组合数。
