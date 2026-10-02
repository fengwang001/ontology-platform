# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## deviceflow：设备授权码流程服务

`deviceflow` 包实现 RFC 8628 风格的设备授权码流程：客户端 `Start`
发起设备授权，用户凭短码 `Authorize` 批准或拒绝，设备按间隔 `Poll`
轮询令牌。所有操作可并发调用（内部串行化，结果等价于某个串行顺序），
相同操作序列（含 `gen` 返回序列）重放得到完全相同的设备码、间隔、
返回类别与令牌序号。

### 用户码规范化与重复判定

- 规范化：去掉全部连字符、字母转大写；规范化后须为 4 到 16 个
  大写字母或数字，否则不合规（`ABCD-EFGH`、`abcdefgh`、
  `ab-cd-ef-gh` 三者等价）。
- `Start` 内 `gen` 返回不合规、或与任一尚未到期（`now < expiresAt`，
  不论状态，含 denied 与 consumed）的授权重复，均算一次失败尝试并
  重新调用 `gen`；连续 100 次失败报生成失败。已到期的用户码可复用。
- `Authorize` 取规范化用户码相同的最近创建的授权。

### 限流与 u 的推导

- 客户端惩罚次数 `s` = 该客户端名下授权上发生过的「过快」事件中
  `t+H > now` 者的个数（`t+H` 恰等于 `now` 已出窗）。
- `s >= Z` 时 `Start` 报限流：把窗口内事件按发生时刻升序排列，
  最早可发起时刻 `u` = 第 `s−Z+1` 个事件的时刻加 `H`（即等到窗口内
  事件数回落到 `Z−1` 的时刻）。
- 拒绝次序：`Start` 为 参数非法 → 时钟回退 → 限流 → 超限 → 生成失败；
  `Authorize` 为 参数非法 → 时钟回退 → 未找到 → 已过期 → 已决定。
  被拒绝的操作不改变任何授权、过快事件、计数器与时钟。

### 轮询返回的固定判定次序

`Poll` 在参数非法、时钟回退、未知设备码三类拒绝之后，其余均为被
接受的操作（推进时钟），按以下次序取第一个成立者：

1. 状态为 `consumed` → 无效授权；
2. `now >= expiresAt` → 已过期；
3. `now < nextAllowed` → 过快（惩罚，见下）；
4. 否则 `nextAllowed = now + interval`，按状态返回：`pending` →
   等待授权；`approved` → 令牌（序号从 1 递增）并置 `consumed`；
   `denied` → 拒绝访问并置 `consumed`。

### 过快惩罚与客户端基础间隔

- 过快时 `interval = min(Imax, interval + D)`，
  `nextAllowed = now + 新 interval`（以惩罚时刻重设，不沿用旧值），
  并记一条 `(客户端, now)` 过快事件；已处于 `Imax` 时同样记事件并按
  `Imax` 重设。
- 客户端基础间隔 = `min(Imax, I0 + D·s)`；新授权的 `interval` 取发起
  时刻的客户端基础间隔，不继承旧授权的 `interval`。

### 名额占用规则

- 活跃授权 = 状态为 `pending` 或 `approved` 且 `now < expiresAt`；
  每客户端活跃数达到 `Cmax` 时 `Start` 报超限。
- `approved` 未领取到期前一直占用名额；`denied` 立即释放；
  `consumed` 与已到期的授权不占名额。名额按客户端隔离。

### 本地验证

```bash
# 全部测试（含 2000 组随机序列与朴素模拟对照、并发测试）
go test ./deviceflow/

# 竞态检测 + 详细日志（打印每组输入、输出与判定依据）
go test -race -v ./deviceflow/

# 仅随机对照 / 仅规格示例
go test ./deviceflow/ -run TestRandomizedAgainstNaive -v
go test ./deviceflow/ -run TestSpec -v
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
