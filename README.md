# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 单梯扫描调度模拟器

`elevator` 包提供带容量限制与方向保持的离散步进电梯模拟：

- `NewElevator(F, C)`：创建 1 到 `F` 层、容量为 `C` 的电梯；初始位于 1 层、方向 `Idle`、空载。`F < 2` 或 `C < 1` 时拒绝。
- `Call(id, from, to)`：接受乘客呼叫。候梯队列按接受先后保序；拒绝调用不会改变任何状态。
- `Tick() TickResult`：推进一步，返回移动前楼层、下梯 ID、上梯 ID、定向后的方向和移动后楼层。
- `Passenger(id)`：返回乘客状态、上梯步序号和下梯步序号；第一次 `Tick` 的步序号为 1。

### Tick 四阶段

每个 `Tick()` 严格按以下顺序执行：

1. **卸客**：轿厢内目的层等于当前层的乘客全部下梯，下梯 ID 按轿厢内扫描顺序记录。
2. **上客**：当前层候梯乘客按候梯队列顺序逐个上梯，直到无人可上或轿厢达到容量；不根据乘客目标方向筛选。本步刚下梯的乘客不会重新上梯。
3. **定向**：目标集合 `T` 包含轿厢内乘客目的层和所有候梯乘客起始层，并排除当前层。
4. **移动**：沿定好的方向移动一层；方向为 `Idle` 时楼层不变。

### 定向规则

- `T` 为空：方向设为 `Idle`，不移动。
- 当前为 `Up` 且 `T` 中存在高于当前层的目标：保持 `Up`。
- 当前为 `Down` 且 `T` 中存在低于当前层的目标：保持 `Down`。
- 其余情况（初始 `Idle`，或当前方向前方已无目标）：选择距离当前层最近的目标；上下距离相等时选择 `Up`。

### 拒绝原因

`Call` 的可判别错误原因按以下顺序只返回第一个：

1. `InvalidCallID`：`id < 1`。
2. `InvalidFloor`：`from` 或 `to` 不在 1 到 `F`。
3. `SameFloor`：`from == to`。
4. `DuplicateCallID`：`id` 已存在，包含仍在候梯、梯内或已送达的乘客。

查询不存在的乘客返回可判别的 `PassengerNotFound`。

所有呼叫、步进和查询方法均由互斥锁保护，并发结果等价于某个合法的串行执行顺序。

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
go test ./elevator
go test -run TestRandomSequencesMatchNaiveSimulation -v ./elevator

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out

# 随机 2000 组逐步重放日志（包含输入、输出与判定依据）
go test -run TestRandomSequencesMatchNaiveSimulation -v ./elevator

# 若 shell 中未配置 Go PATH，可使用本机安装路径
PATH=/usr/local/go/bin:$PATH go test ./...
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
