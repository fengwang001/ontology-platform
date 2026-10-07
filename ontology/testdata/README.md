# reachability_queries.jsonl

由 `TestRandomDifferentialAgainstOracle` 生成的逐查询审计记录（每次运行覆盖重写）。
每行一个 JSON 对象，字段：

- `seed` / `step` / `epoch`：随机种子、操作步序号、查询线性化时的快照纪元；
- `caller` / `start` / `end`：查询输入；
- `outcome` / `reason`：生产实现的三态输出与三态分类依据；
- `ground_truth_path`：独立朴素穷举（`NaiveClassify`）给出的无权限真值；
- `start_visible` / `end_visible`：调用者对两端的存在性权限；
- `expected`：独立参照模型（`oracleModel`）的预期 `outcome/reason`；
- `match`：两实现是否一致（测试要求全部为 true）；
- `metrics`：本次查询实际尝试展开的对象数、检查/截断的链接数（认证级与真值级分列）。
