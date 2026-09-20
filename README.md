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

## sortkey：可插入排序键生成器

`sortkey` 包提供可插拔的排序键生成：元素靠字符串键的字典序决定先后，
支持在任意两个相邻元素之间插入新元素而不改动其他键。

- 字符集固定为 `0123456789abcdefghijklmnopqrstuvwxyz`（36 个字符，顺序与字节序一致）；
  键不得以 `'0'` 结尾（规范形式，避免左侧无法再细分的死路）。
- `Fractional.Between(left, right)` 生成严格落在左右邻居之间的键，结果确定；
  空串表示该侧无邻居（最前 / 最后插入）。
- 键长度有上限，超限时返回 `ErrNeedsRebalance`；`Sequence.Stats` 报告最长键与余量。
- `Sequence.Rebalance` 原子地把所有键重排为等间距短键，保持相对顺序不变。
- 并发插入同一间隙时，最终顺序由元素内容的字典序决定，与调度无关。

演示：

```bash
go run ./cmd/demo
```
