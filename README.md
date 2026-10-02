# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 两层 B+ 树删除再平衡

`ByteBPlusTree` 只有一层叶页，按页容量 `C` 以“字节占用”计量：每条记录贡献声明的整数尺寸 `e`，页占用为该页全部记录尺寸之和。最小占用 `M=ceil(C/2)`，单条记录最大尺寸为 `floor(C/4)`。

- 插入：键放入“分隔键不大于该键的最靠右页”；没有符合页时进入最左页。页溢出时按有序记录枚举切分点，选择左右字节差绝对值最小的位置；差值相同取较小位置。新叶页紧接原页之后，编号只递增、不复用。
- 删除：仅删除所在页低于 `M` 且树中不止一页时触发一次、不级联的本地再平衡，固定次序为左借、右借、并左、并右。
- 借位：从左邻尾部或右邻头部取最少且至少一条，使下溢页达到 `M`，同时借出方取走后仍至少为 `M`；借出后恰为 `M` 合法。
- 合并：两页占用之和不大于 `C` 才执行，恰等于 `C` 合法；总是保留页序中靠左的页及其编号。
- 分隔键：每次操作完成后由各非最左页当前首键隐含确定，不单独保存可变状态。
- 拒绝顺序：先校验参数，再处理 `Insert` 重复键、`Delete` 缺失键、`Insert` 达到页数上限；任何拒绝都不改变页内容、分隔键与编号计数。
- 并发：公开操作受互斥保护，`Pages()` 返回按页序排列的深拷贝；重放相同成功与失败操作序列会得到相同编号和内容。

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

# 若 Go 构建缓存目录只读，可显式指定临时缓存
GOCACHE=/tmp/go-cache go test ./...

# 带竞态检测与详细输出
GOCACHE=/tmp/go-cache go test -race -v ./...

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
