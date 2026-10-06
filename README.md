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

## 边界检查消除子系统（bce）

`bce` 包实现编译器中的数组边界检查消除：输入带控制流的中间代码
（每个下标访问附带一次 `Check`），输出每个检查的判定（移除/保留）、
保留原因与依据的事实来源。

- 允许的事实仅四类：常量下标、常量数组长度、路径比较条件、已通过的检查。
- 下界（`idx >= 0`）与上界（`idx < len`）分别证明、分别报告原因。
- 循环归纳变量（固定正步长、起点与上界可证）可整循环证明，取等按比较符号开闭精确处理。
- 数组重赋使长度与已通过检查事实失效；汇合处只保留两侧共有事实。
- 输入错误按固定次序拒绝：未定义引用 > 多终结 > 不可达块 > 循环多入口，不产生部分结果。
- 分析无共享状态，可并发；同一输入的报告逐字节相同。
- 设计取舍见 `bce/DESIGN.md`。

```go
rep, err := bce.Analyze(prog) // rep.String() 为确定性报告
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
