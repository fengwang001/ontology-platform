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

## 有序用电错避峰轮换调度器

`scheduler/` 包实现轮换调度：区域下设轮换组（累计被限时长最少者优先、
编号小者优先平局），用户分免控/保底/普通；指令带等级与对齐时段边界的
左闭右开窗口，可叠加（取最大等级）、改级、取消，一律在时段边界生效；
通知在时段开始前确认，未确认记考核。设计取舍见 [DESIGN.md](DESIGN.md)。

```bash
go test ./scheduler/                              # 单元 + 随机对照 + 性能不变量
go test -run TestRandomAgainstModel -v ./scheduler/  # 查看逐操作日志
go test -race ./scheduler/                        # 并发等价串行
```
