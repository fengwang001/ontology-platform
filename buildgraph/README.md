# buildgraph — 构建图脏判定器

`buildgraph` 根据文件修改时刻、三类输入依赖与构建日志判定哪些构建边需要
重建，并在重建（`Complete`）后支持重新核对。

## API

- `New() *Graph`
- `AddEdge(id, cmd, outputs, explicit, implicit, orderOnly, restat)`
- `SetMtime(path, t)` / `Remove(path)`
- `Complete(id, outputs)`
- `DirtySet(targets) ([]string, error)`，返回脏边编号，按字节序升序
- `NaiveDirtySet(targets)`：按定义逐边无记忆递归求值，仅用于对拍测试
- `EvalCount()`：最近一次 `DirtySet` 实际求值的边数

## 三类输入

- **显式输入（explicit）**：修改时刻进入「输入最大值」；其产出边脏会传播到本边。
- **隐式输入（implicit）**：语义同显式（如头文件依赖）。
- **仅排序输入（order-only）**：修改时刻**不**进入输入最大值，其产出边脏
  **不**使本边脏；但该产出边仍在传递闭包内，会被包含在 `DirtySet` 的
  返回集合中；闭包缺源检查与成环判定也会沿它走。

## 自身脏 vs 脏

- **自身脏**满足任一条件：
  1. 日志中没有该边；
  2. 日志命令与当前命令不等；
  3. 任一输出不存在；
  4. 非 restat 边：各输出修改时刻的最小值严格小于当前输入最大值；
  5. restat 边：日志 `inMax` 严格小于当前输入最大值（不看输出时刻）。

  相等一律不算脏。「输入最大值」只对存在的显式与隐式输入取，没有可取者为 0。

- **脏** = 自身脏，或任一显式/隐式输入路径的产出边为脏。仅排序输入不传播脏。

## restat 与日志 inMax

日志对每条边记录最近一次 `Complete` 时的 `(命令, inMax)`。restat 边重建后
若输出内容未变、输出时刻停留在旧值，只要新的日志 `inMax` 已追上当前输入
最大值，该边与其下游即转为不脏——这正是「restat 上游 Complete 且输出时刻
未变后下游由脏转不脏」的依据。

## 错误与优先级

所有被拒绝的操作都不改变修改时刻、日志与图。原因可用 `errors.Is` 区分。

- `AddEdge`：空编号 → 编号已存在 → 输出为空 → 任一路径为空 → 输出已被
  其他边产出 → 同一路径既是输出又是输入 → 加入后三类依赖成环。
- `Complete`：边不存在 → 输出键集不恰为全部输出 → 时刻小于 1 →
  显式/隐式输入缺失。成功时把输出置为给定时刻，并记录
  `(当前命令, 当前显式与隐式输入时刻最大值)`。
- `SetMtime`：空路径 → `t < 1`。`Remove` 空路径被拒；删除本不存在的
  路径是无变化的成功。
- `DirtySet`：先报第一个（按目标列表序）「既不是任何边输入也不是输出」的
  目标（`TargetError`，`errors.Is(ErrTargetNotInGraph)`）；再报闭包内缺失
  源（`MissingSourceError{EdgeID, Path}`，`errors.Is(ErrMissingInput)`），
  顺序为边号字节序升序，边内按显式、隐式、仅排序，列表内按下标。

## 并发与复杂度

所有方法持 `sync.RWMutex`，并发调用等价于某个串行顺序，重放同序列结果
完全一致。闭包、环检测与脏求值全部使用显式栈/队列的迭代算法，深度
20000 的线性链不会栈溢出；一次 `DirtySet` 内每条边最多求值一次
（Kahn 拓扑求值，`EvalCount()` 恰等于闭包边数）。

## 本地验证

```bash
# 全量
go test ./...

# 竞态
go test -race ./...

# 对拍与判定依据日志（2000 组随机图）
go test -run TestRandomDifferential -v ./buildgraph/

# 深链 / 菱形求值次数（1000 与 20000 条边）
go test -run 'TestDeepChain|TestDiamond' -v ./buildgraph/

go vet ./...
gofmt -l .
```
