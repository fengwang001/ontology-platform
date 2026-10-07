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

## gc 包：分代内存回收模型

`gc/` 实现语言运行时的分代回收模型：年轻区分配、写屏障 + 记忆集、
年轻区回收与晋升、显式触发的年老区回收、统计视图与固定优先级的错误拒绝。
设计与取舍见 [gc/DESIGN.md](gc/DESIGN.md)，API 见 `gc/runtime.go` 注释。

```bash
go test ./gc/          # 单元测试 + 朴素模型随机对照
go test -race -v ./gc/ # 竞态检测与逐条操作日志
```
