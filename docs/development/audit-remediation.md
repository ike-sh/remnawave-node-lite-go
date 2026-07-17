# M8 审计整改计划

本文记录 `0.1.0` 发布前的静态整改边界。审计基线为本地提交
`489eb258faa1e804b959b94b8c28fe5dbe4782c0`，官方行为参考固定为
`@remnawave/node 2.8.0@596f015a5c8f876dc9a9d61b6cb78d35bd8e379b`。

当前候选不是发布候选。只有本计划中的静态整改完成、门禁重新变绿，并对冻结后的
同一提交完成真实 Linux、Panel 2.8.1 和资源验收后，才能进入 `v0.1.0` 发布流程。

## 工程原则

- 官方实现定义外部兼容目标，不定义本项目的内部架构。官方已有的失效开放、错误吞噬、
  无界资源或不可重试行为不得照搬。
- 生命周期、进程、规则和状态必须有唯一所有者。跨组件不变量由业务所有者强制执行，
  不依赖 HTTP handler 按约定获取若干互斥锁。
- 所有成功 spawn 的进程必须在返回前登记所有权，并且只有确认 leader 与后代均已清理后
  才能报告 stopped 或撤销防火墙规则。
- 所有 mutation 必须绑定明确的 Xray generation。RPC 生效与本地 hash/state commit 不得跨代。
- 防火墙更新遵循 fail-closed、plan/apply/reconcile/commit；失败必须可见、可回滚、可幂等重试。
- 所有来自 HTTP、Panel、插件配置、rw-core webhook、文件和外部命令的数据都必须有字节、
  条目、深度、并发、时间和输出边界。错误与日志本身也属于资源预算。
- `512 MiB RAM / 1 vCPU / 2 GiB disk` 是整机目标。Go heap 限制不能替代 core、cgroup、
  内核 nftables、页缓存、安装临时文件和日志的整机预算。
- 兼容性 oracle 必须独立于 Go 实现生成。复制实现常量或手写 schema 后自我比较不构成证据。

## 整改阶段

1. **输入与部署安全**：移除 OpenRC root shell source；校验 Secret；限制请求数组、validation
   issue、JSON 深度、插件展开和日志字段。
2. **Xray 生命周期**：实现 stop 抢占 start、spawn 原子登记、后代清理成功条件、generation
   lease 和统一的跨组件协调器。
3. **Plugin 与连接正确性**：修复 webhook 过载反馈、nft 幂等删除、动态 block 保留、
   core-first cleanup 和可重试 socket drop。
4. **官方行为对齐**：修复 stats reset、protobuf wire、HTTP parser、响应模型、系统信息、
   JWT 和 Unix config target 等已确认偏差。
5. **运维资源边界**：补齐 OpenRC cgroup、低内存升级迁移、下载/解压/磁盘预检、可靠停止、
   回滚和进程身份健康检查。
6. **可信测试链**：从锁定官方源码和 SDK 生成 oracle，使用有 setup/assert/teardown 的场景
   runner，evidence 绑定逐 case 结果、退出码、日志摘要和二进制摘要。
7. **代码冻结与验收**：全量 test/race/vet/static analysis/双架构构建通过后冻结提交；最后执行
   systemd、OpenRC、Panel 2.8.1、真实 rw-core、50k 用户和至少 24 小时整机 soak。

## 提交策略

- 每个提交只关闭一个可解释的领域问题，并同时包含对应回归测试。
- 架构迁移先提交所有者与接口，再提交调用方，禁止长期保留双重真相源。
- 每阶段结束运行该阶段门禁并创建本地 checkpoint；最终验收前不创建 `v0.1.0` tag。
- 任何新发现的 P1/P2 立即加入当前阶段；与当前领域无关的清理不得混入修复提交。

