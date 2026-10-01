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

# 只运行单梯扫描调度测试；-v 会打印 2000 组随机序列的输入、输出和判定依据
go test -race -v ./elevator

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

## 单梯扫描调度

`elevator.New(floors, capacity)` 创建单台轿厢。楼层范围为 `1..floors`，轿厢初始在 1 层、方向为 `Idle`、空载；`floors < 2` 或 `capacity < 1` 会返回错误。

- `Call(id, from, to)`：按参数校验顺序返回第一个错误：id 非法、楼层越界、同层、id 已存在；被接受的乘客进入候梯队列并保持接受顺序。
- `Tick()`：将内部步序号加一，然后严格按四个阶段执行。
  1. 卸客：轿厢内所有目的层等于当前层的乘客下梯。
  2. 上客：当前层候梯乘客按队列次序上梯，只受容量限制，不检查乘客自身方向；本步刚下梯者不会重新上梯。
  3. 定向：目标集合为轿厢内目的层和仍候梯乘客的起始层，并去掉当前层。集合为空则保持楼层不动且方向为 `Idle`。
  4. 移动：`Up` 上一层，`Down` 下一层；`Idle` 不移动。

定向时先做方向保持：当前为 `Up` 且仍有高于当前层的目标则继续 `Up`；当前为 `Down` 且仍有低于当前层的目标则继续 `Down`。不能保持时，在所有目标中选择距离当前层最近者，距离相等选择位于上方的目标。若轿厢在当前层唤醒候梯乘客，该乘客会在同一个 `Tick()` 的上客阶段进入轿厢。

`Passenger(id)` 返回乘客当前状态、起终点、上梯步序号与下梯步序号；未知 id 返回独立的 `ErrPassengerMissing`。所有公开方法由同一把互斥锁保护，并发调用的结果等价于某个串行交错。

测试包含容量受限、同层卸客后立即上客、上行时接收下行乘客、等距取上、方向保持与反转、空闲唤醒、拒绝不改状态、并发竞态检测，以及 2000 组随机呼叫 / Tick / 查询序列与独立朴素模拟器的逐步对照。
