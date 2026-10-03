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

## 时间依赖路网最早到达查询（`timetable` 包）

`timetable/` 实现边耗时随出发时刻分段变化、可封闭、可随时间通告的有向
路网：任意等待下的最早到达、紧边路线（先边数少、再边号字典序小）、
历史版本查询与 `Popped` 搜索规模度量。语义、到达函数推导、紧边取法与
时钟/版本规则见 `timetable/DESIGN.md`。

```bash
go test ./timetable -race -v
go test ./timetable -run TestRandomDifferential -v   # 2000 组朴素对照
go test ./timetable -run TestGridPopped -v           # 300x300 网格规模
```
