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

## 逻辑删除溯源审计与复活协调器

实现位于 `lifecycle/`：对象生命周期建模为互不重叠的存活区间，删除关闭当前
区间、复活开启新区间，审计事件按区间标识精确归属；复活必须精确指向最近一次
删除，并按“失效时点恰好等于本次删除时点”原子地恢复随删除失效的第一类链接。

关键文档：`lifecycle/DESIGN.md`（关键取舍、被放弃方案、性能证明与本地验证）。

```bash
go test ./lifecycle -race -v                        # 全部用例
go test ./lifecycle -run TestRandomDifferential -v  # 对照朴素模型的随机差分
```
