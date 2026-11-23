# CLAUDE.md

Project conventions, structure, and build/test commands live in
[AGENTS.md](./AGENTS.md).

## Output Format

- Keep responses concise and actionable.
- Use code blocks with the correct language tag.
- Prefer editing existing files over creating new ones.

## Tool Usage

- Read files before editing.
- Batch independent tool calls in one message.
- Use Grep/Glob for search, not Bash find/grep.

## Skills

Invoke the relevant skill before starting non-trivial work, when the agent
harness provides one.

| Skill | When to use |
|---|---|
| `/superpowers:brainstorming` | Before implementing any feature |
| `/superpowers:writing-plans` | Before multi-step tasks |
| `/superpowers:executing-plans` | When running a written plan |
| `/superpowers:systematic-debugging` | When encountering a bug |
| `/superpowers:requesting-code-review` | Before merging |
| Git workflow skill or tool | Start a feature branch or prepare an MR |
| Security review skill or tool | Before merging security-sensitive changes |

These commands are harness-specific; use the equivalent available in your
environment when a listed skill does not exist.

## Execution Pattern

For any non-trivial task:
1. Invoke the relevant skill first.
2. In one message: spawn agents, batch file ops, batch shell commands, batch todos.
