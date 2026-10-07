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

## 子图快照完整性校验（`snapshot` 包）

对导出的对象/链接记录流做顺序校验，定位第一条不可信记录并返回最大可
恢复前缀。详见 [`DESIGN.md`](DESIGN.md)。

```go
import "ontology/snapshot"

result := snapshot.ValidateSlice(records)
// result.Status:   snapshot.StatusComplete | snapshot.StatusTruncated
// result.PrefixLen: 最大可恢复前缀长度（完整时等于全长）
// result.BadIndex:  损坏记录下标（完整时为 -1）
// result.Reason:    ObjectCorrupt | LinkCorrupt | LinkReferenceMissing
```

流式输入实现 `snapshot.Reader` 接口即可；校验命中损坏即停止读取，实际
读取记录数不超过 `PrefixLen + 1`。

```bash
# 随机差分测试（含逐次审计日志）
go test -race -v -run TestRandomDifferential ./snapshot

# 将每次校验的完整输入与输出留存为 JSONL
go run ./cmd/snapshotaudit -trials 200 -out audit.jsonl
```
