# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 可扩展哈希桶页索引

根包提供可注入哈希函数的 `Index`：目录按哈希值低位定位，目录长度始终为 `2^全局深度`，每个桶有自己的局部深度。

- **结构不变量**：局部深度为 `d` 的桶必须被恰好 `2^(全局深度-d)` 个目录项引用；目录项只能引用存在的桶；桶内键数不超过容量。
- **分裂条件**：目标桶已满时，若局部深度小于全局深度则只分裂该桶；若局部深度等于全局深度，先将目录加倍再分裂。若分裂后新键仍落入满桶，继续分裂。
- **伙伴桶**：桶局部深度为 `d` 时，其伙伴目录前缀在第 `d-1` 个低位上取反。两个伙伴桶必须局部深度相同，且目录前缀的高位完全一致。
- **合并条件**：删除后，如果目标桶与其伙伴桶局部深度相同且键数之和不超过容量，则立即合并；合并结果继续检查新伙伴，允许连锁合并。
- **目录收缩**：每次合并后检查所有桶；只要所有桶的局部深度都小于全局深度，目录即可减半，并可连续减半到不能继续为止。
- **溢出撤回**：局部深度达到 `HashBits` 后仍无法容纳新键时返回 `ErrBucketOverflow`；需要新桶但桶数将超过 `MaxBuckets` 时返回 `ErrTooManyBuckets`。插入采用写时复制事务，重复键、缺失键、溢出或桶数上限拒绝都不会替换原目录、桶和计数器。
- **并发与统计**：`Lookup` 使用读锁，`Insert`、`Delete` 使用写锁；读者只能看到操作前或操作后的完整状态。`Stats` 报告分裂、合并、目录加倍和目录减半次数，`Snapshot` 返回深拷贝以便逐项校验。

可区分的错误包括：`ErrDuplicateKey`、`ErrKeyNotFound`、`ErrBucketOverflow`、`ErrTooManyBuckets` 和 `ErrInvalidConfig`。

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
go test .
go test -run TestSplitOverflowRollbackCascadeMergeAndShrink -v .

# 竞态检测；如默认缓存目录不可写，可显式指定 GOCACHE
GOCACHE=/tmp/go-cache go test -race -v .

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
