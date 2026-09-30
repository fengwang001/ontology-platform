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

## 托管区间账户

`ontology.Bank` 提供有界账户、未决事务预留、事务提交/中止和精确读。

- 对账户分别累计未决负增量 `N` 与未决正增量 `P`；可能区间是 `[b+N, b+P]`。
- `N` 只包含负增量，`P` 只包含正增量。同一事务的多次预留也分别计入两侧，不在事务内预先抵消。
- 负增量只检查预留后的 `b+N >= L`，因为即使全部负增量提交，余额也不会低于该下端。
- 正增量只检查预留后的 `b+P <= H`，因为即使全部正增量提交，余额也不会高于该上端。
- 任取当前未决项子集、按任意顺序提交时，余额始终是初值加已提交增量，并保持在 `[L,H]` 内。

正负两侧独立判定是因为未决事务的提交选择未知：正增量和负增量来自可能分别提交或中止的操作，不能用同一事务中的 `+50` 抵消其先前的 `-50`。这种保守区间允许预留互不等待，同时保证最坏组合也不越界。

预留使用账户状态的原子 CAS 更新 `(余额、负增量和、正增量和、未决项数)`，不同事务不持有账户锁即可并发；提交或中止只移除当前事务记录的贡献，再把事务标记为终结。预留和读共享提交门禁读锁，跨账户提交取写锁并串行化终结阶段，保证一个事务涉及的全部账户作为一个生效点，读者不会看到部分提交。

精确读只在未决项数为 0 时返回确定余额；否则返回“余额不确定”以及当前 `[b+N,b+P]`。所有操作默认通过标准日志输出输入、输出和判定依据，也可通过 `SetLogger` 替换或静音。

### 本地验证

如当前环境的 `PATH` 不包含 Go，可使用 `/usr/local/go/bin/go`；若默认构建缓存只读，可把缓存放到 `/tmp`：

```bash
GOCACHE=/tmp/ontology-go-cache /usr/local/go/bin/go test ./... -v
GOCACHE=/tmp/ontology-go-cache /usr/local/go/bin/go test -race ./...
GOCACHE=/tmp/ontology-go-cache /usr/local/go/bin/go vet ./...
```

测试覆盖：

- `b=50,L=0,H=100` 时，同事务 `-50`、`+50` 后再预留 `-1` 仍报“下界不足”。
- 正、负增量只检查自己新增的一侧；未决项数恰达 `M` 后拒绝新预留。
- 精确读在无未决项和有未决项时的返回，以及错误优先级和拒绝操作不改状态。
- 6 个未决项（在“10 个以内”范围内）的全部提交子集与全部顺序，共 1957 种真实重放。
- 跨账户事务、同序列重放一致性，以及并发预留、提交、中止、读的余额守恒和竞态安全。
