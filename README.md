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

## LSM 写停顿控制器

根包 `ontology` 提供 `WriteStallController`，按 LSM 的零层文件数 `n0`、待压实字节 `pend`、冻结内存表数 `imm` 在 `StallNormal`、`StallSlow`、`StallStopped` 间迁移。

### 判定顺序

每次调用 `Observe(n0, pend, imm)` 严格按以下顺序执行：

1. 若 `n0 >= S2`、`pend >= P2` 或 `imm >= I`，进入停写。
2. 否则，若当前为停写，只有三者都严格低于各自停写恢复线时才离开；离开后满足减速条件则进入减速，否则进入正常；任一未恢复则保持停写。
3. 否则，若 `n0 >= S1` 或 `pend >= P1`，进入减速。
4. 否则，若当前为减速，只有 `n0` 与 `pend` 都严格低于各自减速恢复线才回到正常；否则保持减速。
5. 否则为正常。

阈值 `T` 的恢复线为 `floor(3T/4)`，“低于恢复线”指观测值严格小于该值。冻结内存表数只有停写阈值 `I` 和停写恢复线，不参与普通减速条件。

### 减速延迟

减速态延迟单位为微秒：

```text
rho = max((n0 - S1) / (S2 - S1), (pend - P1) / (P2 - P1))
rho = clamp(rho, 0, 1)
delay = max(1, ceil(D * rho))
```

实现使用整数有理数交叉相乘选择较大比值，再用上取整除法，避免浮点误差；正常和停写态延迟为 `0`。`Admit()` 在停写态返回可区分的 `StallRejectWriteStopped`，其他状态接受并返回当前延迟。

### 错误与并发

构造参数按 `S1 >= S2`、`P1 >= P2`、`I <= 0`、`D <= 0` 的顺序只返回第一个原因。`Observe` 参数按 `n0`、`pend`、`imm` 的顺序检查负数，也只返回第一个原因。被拒绝的构造或观测不会改变状态。

`Observe`、`Admit`、`State`、`DelayMicros` 均通过读写锁串行化，行为等价于某种合法的串行交错顺序；以固定观测序列重放会得到完全相同的状态与延迟序列。

### 本地验证

```bash
go test -v ./...
go test -race ./...
go vet ./...
```

测试覆盖阈值等于和差 1、恢复线等于和差 1、停写退出到正常/减速、迟滞区延迟下限 `1`、两个压力比值取较大者、上取整、三态全部可达迁移、并发调用，以及 3000 步含负参数随机观测与朴素状态机对拍。使用 `-v` 可查看每一步输入、输出和判定依据日志。
