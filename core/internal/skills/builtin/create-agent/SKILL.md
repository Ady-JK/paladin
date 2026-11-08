---
name: create-agent
description: Help the user design and create a file-based Paladin subagent with selected tools and MCP access.
---
# Create a Paladin subagent

Use the live capability list supplied with this skill. Ask the user what the agent should do, then help them choose a name, concise description, role, tools, MCP tools and `read-only` or `read-write` access. Offer sensible defaults but do not silently grant tools or servers beyond what the user chooses. List short capability descriptions, not full schemas. Explain unavailable MCP servers and never invent their tools.

Create `<agent definitions directory>/<name>/agent.json` using the directory supplied in the live context. Names use lowercase letters, digits, `_` or `-`, start with a letter, and must match the folder. For example:

```json
{
  "name": "test-reviewer",
  "description": "Reviews tests and reports missing cases",
  "permissions": "read-only",
  "tools": {"read": true, "grep": true, "glob": true, "list": true},
  "mcp": {"example-server": ["lookup"]},
  "role": "Review the requested tests. Report concrete gaps and file locations."
}
```

`tools` names are the exact built-in or extension names from the catalog. `mcp` maps server names to exact tool names; omit a server when none of its tools should be available. `"*"` selects all tools on a server, so use it only when the user explicitly chooses that scope. Use empty objects when no tools or MCP servers are selected. Role is the full user-authored role prompt. Tool and MCP selections remain subject to the parent's switches, accepted tools, permissions and plan mode. Subagents cannot spawn or manage other agents, even if such tools are selected. Read-only agents cannot use extension tools, the browser, memory writes, file writes, or mutating shell commands; they can use MCP tools only when the server marks them read-only. MCP hints are server-provided, not a guarantee. Read-write tools and MCP calls follow normal approvals and sandbox rules.

Do not overwrite an existing agent unless asked. Write valid JSON and verify the file and a suitable test. Newly created agent types appear in `spawn_agent` on the next turn; their definitions reload for new children. If writing the definitions directory requires approval, use the normal file approval flow.
