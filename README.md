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

## history 包

`./history` 实现浏览器会话历史栈与前进后退缓存（bfcache）的协调内核：
历史条目列表、当前位置、遍历调度（同时刻只执行最后一次）、缓存资格
四条件判定与容量/TTL 淘汰五个模块相互协作。设计说明见
[history/DESIGN.md](history/DESIGN.md)。

```bash
go test ./history/ -race -v          # 场景测试 + 朴素模型随机对照
go test ./history/ -bench=.          # 遍历 O(1) / 驱逐 O(log n) 基准
```
