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

## 会话历史与前进后退缓存内核

`history/` 包实现浏览器会话历史栈与 BFCache 的协调内核（历史条目、遍历调度、
缓存资格判定、容量与存活期淘汰）。设计取舍见 [DESIGN.md](DESIGN.md)。

```bash
go test ./history/ -v          # 场景用例，日志含输入/输出/判定依据
go test ./history/ -race       # 并发串行化校验
go test ./history/ -bench .    # 遍历 O(1)、淘汰 O(log n) 的性能证明
```
