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

## topk：近似热门元素统计

`topk` 包提供多行计数草图（Count-Min Sketch）与 Space-Saving 风格候选列表的组合结构，
用于在元素到达流上维护出现次数最多的若干候选。所有公开方法均可被多个执行体并发调用。

### 哈希定位

- 草图共 `rows` 行、每行 `cols` 个格子；第 `r` 行使用固定种子
  `seed[r] = mix64(r + 0x9e3779b97f4a7c15)`。
- 元素 `e` 在第 `r` 行的落格为 `mix64(e + seed[r]) % cols`，`mix64` 为 splitmix64
  风格的确定性混合函数，因此定位完全可复现，不依赖任何随机源。

### 估计与排名规则

- 到达 `(element, count)` 时，在每行对应格子累加 `count`。
- `Estimate(e)` 取各行对应格子的最小值；由于碰撞只会把其他元素的计数叠加进来，
  估计值只高估、不低估（`Estimate(e) >= 真实累计次数`）。
- 候选全序：估计值降序；估计值相同按元素值升序。该全序稳定且可复现。

### 候选维护

- 候选列表最多保留 `capacity` 条记录，每条记录保存元素及其**到达时刻的**估计值。
- 元素到达时：已在候选中则刷新其记录估计值并重排；不在候选中且列表未满则插入；
  列表已满但按全序高于队尾则替换队尾；否则忽略。
- 其余候选的记录估计值不做被动刷新，仅在自身到达时刷新（可能暂时低于草图当前估计）。

### 边界与错误类别

所有非法输入整体拒绝，失败不改变草图与候选、不留痕，且类别互不相同（均可用
`errors.Is` 判定）：

- `ErrInvalidParam`：构造参数非法（`rows`/`cols`/`capacity` 非正，或 `maxElement` 为 0）。
- `ErrElementOutOfRange`：元素不在 `[0, maxElement)` 内。
- `ErrNonPositiveCount`：到达次数为零。
- `ErrCountOverflow`：累加会使某行格子溢出 `uint64`；先校验所有行再写入，保证原子拒绝。

### 自检

`SelfCheck` 校验内部不变量：候选数不超容量、元素合法且无重复、列表满足全序、
记录估计值不超过草图当前估计值。

### 本地验证

```bash
# 单元测试（含竞态检测）
go test -race ./topk/

# 查看逐步日志：每步输入、估计值与候选判定依据
go test -v -run TestStepByStep ./topk/
```
