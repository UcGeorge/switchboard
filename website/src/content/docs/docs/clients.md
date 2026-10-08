---
title: "Configure agent clients"
description: "Register Switchboard with Claude Code, generic MCP clients, Desktop bridges, and Codex."
---

These commands configure the **agent side** of Switchboard. They do not configure a chat application's OpenAI provider. For that side, use [OpenAI application integration](../openai/).

## Claude Code

```sh
switchboard mcp add claude-code
switchboard skill install
```

`mcp add` creates an agent token and invokes the local `claude mcp add` command with HTTP transport. The default registration scope is `user`; choose `--scope local` or `--scope project` when desired. `--print` prints the command for inspection instead of invoking Claude Code. If Claude Code is not on PATH, Switchboard prints the command to run manually.

Load the updated MCP configuration in a new agent session, then ask it to serve requests. `skill install` writes the serving instructions into `~/.claude/skills/switchboard-agent/SKILL.md`. Use `--project /path/to/project` to install there instead.

## Remote instance, local agent

```sh
export SWITCHBOARD_URL=https://switchboard.example.com
export SWITCHBOARD_ADMIN_TOKEN=sbc-your-admin-token
switchboard mcp add claude-code
switchboard skill install
```

The token is created remotely, while Claude registration and the skill file remain local. To register using an existing agent token without administration access:

```sh
switchboard --url https://switchboard.example.com mcp add claude-code   --token sba-existing-agent-token
```

See [remote operation](../remote/) for credential setup.

## Generic HTTP MCP clients

`switchboard mcp config cursor`, `windsurf`, or `json` prints:

```json
{
  "mcpServers": {
    "switchboard": {
      "url": "http://127.0.0.1:8080/mcp",
      "headers": { "Authorization": "Bearer sba-your-token" }
    }
  }
}
```

Paste into the client's MCP configuration. Client schemas differ: inspect the generated configuration and adapt it to the version you use. In particular VS Code generally expects a `servers` map rather than `mcpServers`; the CLI's `vscode` format generates that map. Reload the MCP session after editing.

## Claude Desktop bridge

`switchboard mcp config claude-desktop` prints a stdio bridge configuration using `npx -y mcp-remote` to connect to the HTTP endpoint. That bridge needs Node on the client machine. OAuth-enabled HTTP connectors can add the URL directly instead. Hosted clients need a publicly reachable HTTPS deployment; they cannot reach your laptop's `127.0.0.1`.

## Codex

`switchboard mcp config codex` prints a TOML snippet and identifies the agent-token environment variable. Put the stanza in the client's configuration and make the variable available to its process. This configures the transport; the agent still needs the [serving instructions](../mcp/).

## Avoid credential leakage

Generated configurations contain tokens. Keep them out of committed project files. Prefer user-scoped configuration or a client's environment-backed secret handling. Static tokens do not expire by default; revoke them when the serving instance is retired.

Upstream configuration details: [VS Code MCP configuration](https://code.visualstudio.com/docs/agents/reference/mcp-configuration).
