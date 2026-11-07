# codex-mcp v0 实施计划

在 `/Users/buerka/Documents/codex_mcp`（当前为空目录）新建 Go 项目，实现一个 stdio MCP server，把 `codex exec` 包装成异步 job，并创建 public GitHub 仓库 `buerka/codex-mcp` 作为远端。

## 1. 关键决策

- **保留 6 个工具和异步 job**，不做工具面精简；上下文友好体现在返回内容，而不是砍功能。
- **执行层**：子进程 `codex exec --json`，最终回复用 `--output-last-message` 落盘。
- **传输**：仅 stdio，不做 HTTP。
- **模块/仓库**：`github.com/buerka/codex-mcp`，GitHub `buerka/codex-mcp`，public，MIT。
- **Go**：1.27.3；依赖 `github.com/modelcontextprotocol/go-sdk v1.8.0`。
- **默认安全**：sandbox 只暴露 `read-only` / `workspace-write`，不提供 `danger-full-access`；默认 `--ignore-user-config`，需要继承时用环境变量显式打开；不暴露任意 `-c` 配置。
- **不做**（v0 范围外）：HTTP、审批转发、app-server、审计日志、跨进程文件锁；这些留到 v1.1/v2。

## 2. 工具面（6 个）

| 工具 | 作用 | 关键参数 |
|---|---|---|
| `codex_run` | 创建 job | `prompt`、`cwd`、`thread_id`、`model`、`sandbox`、`profile`、`timeout_sec`、`ephemeral`、`images`、`add_dirs`、`output_schema` |
| `codex_status` | 一行状态，可选有界等待 | `job`、`wait_sec`（≤30） |
| `codex_result` | 最终回复，支持等待和顺序分页 | `job`、`wait_sec`、`offset`、`max_bytes` |
| `codex_output` | 按 id/offset 分页 final/events/stderr | `id`、`offset`、`length` |
| `codex_cancel` | 终止 job 进程组 | `job`、`grace_sec` |
| `codex_list` | 最近 job，一行一个 | `limit`、`state` |

工具描述全部一句话；字段描述控制在十个词以内；不使用 `structuredContent` 复制正文。

## 3. 上下文契约（核心）

按内容类型分开处理，不把 ssh-mcp 的 head/tail 无脑套到模型回复上：

- **元数据**：`codex_status` / `codex_cancel` / `codex_list` 一行，单行是设计目标。
- **最终回复（文本）**：默认上限 32 KB；不超过就**完整返回、多行保留**；超过则**顺序分页**，保留开头，给下一段 offset，绝不挖掉中间。
- **最终回复（JSON schema）**：默认上限 64 KB；超了顺序分页，**绝不返回半截 JSON**。
- **诊断信息（events/stderr）**：8 KB head+tail，中间省略，附 `codex_output` id 和 offset，和 ssh-mcp 一致。
- **错误**：一行摘要 + 2 KB stderr 尾部 + 分页提示。
- **日志**：默认只给 `events=N stderr=N last=...` 摘要；raw JSONL 必须显式通过 `codex_output` 分页。
- **典型轮次**：`codex_run` → `codex_result(wait_sec)`，通常两次；`codex_status` 只在需要看进度时调用。

示例：

```text
> codex_run {prompt:"fix failing tests", cwd:"/repo"}
job=cx-7 queued

> codex_result {job:"cx-7", wait_sec:20}
job=cx-7 exit=0 3m12s thread=0199… usage=12k/678
---
Fixed 3 failing tests...

> codex_result {job:"cx-7", offset:32768}
job=cx-7 exit=0 ...
---
<下一段完整内容>
```

存储与分页：

- `~/.codex-mcp/jobs/<id>/final.txt`
- `~/.codex-mcp/jobs/<id>/events.jsonl`
- `~/.codex-mcp/jobs/<id>/stderr.log`
- 目录 0700、文件 0600；单 job 大小上限；保留最近 N 个 job。
- `codex_output` 支持 `cx-7:final`、`cx-7:events`、`cx-7:stderr`，按字节分页。
- 所有响应先脱敏：私钥块、`sk-...`、`Bearer ...`。

## 4. 执行层

- 二进制发现：`CODEX_BIN` → PATH 里的 `codex` → 明确报错。不自动使用 ChatGPT.app 内置 alpha 二进制。
- argv 直传，无 shell：
  `exec --json --color never -C <cwd> -s <sandbox> [--approve-for-me] [--ignore-user-config] [-m model] [-p profile] [--ephemeral] [--add-dir ...] [-i ...] [--output-schema <job>/schema.json] [--output-last-message <job>/final.txt] [resume --all <thread_id>] "prompt"`
- 解析 JSONL 只关注 `thread.started`、`turn.completed`、`turn.failed`、`error`；其余事件写文件，不进上下文。
- `exec.CommandContext` + 进程组 kill；SIGTERM → grace → SIGKILL；`cmd.WaitDelay` 兜底。
- 超时默认 1800 秒，单次可调，上限 7200 秒。
- `--ephemeral` 时不能 resume；`resume` 用 `--all` 避免 cwd 过滤导致找不到线程（实现前用本地 CLI `--help` 验证参数顺序）。
- job 状态机：`queued → starting → running → succeeded | failed | timed_out | canceled`。
- 并发：默认 1，队列上限 32；进程内按 cwd 串行；v0 不做跨进程锁。

## 5. 安全

- 默认 sandbox `workspace-write`；只允许 `read-only` 和 `workspace-write`。
- `CODEX_MCP_ROOT` 可选；cwd/add_dirs/images 做 `EvalSymlinks` 后校验在根目录内，拒绝符号链接逃逸。
- 默认 `--ignore-user-config`（`CODEX_MCP_IGNORE_USER_CONFIG=0` 可继承用户配置，明确文档说明风险）。
- 不把工具参数映射成环境变量；`CODEX_API_KEY`/`CODEX_HOME` 只从服务端环境读取。
- 默认不传 `--skip-git-repo-check`；`CODEX_MCP_ALLOW_NON_GIT=1` 才允许。
- stdout 只走 MCP 协议；日志全部 stderr 或丢弃。

## 6. 代码结构

```text
codex_mcp/
  main.go                     # flags/env、version、signal、server.Run
  server.go                   # 6 个工具注册、schema、文本渲染
  internal/codex/
    runner.go                 # Runner 接口、RunSpec/RunResult
    exec_runner.go            # codex exec 子进程
    args.go                   # argv 构造、resume 语法
    events.go                 # JSONL 解码
    discover.go               # codex 二进制/版本探测
  internal/jobs/
    manager.go                # 队列、并发、按 cwd 串行
    job.go                    # 状态机、超时、取消
    store.go                  # job 注册表、保留与清理
  internal/output/
    render.go                 # final/JSON/diagnostic 三种策略
    redact.go                 # 私钥、sk-、Bearer 脱敏
    page.go                   # 文件分页
  internal/policy/policy.go   # 路径、sandbox、上限
  internal/proc/
    proc_unix.go              # 进程组
    proc_windows.go           # Job Object / taskkill 兜底
  internal/testdata/fakecodex/main.go
  main_test.go                # in-memory MCP 集成测试
  README.md
  LICENSE
  .gitignore
  .github/workflows/ci.yml
```

## 7. 环境变量

`CODEX_BIN`、`CODEX_HOME`、`CODEX_MCP_ROOT`、`CODEX_MCP_TIMEOUT`、`CODEX_MCP_MAX_CONCURRENCY`、`CODEX_MCP_MAX_FINAL`（32768）、`CODEX_MCP_MAX_JSON`（65536）、`CODEX_MCP_MAX_DIAG`（8192）、`CODEX_MCP_MAX_PROMPT`（65536）、`CODEX_MCP_RETENTION`（100）、`CODEX_MCP_IGNORE_USER_CONFIG`（1）、`CODEX_MCP_ALLOW_NON_GIT`（0）。

## 8. 测试与 CI

- 假 Codex：Go 测试助手，按 `FAKE_CODEX_SCENARIO` 输出 JSONL、写 final、sleep、退出码；覆盖成功、失败、超时、取消、大回复、坏 JSON。
- 单元：argv、事件解析、final/JSON/diagnostic 三种渲染、分页、脱敏、路径策略、状态机。
- 集成：go-sdk in-memory transport，走 run→result、status、output、cancel、list；验证 stdout 只有协议数据。
- 进程：取消杀进程树、超时无残留；Unix 实测，Windows 编译 + CI。
- CI：`gofmt -l`、`go vet`、`go test -race`、`go build`；ubuntu 跑测试，windows 只编译。

## 9. GitHub

- 先 `gh auth status` 和 `gh repo view buerka/codex-mcp`；已存在则复用，不覆盖。
- `git init -b main`，测试通过后提交；`gh repo create buerka/codex-mcp --public --source=. --remote=origin --push`。
- 校验 `gh repo view buerka/codex-mcp --json url,visibility` 返回 public。
- 不提交凭证、job 数据、构建产物；`.gitignore` 覆盖 `codex-mcp`、`*.test`、`.DS_Store`。

## 10. 实施顺序与验收

1. 脚手架 + go.mod + version + signal。
2. ExecRunner + JSONL 解析 + 假 Codex 测试。
3. Job Manager + 状态机 + 超时/取消 + 输出存储。
4. 6 个工具 + 三种渲染策略 + 分页。
5. 安全策略 + Windows 进程处理。
6. README、LICENSE、CI。
7. 本地验证：`gofmt -l .` 无输出、`go vet ./...`、`go test -race ./...`、`go build`。
8. 初始化 git、提交、创建 public 仓库并 push。

验收：`codex_run` 立即返回 job；`codex_result(wait_sec)` 能拿到完整多行回复；长回复顺序分页不丢中间；JSON 回复不会被截成非法 JSON；events/stderr 默认摘要、按需分页；取消能杀进程组；stdout 无杂质；GitHub 仓库为 public。

## 假设

- GitHub owner 用 `buerka`，仓库名 `codex-mcp`（与现有 `ssh-mcp` 命名一致）；本地目录保持 `codex_mcp`。
- `gh` 已安装并登录；如果未登录会停下来报告，不会擅自使用其他凭据。
- 测试不调用真实 Codex、不消耗额度；真实冒烟需要你本地有 `codex` 且已登录，默认跳过。
- 如果 `codex exec resume --all` 的实际参数顺序与资料不符，以本地 CLI `--help` 为准，适配器集中在一处调整。