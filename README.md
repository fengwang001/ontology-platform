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

## regalloc：线性扫描寄存器分配器

`regalloc` 包在 K 个物理寄存器（编号 `0..K-1`）间为半开活跃区间
`[start, end)` 做线性扫描分配，装不下时溢出到溢出槽（槽号从 0 起按溢出
先后依次发放，永不复用）。`Add` 与 `Query` 均可并发调用，内部以互斥锁
串行化，结果等价于某个串行顺序；相同添加序列重放得到完全相同的分配。

### 添加规则

- 区间按起点非降序添加；每次添加先校验（按序只报第一个错误）：
  编号已存在（`ErrDuplicateID`）、起点不小于终点（`ErrInvalidRange`）、
  起点小于上一成功添加的起点（`ErrStartRegression`）。被拒绝的添加
  不改变任何分配与溢出槽计数。构造时 `K < 1` 报
  `ErrInvalidRegisterCount`；查询未知编号报 `ErrNotFound`。
- **释放时机**：校验通过后，先释放全部终点 `<=` 新起点的活跃区间。
  终点恰等于新起点即已到期（半开区间不重叠），其寄存器可立即复用。
- **空闲分配**：有空闲寄存器时取编号最小者。
- **溢出选择**：无空闲寄存器时，在「当前全部活跃区间 + 新区间」中选
  终点最大者溢出：
  - 新区间终点不小于活跃区间最大终点（含并列）时，新区间自溢出；
  - 否则溢出活跃区间中终点最大者，活跃区间之间终点并列时取编号最小者；
  - 被溢出的活跃区间整体改为溢出（不拆分），其寄存器立即转给新区间。
- **溢出槽**：被溢出的区间（无论新旧）从槽 0 起按溢出先后依次获得
  不复用的槽号，槽号连续无空洞。

### 查询

`Query(id)` 返回区间当前的分配：寄存器或溢出槽。已被转让寄存器的
活跃区间只报溢出槽。

### 本地验证

```bash
# 单元测试（规则覆盖：终点恰等于新起点复用、并列新区间自溢出、
# 活跃并列取最小编号、K=1 连续溢出、溢出寄存器复用等）
go test ./regalloc

# 详细日志：打印每步输入、输出与判定依据（朴素模拟对照）
go test -v -run TestAgainstNaiveModel ./regalloc

# 竞态检测（并发 Add/Query）
go test -race ./regalloc
```
