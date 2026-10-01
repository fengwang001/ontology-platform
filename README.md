# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 流式 B+ 树批量装载

`bplus.NewBulkLoader(C, B, p)` 创建批量装载器，键类型为非空字符串：

- 叶页容量为 `C`，最小键数 `mL = floor(C/2)`，目标占用 `tL = max(ceil(C*p/100), mL)`。
- 内部页最多 `B` 个子节点，最小子节点数 `mI = ceil(B/2)`，目标 `tI = max(ceil(B*p/100), mI)`。
- `Add(keys...)` 只追加严格递增键；当前叶页达到 `tL` 时封页，新页在下一次添加时才创建，因此不会产生空页。
- `Finish` 先处理叶层最后一页：最后一页小于 `mL` 时与前一页合并；总数不超过 `C` 就保留为一页，否则均分为前页 `ceil(总和/2)`、末页 `floor(总和/2)`。
- 叶层定稿后自底向上按每组 `tI` 个直接子节点构造内部页；末组少于 `mI` 时使用同样的“能合并则合并，否则均分”规则。
- 内部页第 `j` 个分隔键是第 `j+1` 个直接子树的最小键。点查时 `k >= 分隔键` 即进入右侧子树；`Get` 返回是否存在以及访问页数，已完成树的访问页数恒等于树高。
- 空输入在 `Finish` 后得到单个空叶根；单页叶层和最终根不受最小占用限制。
- `Add`、`Finish`、`Get` 使用读写锁保护，并发结果等价于某个合法串行顺序；被拒绝的批次不会部分写入。

错误原因使用不同哨兵错误区分：构造参数错误、空键、非递增键、`Finish` 后写入或重复 `Finish`、`Finish` 前查询。非递增错误可通过 `bplus.OrderError` 取得违规键及其从 0 开始的全局下标。

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

# 仅验证 B+ 树批量装载（测试会输出输入、页占用、朴素模型与判定依据）
go test -v ./bplus

# 若默认 GOCACHE 位于只读目录，可指定临时缓存
GOCACHE=/tmp/go-cache go test -v ./bplus
GOCACHE=/tmp/go-cache-race go test -race ./bplus

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
