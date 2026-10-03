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

## STM 竞争管理器（`stm` 包）

`stm.Manager` 在事务打开对象发生冲突时裁决谁中止、谁等待，并给出退避延迟。
构造参数：`M` 对象数（1..64）、`L` 特权阈值（1..16）、`D` 基础延迟（1..1000）、
`E` 指数封顶（0..20）、`P` 积分上限（1..1000）、`Q` 强制阈值（1..16）；
任一越界即以 `ErrInvalidConfig` 整体拒绝。

每个事务维护：积分 `kp`（初值 0）、被他人中止次数 `ab`（初值 0）、本次尝试对
每个对象的持有方式（无/读/写）与连续失败次数 `att[o]`（初值 0）。
`ab >= L` 的事务为特权事务。

### 裁决顺序

`Open(t, o, 写?)` 的敌手集合：求写时为他人写者与全部他人读者，求读时仅为他人
写者。令 `k = att[o]`（增加前的值），对每个敌手 `e` 依次判定：

1. 二者皆特权：事务号小者胜；
2. 仅 `t` 特权：`t` 胜；
3. 仅 `e` 特权：`t` 败；
4. 皆非特权：`k >= Q` 或 `kp(t)+k > kp(e)`（严格大于）时胜。

压过全部敌手才动手：每个敌手转已中止、`ab` 加一、`kp` 衰减为 `⌈kp/2⌉`、释放
全部持有并清空其 `att`；随后 `t` 获得对象，返回被中止者升序列表。否则不动任何
敌手，仅 `att[o] = k+1`，返回等待与延迟。

### 积分与 att 清零时机

- `kp` 仅在本次尝试首次获得某对象时加一（封顶 `P`）；读升级为写不再加分；
  被他人中止时取上整一半；此外不变。
- `att[o]` 在每次冲突失败时加一；成功获得 `o` 时清零（无论是否因封顶而未加分）；
  事务被他人中止、`Restart`、`Commit`、`Abort` 时全部清零。

### 延迟公式

- `Open` 等待：`D × 2^min(k, E)`，`k` 取 `att[o]` 增加前的值。
- `Restart`：`D × 2^min(max(ab-1, 0), E)`。

### 拒绝顺序

其余调用被拒绝时只报第一个原因：事务号不存在（`ErrNoSuchTxn`）→ 状态不符
（`ErrBadState`，`Open`/`Commit`/`Abort` 须活跃，`Restart` 须已中止）→ 对象越界
（`ErrBadObject`，仅 `Open`）。被拒绝的调用不改变任何状态。

### 本地验证

```bash
# 全部单元测试（含 2000 组随机序列与朴素模拟的逐步对照）
go test ./stm

# 查看随机对照日志（输入、输出与判定依据）
go test -v -run TestRandomAgainstNaiveSim ./stm

# 竞态检测
go test -race ./stm
```
