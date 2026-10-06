# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 分块压缩追加容器

`blockstore` 包提供按固定逻辑块独立压缩的只追加字节容器：

- 每个块带 25 字节头，记录存放形式、存放长度、原始长度和 FNV-1a 校验值。
- 每个块恰好调用一次注入压缩器；压缩失败和收益不足分别统计。
- 追加先重写未满尾块，再切分剩余内容；整次追加原子提交。
- 随机读取可跨块、自动截断到末尾并返回 `EOF`；只处理读取区间涉及的块。
- 解压失败、长度不符、校验不符分别报错并携带块号。
- 压缩块使用哈希表加双向链表 LRU 缓存；直存块不占缓存，容量 0 表示禁用。
- 块定位使用块起点数组二分查找，缓存操作为 O(1)。

主要接口：

```go
container, err := blockstore.New(
    blockstore.Config{BlockSize: 4096, MinGain: 16, CacheCapacity: 64},
    compressor,
    decompressor,
)

if err := container.Append(data); err != nil {
    return err
}

result, err := container.ReadAt(offset, length)
// result.Data 是读取内容，result.EOF 表示已读到当前流末尾。

stats := container.Stats()
```

块格式、关键取舍和被放弃方案见 `DESIGN.md`。

### 本容器专项验证

```bash
# 全量测试（含固定种子随机朴素模型对照）
go test -v ./...

# 并发竞态检测
go test -race ./...

# 打印每条随机操作的输入、输出、存放原因、命中与解压次数
go test -run TestRandomOperationsMatchNaiveModel -v

# 块定位与缓存复杂度结构验证
go test -run 'TestBlockLookup|TestCacheOperations' -v

# 三类损坏在直存块和压缩块上的矩阵测试
go test -run 'TestCorruption|TestReadError|TestErrorPriority' -v
```

## 环境要求

- Go 1.26+（`go version` 确认）

## 运行

```bash
# 拉取依赖
go mod tidy

# 当前仓库交付的是 blockstore 包；使用上方专项测试即可运行验证。
```

## 测试

```bash
# 全量测试
go test ./...

# 带竞态检测与详细输出
go test -race -v ./...

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
