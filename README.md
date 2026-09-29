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

## topk：可撤回的前 K 名维护器

`topk` 包维护分数最高的若干元素，支持覆盖式新增、幂等撤回与并发读取。

### 排序键与并列规则

- **主键**：分数降序（分数高者名次靠前）。
- **次级规则（并列）**：分数相同按元素标识字典序升序（标识小者名次靠前）。
- 两条规则合成全序，任意两个元素名次唯一，结果确定且可复现。

### 语义

- `New(k, capacity)`：K 非正或容量小于 K 时整体拒绝（`ErrNonPositiveK` / `ErrCapacityTooSmall`）。
- `Upsert(id, score)`：覆盖式更新；空标识拒绝（`ErrEmptyID`）；新增时已达容量上限拒绝（`ErrAtCapacity`），覆盖已有元素不受容量限制。任何失败都不改变状态。
- `Remove(id)`：幂等空操作；删除门槛内元素后，门槛外名次最高者自动补位。
- `TopK()` / `Top(n)` / `Count()`：并发安全；不足 K 个返回全部；任意 `n <= K` 的结果是完整有序序列的前缀。

### 本地验证：朴素全量排序核对

`TestNaiveCrossCheck` 用朴素参考实现交叉核对：把全部元素放入切片，按
`(分数降序, 标识升序)` 全量排序后取前 K 名，与维护器结果逐步比对：

```bash
# 运行交叉核对与全部单测（日志打印操作、当前有序序列与判定依据）
go test -race -v ./topk/

# 只看朴素全量排序核对用例
go test -run TestNaiveCrossCheck -v ./topk/
```
