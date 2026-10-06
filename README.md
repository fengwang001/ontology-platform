# thin-pool

精简配置存储池的空间管理服务，提供精简卷、按需物理块分配、保留空间、范围回收、扩缩容和水位事件。

## 环境要求

- Go 1.26+（`go version` 确认）

## API

```bash
pool, err := thinpool.NewPool(physicalBlocks, overcommitPercent, warningPercent, criticalPercent)
err = pool.CreateVolume(name, virtualBlocks, reservedBlocks)
err = pool.WriteBlock(name, virtualBlock)
released, err := pool.ReclaimRange(name, start, length)
err = pool.ResizeVolume(name, newVirtualBlocks)
err = pool.SetReservation(name, reservedBlocks)
err = pool.DeleteVolume(name)
snapshot := pool.Snapshot()
events := pool.Events()
```

错误通过 `thinpool.Error.Code` 区分，错误码定义在 `errors.go`。完整设计与复杂度证明见 `DESIGN.md`。

## 测试

```bash
# 全量测试
go test ./...

# 单个包 / 单个用例
go test -run TestRandomOperationsAgainstNaiveModel -v ./...

# 带竞态检测
go test -race ./...

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

若默认 Go 缓存目录只读，可设置：

```bash
export PATH=/usr/local/go/bin:$PATH
export GOCACHE=/tmp/go-cache-ontology
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
