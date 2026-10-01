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

## 线性扫描寄存器分配器

实现在 `registeralloc` 包中，核心 API 为 `registeralloc.New(K)`、`(*Allocator).Add(id, start, end)` 和 `(*Allocator).Query(id)`。区间统一按半开区间 `[start, end)` 处理。

- **释放时机**：每次成功通过校验的添加开始时，先释放全部满足 `active.end <= start` 的活跃区间；因此 `end == start` 时寄存器会立即复用。
- **空闲选择**：释放后若仍有空闲寄存器，选择编号最小者；没有空闲寄存器才溢出。
- **溢出选择**：在新区间和当前全部活跃区间中选择终点最大者；终点并列时新区间优先自溢出；活跃区间之间并列时选择编号最小者。
- **溢出槽**：溢出不拆分区间。活跃区间被溢出时，其寄存器立即转给新区间；所有被溢出的区间按发生顺序取得 `0,1,2,...`，槽号不复用、无空洞。
- **查询结果**：查询返回当前寄存器或溢出槽。已让出寄存器的活跃区间之后只返回其溢出槽。
- **输入顺序**：成功添加的起点必须非降序；校验失败按“编号已存在 → 起点不小于终点 → 起点小于上一次成功起点”只返回第一个错误，且不改变任何状态。
- **并发语义**：内部使用读写互斥保护；添加之间串行，查询可与其他查询并发，整体结果等价于某个合法串行顺序。

测试中的朴素模拟器按相同规则逐步维护活跃集合、空闲寄存器和溢出槽，生产实现的每一步都与其对照；`go test -v` 日志会打印输入、输出和判定依据。

## 测试

```bash
# 全量测试
go test ./...

# 带竞态检测与详细输出
go test -race -v ./...

# 单个包 / 单个用例
go test ./ontology
go test -run TestObjectType ./ontology

# 寄存器分配器：查看逐步输入、输出与判定依据
go test -v ./registeralloc

# 寄存器分配器：并发可串行化与竞态检测
go test -race -v ./registeralloc

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
