# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 多版本事务历史隔离异常判定器

`anomaly` 包按读写操作与每键版本次序构建 WW/WR/RW 三类边的事务
依赖图，依 G0 → G1a → G1b → G1c → G-single → G2 的顺序只报最先
命中的类别，给出隔离等级（无 / PL-1 / PL-2 / PL-2+ / PL-3）与
规范见证环（最短、字典序最小、以环内最小编号起头）。判定为无
共享状态的纯函数，可并发调用，且与输入排列无关。

完整规则、输入格式与示例见 `docs/anomaly.md`；快速试用：

```bash
go run ./cmd/anomaly        # 内置演示
go test -race -v ./anomaly/ # 含逐环穷举对拍与随机历史
```

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
