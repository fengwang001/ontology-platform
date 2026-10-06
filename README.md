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

## 信号配时控制与绿波协调（signal 包）

- `signal/`：城市干线路口信号控制服务——固定相序配时方案切换、紧急/公交两级优先、
  相位差偏离回归；任意时刻的相位、剩余时长与偏离量可精确复现，查询为 O(1)。
- `signal/naive/`：独立编写的逐秒推进参照模型，用于随机操作序列对照测试。
- 设计取舍与验证方法见 `signal/DESIGN.md`。
