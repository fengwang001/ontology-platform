# ontology-platform

当前交付模块为工业控制室报警生命周期管理服务，代码位于 `alarm/`，设计说明见 `alarm/DESIGN.md`，包级使用文档见 `alarm/README.md`。

## 环境要求

- Go 1.26+（`go version` 确认）

## 运行

```bash
# 报警包测试
go test ./alarm

# 若环境默认 Go 缓存目录只读，可指定临时缓存：
GOCACHE=/tmp/go-cache-ontology go test ./alarm
```

## 测试

```bash
# 全量测试
go test ./...

# 带竞态检测与详细输出
go test -race -v ./...

# 单个包 / 单个用例
go test -v ./alarm
go test -run TestChatterWindowLeftOpenRightClosed ./alarm

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out

# 两档总点数性能对照（活动报警数固定）
go test -run '^$' -bench 'BenchmarkService(100|5000)Points' -benchmem ./alarm
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
