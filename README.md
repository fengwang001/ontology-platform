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

## 流式 B+ 树批量装载器（`bptree` 包）

`bptree` 包从严格递增的键流自底向上批量装载一棵只读 B+ 树。

### 构造参数与占用目标

`New(C, B, p)`：叶页容量 `C`（至少 2）、内部页最大子节点数 `B`（至少 3）、
填充百分比 `p`（1–100，整数）。派生量为：

- 叶页最小键数 `mL = floor(C/2)`；内部页最小子节点数 `mI = ceil(B/2)`。
- 叶页目标占用 `tL = max(ceil(C·p/100), mL)`。
- 内部页目标子节点数 `tI = max(ceil(B·p/100), mI)`。
- 因此 `p=100` 时目标即容量上限；`p=1` 时目标被抬到最小占用。

### 装载与末页再平衡

- `Add` 把键追加到当前叶页，键数达到 `tL` 立即封页；新页在下一次 `Add`
  时才创建，不产生空页。`AddMany` 等价于批量 `Add`，校验失败时整批不生效。
- `Finish` 时，若某层最后一页占用小于该层最小占用，则把它与前一页合并：
  合并和不大于容量（叶为 `C`，内部为 `B`）就保留为一页；否则重新均分，
  前一页得 `ceil(和/2)`、最后一页得 `floor(和/2)`。末页恰好等于最小占用时
  不再平衡。
- 叶层封好后自底向上逐层把相邻节点按每组 `tI` 个分组，末组不足 `mI` 时
  与前一组执行同样的“合并或均分”，直到只剩一个根节点。
- 根页与只有一页的叶层不受最小占用限制；空输入得到一个空叶根，树高为 1。

### 分隔键与点查

- 内部页第 `j` 个分隔键是第 `j+1` 个子树里的最小键（0 起始）。
- `Get(k)` 在内部页中遇到 `k >= 分隔键` 就走右侧子树；返回是否存在和访问
  页数。任何查询（含不存在的键）访问页数恒等于树高。

### 拒绝规则（原因可区分）

- 构造按 `C < 2` → `B < 3` → `p` 越界的顺序返回
  `ErrBadLeafCapacity`、`ErrBadInternalFanout`、`ErrBadPercentage`。
- `Add`：先判 `Finish` 之后（`ErrAlreadyFinished`），再判空键
  （`ErrEmptyKey`，先于次序错误），再判键不大于已加入的最大键
  （`*NonMonotonicError`，包含非法键、0 基下标和前一个键，含相等）。
- `Finish` 之后再 `Add` 或再 `Finish` 返回 `ErrAlreadyFinished`；
  `Finish` 之前调用 `Get` 返回 `ErrGetBeforeFinish`。
- 被拒绝的操作不改变任何已装入内容。
- `Add`/`AddMany`/`Finish`/`Get` 均可并发调用，结果等价于某个串行顺序；
  同一键序列无论分几次加入，逐页内容完全相同。

### 本地验证

```bash
# 全量测试（含 n=0..200 与按定义实现的朴素装载器逐页对拍、边界与并发）
go test -v ./bptree/

# 竞态检测
go test -race -v ./bptree/

# 代码检查
gofmt -l .
go vet ./...
```

测试以 `-v` 日志打印每个用例的输入、输出（逐页占用 / 分隔键）与判定依据；
对拍覆盖多种 `(C,B,p)` 组合下键数 0–200，逐键与越界键 `Get` 校验
存在性并断言访问页数等于树高，同时校验除最后一页外各层页占用始终落在
`[最小占用, 容量]` 区间、分隔键等于子树最小键。
