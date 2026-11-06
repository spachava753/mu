# Mu

A small native desktop client for coding agents that speak [ACP](https://agentclientprotocol.com), written entirely in Go.

- Run several threads at once, each in its own folder, a shared project folder, or a Git worktree.
- Follow replies as they stream, with thinking, plans, tool calls and diffs.
- Answer permission requests, change the options the agent offers, such as its model, and see the context usage and cost it reports.
- Import sessions from other clients, and fork or archive them. Conversations stay with the agent: Mu saves no transcripts.

## Install

**Linux** (needs GTK 3):

```sh
curl -fsSL https://github.com/spachava753/mu/releases/latest/download/install.sh | sh
```

Run it again to update, or add `-s -- --uninstall` after `sh` to uninstall.

**macOS**: download the `.dmg` from the [latest release](https://github.com/spachava753/mu/releases/latest) and drag Mu to Applications. Releases aren't notarized, so allow it once under **System Settings → Privacy & Security**.

## Get started

Mu doesn't install agents or sign in for them, so set up your agent first.

- **[Pi](https://pi.dev)** works out of the box if it's installed.
- **Any other agent** needs a command that speaks ACP v1 over stdio and supports listing, loading, closing and forking sessions. Add it under **Settings**.

Then choose **New thread**.

Agents run with your user's permissions, and Mu doesn't sandbox them, so only run ones you trust. Mu sends no telemetry.

## More

- [Behavior](docs/behavior.md): what Mu does, in detail
- [Glossary](GLOSSARY.md)
- [Development](docs/development.md): building, testing and releasing

## License

[MIT](LICENSE)
