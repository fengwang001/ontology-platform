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

## 分代回收模型（`gengc` 包）

`gengc/` 实现年轻区/年老区分代内存回收：对象在年轻区分配，熬过阈值次年轻区
回收后晋升；写屏障按对象粒度登记「年老→年轻」引用，年轻区回收只扫描年轻区；
年老区回收仅显式触发、全堆可达性扫描。

- 设计说明（关键取舍、放弃方案、复杂度证明、验证方法）：`gengc/DESIGN.md`
- 定向用例：`go test ./gengc/ -race -v`
- 与朴素全扫描模型的随机差分（逐条输入/输出/判定日志）：
  `go test ./gengc/ -run TestRandomDifferential -v`
