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

## 基本块局部值编号器（Local Value Numbering）

实现在 `ontology/lvn.go`（包 `ontology`），入口为 `ontology.NewBlock()`，
通过 `Block.Append(Ins)` 按指令顺序追加、`Block.Seal()` 封口、
`Block.Query()` 获取一致快照；三个方法均可并发调用，内部用互斥锁串行化，
结果等价于某个串行执行顺序。

指令用构造函数创建：`Const(c)`、`Load(s)`、`Add(a,b)`、`Mul(a,b)`、
`Sub(a,b)`、`Store(s,v)`、`Call()`。每条产生值的指令返回 `Result{VN,
Reused, Key, Reason}`；值编号从 1 起连续分配，`STORE` 不产生值（`VN=0`），
复用与被拒绝的指令均不占号。

### 键的规范化

判定依据是「（操作，规范化操作数）」表键：

- `CONST c`：键为数值 `c`，如 `CONST 42`。
- `ADD`/`MUL`（可交换）：两个操作数按值编号升序规范化，如 `ADD(1,3)`；
  `ADD 3 1` 与 `ADD 1 3` 命中同一编号。
- `SUB`（不可交换）：保持原顺序，`SUB(1,2)` 与 `SUB(2,1)` 是不同键。
- `CONST`/`ADD`/`MUL`/`SUB` 是纯计算键，**不含调用纪元**：`CALL` 不会使其
  失效，跨 `CALL` 仍可复用。
- `CALL` 恒产生新值编号，永不复用。

### 存取转发、槽版本与调用纪元

每个槽维护「版本」（非冗余 `STORE` 计数，初值 0），全块维护「调用纪元」
（`CALL` 计数，初值 0）和「已知值」表（仅由 `STORE` 写入）：

- `LOAD s`：若槽 `s` 有已知值（最近一次 `STORE` 存入且之后无 `CALL`），直接
  转发该值编号并标记复用（键显示为 `SLOT s KNOWN->v`）；否则查表键
  `LOAD(slot=s, ver=该槽版本, epoch=调用纪元)`，命中则复用，未命中分配新编号。
  `LOAD` 的结果**不会**写回已知值表。
- `STORE s v`：若槽 `s` 的已知值恰为 `v`，标记为冗余存储（`Reused=true`），
  版本不变、状态不改；否则该槽版本加一，并把已知值更新为 `v`。
- `CALL`：调用纪元加一、清空全部已知值、分配一个全新值编号。纯计算键保留，
  因此纪元变化后只有 `LOAD` 键（带纪元）发生变化。

### 拒绝规则（按序只报第一个）

每次 `Append` 校验顺序固定为「已封口 → 非法操作数 → 负槽号」：

- 封口后再追加返回 `ErrSealed`；`Seal` 重复调用同样返回 `ErrSealed`，封口只能
  成功一次。
- 操作数引用不存在的值编号（`<1` 或 `>= 下一个待分配编号`）返回 `ErrBadValue`。
- 槽号为负返回 `ErrBadSlot`。

被拒绝的指令不改变值编号表、已知值、版本与纪元。

### 本地验证

```bash
# 详细日志：打印每条指令的输入、值编号/复用标记、表键与判定依据
go test -race -v ./ontology

# 覆盖率
go test -cover ./...

# 格式化与静态检查
gofmt -l .
go vet ./...
```

`ontology/lvn_test.go` 内的 `naiveModel` 是按上述规则独立书写的朴素模拟器；
所有固定场景（ADD/MUL 交换复用、SUB 交换不复用、STORE→LOAD 转发、冗余存储、
CALL 后同槽重读得新编号、两次 CALL 不合并、非法操作数/槽号及优先级、封口）
以及 200 组随机指令流（含非法指令）都与该朴素模拟器逐步对照，编号、复用标记、
表、已知值、版本与纪元必须完全一致；另有重放确定性测试与并发竞态测试。
