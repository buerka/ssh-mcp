# ssh-mcp

A lightweight, high-performance MCP (Model Context Protocol) server that gives
coding agents — ZCode, Codex, Claude Code, Cursor, … — secure access to your
SSH hosts. Written in Go, shipped as a single static binary.

轻量、高性能的 MCP（Model Context Protocol）服务器，让 ZCode、Codex、Claude
Code、Cursor 等编码 agent 安全地操作你的 SSH 主机。Go 编写，单个静态二进制。

## Design goals 设计目标

- **Small context footprint.** Every tool response is plain text (never JSON)
  and capped at 8 KB by default: head + tail preserved, middle elided with an
  `ssh_output` id so the model can page through the full output only when it
  actually needs to. Tool descriptions are one-liners.
  **省模型上下文。** 工具响应一律纯文本（不用 JSON），默认截断到 8 KB：保留
  头尾、中间省略并附带 `ssh_output` id，模型真正需要时才按需分页取回。工具
  描述每条只有一句话。
- **Loose permissions.** No command allowlists, no confirmation prompts. The
  security boundary is the SSH account itself — the agent runs commands as
  your configured remote user, exactly like you would.
  **权限松散。** 不做命令白名单、不加确认环节。安全边界就是 SSH 账号本身：
  agent 以你配置的远程用户身份执行命令，和你本人登录没有区别。
- **Zero credential leakage.** No new credential store. Connections reuse
  `~/.ssh/config`, `~/.ssh/known_hosts` and your keys/agent. Host key
  verification is strict by default (relax with `--insecure`). Every tool
  response passes a redaction filter that scrubs private key material, and the
  server logs nothing by default.
  **凭据零暴露。** 不引入任何新凭据存储。连接复用 `~/.ssh/config`、
  `~/.ssh/known_hosts` 与你的密钥/agent。主机密钥默认严格校验（可用
  `--insecure` 放宽）。所有响应经过私钥脱敏过滤器，默认零日志。
- **Fast.** One persistent connection per host with keepalive, reused across
  tool calls and shared by exec/SFTP/forwarding; dial timeout 10 s, exec
  timeout 60 s (both overridable per call).
  **高性能。** 每主机一条持久连接（带 keepalive），跨工具调用复用，exec/SFTP/
  转发共享；连接超时 10 秒、命令超时 60 秒，均可按调用覆盖。

## Tools 工具

| Tool 工具 | Purpose 用途 |
|---|---|
| `ssh_hosts` | list host aliases from `~/.ssh/config`（仅列主机别名，不含凭据） |
| `ssh_exec` | run a shell command; returns `exit:N` + redacted, truncated output（执行命令，返回退出码 + 脱敏截断输出） |
| `ssh_output` | page through a previously truncated output by id（按 id 分页取回被截断的输出） |
| `ssh_ls` | list a remote directory over SFTP（列远程目录） |
| `ssh_get` / `ssh_put` | download / upload a file over SFTP（下载 / 上传文件） |
| `ssh_forward` | start/stop a local port forward through an SSH host（建立/停止本地端口转发） |

## Build 构建

```sh
go build -o ssh-mcp .
```

Requires Go 1.25+.（需要 Go 1.25 或更高版本。）

## Wire it up 接入

**ZCode** (`.zcode/mcp.json` or user config / 用户级或项目级配置):

```json
{
  "mcpServers": {
    "ssh": {
      "command": "/path/to/ssh-mcp"
    }
  }
}
```

**Codex** (`~/.codex/config.toml`):

```toml
[mcp_servers.ssh]
command = "/path/to/ssh-mcp"
```

**Claude Code:**

```sh
claude mcp add ssh -- /path/to/ssh-mcp
```

### Password auth (optional) 密码登录（可选）

Keys are preferred, but if a host only supports passwords, store one in a
`0600` file next to your SSH config. The server adds `ssh.Password` auth on
top of keys/agent when a password is found — keys are still tried first.

优先用密钥，但若某台主机只支持密码，可把密码存进 SSH 配置旁的 `0600` 文件。
找到密码时服务器会在密钥/agent 之上追加 `ssh.Password` 认证——密钥仍然先试。

```sh
ssh-mcp password set web1   # hidden prompt; saved to ~/.ssh/ssh-mcp.json (0600) 隐藏输入，存入 0600 文件
ssh-mcp password del web1   # remove 删除
ssh-mcp password list       # host names only, never passwords 只列主机名，绝不显示密码
```

- Passwords never appear in tool output, logs or errors; the file is rejected
  as unsafe if its permissions are looser than `0600`.
  密码绝不出现在工具输出、日志或错误信息里；文件权限一旦宽于 `0600` 会拒绝
  读取并提示修复。
- Connection order: ssh-agent → identity files → password (when stored).
  认证顺序：ssh-agent → 密钥文件 → 密码（已存储时）。

### Flags and environment 参数与环境变量

| Flag / env | Meaning 含义 |
|---|---|
| `--insecure` | skip `known_hosts` host key verification (dev only)（跳过主机密钥校验，仅限开发） |
| `--verbose` | log tool calls to stderr, redacted（向 stderr 输出脱敏后的调用日志） |
| `SSH_MCP_MAX_OUTPUT` | response cap in bytes, default 8192（单次响应字节数上限，默认 8192） |

## Example session 使用示例

```
> ssh_hosts
web1
db1

> ssh_exec {host: web1, command: "uname -a"}
exit:0
Linux web1 6.8.0 ... x86_64 GNU/Linux

> ssh_exec {host: web1, command: "cat huge.log"}
exit:0
<head 5KB>
...[61440 bytes omitted; fetch with ssh_output id=out-3 offset=5461]...
<tail 2.5KB>
```

## Security notes 安全说明

- Credentials never appear in tool output; private key blocks are redacted
  from every response as a backstop.
  凭据绝不出现在工具输出里；每个响应都会经过私钥块脱敏兜底过滤。
- Host key checking uses `~/.ssh/known_hosts`; first connections to new hosts
  must be made once by hand (or accept `--insecure` knowingly).
  主机密钥校验基于 `~/.ssh/known_hosts`；新主机首次需手动连接一次（或明确
  接受 `--insecure`）。
- Password auth is optional and off by default; keys and ssh-agent come
  first. Stored passwords live in `~/.ssh/ssh-mcp.json` with mode `0600`.
  密码登录默认关闭、完全可选，密钥与 ssh-agent 永远优先。已存密码放在
  `~/.ssh/ssh-mcp.json`，权限 `0600`。
- The server is stdio-only: it never opens a network port of its own, and
  created port forwards bind to `127.0.0.1` only.
  服务器只走 stdio，自身不监听任何网络端口；端口转发仅绑定 `127.0.0.1`。

## License

MIT
