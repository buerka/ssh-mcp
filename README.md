# ssh-mcp

A lightweight, high-performance MCP (Model Context Protocol) server that gives
coding agents — ZCode, Codex, Claude Code, Cursor, … — secure access to your
SSH hosts. Written in Go, shipped as a single static binary.

## Design goals

- **Small context footprint.** Every tool response is plain text (never JSON)
  and capped at 8 KB by default: head + tail preserved, middle elided with an
  `ssh_output` id so the model can page through the full output only when it
  actually needs to. Tool descriptions are one-liners.
- **Loose permissions.** No command allowlists, no confirmation prompts. The
  security boundary is the SSH account itself — the agent runs commands as
  your configured remote user, exactly like you would.
- **Zero credential leakage.** No new credential store. Connections reuse
  `~/.ssh/config`, `~/.ssh/known_hosts` and your keys/agent. Host key
  verification is strict by default (relax with `--insecure`). Every tool
  response passes a redaction filter that scrubs private key material, and the
  server logs nothing by default.
- **Fast.** One persistent connection per host with keepalive, reused across
  tool calls and shared by exec/SFTP/forwarding; dial timeout 10 s, exec
  timeout 60 s (both overridable per call).

## Tools

| Tool | Purpose |
|---|---|
| `ssh_hosts` | list host aliases from `~/.ssh/config` (aliases only) |
| `ssh_exec` | run a shell command; returns `exit:N` + redacted, truncated output |
| `ssh_output` | page through a previously truncated output by id |
| `ssh_ls` | list a remote directory (SFTP) |
| `ssh_get` / `ssh_put` | download / upload a file (SFTP) |
| `ssh_forward` | start/stop a local port forward through an SSH host |

## Build

```sh
go build -o ssh-mcp .
```

Requires Go 1.25+.

## Wire it up

**ZCode** (`.zcode/mcp.json` or user config):

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

### Flags and environment

| Flag / env | Meaning |
|---|---|
| `--insecure` | skip `known_hosts` host key verification (dev only) |
| `--verbose` | log tool calls to stderr (redacted) |
| `SSH_MCP_MAX_OUTPUT` | response cap in bytes (default 8192) |

## Example session

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

## Security notes

- Credentials never appear in tool output; private key blocks are redacted
  from every response as a backstop.
- Host key checking uses `~/.ssh/known_hosts`; first connections to new hosts
  must be made once by hand (or accept `--insecure` knowingly).
- Password auth is intentionally unsupported. Keys and ssh-agent only.
- The server is stdio-only: it never opens a network port of its own, and
  created port forwards bind to `127.0.0.1` only.

## License

MIT
