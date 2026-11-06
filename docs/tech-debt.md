# Tech debt

Known gaps Mu carries for now, and what to do when each is resolved.

## ACP SDK v0.6.0

Mu uses [`github.com/spachava753/acp-sdk`](https://github.com/spachava753/acp-sdk) and will not switch to another ACP SDK. These gaps get fixed in that SDK. After upgrading to a release that fixes one, make the Mu changes listed under it.

### Session updates can be handled out of order

**Problem:** The generated dispatchers call `jsonrpc2.Async(ctx)` first for every incoming message, notifications included, so each `session/update` runs on its own goroutine and they race. A test streaming 2,000 updates saw about 5–7% handled out of order.

Separately, the read loop completes responses immediately while notifications wait in a queue. A response can therefore reach the caller before the updates the agent sent ahead of it.

**Workaround in Mu:** [`internal/agent/transport.go`](../internal/agent/transport.go) delivers `session/update` itself, synchronously in wire order, from inside the transport's `Read`. Responses pass through the same loop, so they can't overtake earlier updates. `TestStreamedUpdatesStayInOrder` guards this.

**When the SDK orders notifications and holds responses behind them:**

1. Delete the `orderedConn` wrapper and use `acp.IOTransport` directly. `Process.Alive` relies on the wrapper's `ended` signal, so it needs another way to detect that the agent's output ended.
2. Add a `SessionUpdate` method to the `handler` in `internal/agent/agent.go`.
3. Keep `TestStreamedUpdatesStayInOrder`, and make sure undecodable updates still reach the protocol diagnostics (`TestProtocolDiagnosticsAreReported`).
4. Remove the bridge note from `AGENTS.md`.

### No `$/cancel_request`

**Problem:** The SDK neither sends nor handles `$/cancel_request`.

- An agent can't withdraw a pending permission request, so its card stays until the user answers it.
- When Mu abandons a request, the agent isn't told. Mu's protocol-level cleanup still applies: it closes the session after an abandoned prompt or load.

**When the SDK supports it:**

1. Check that `App.permission` answers `cancelled` when the agent withdraws a request (the handler's context ends), and add a test.
2. Remove the limitation from the "Permission requests" section of `docs/behavior.md`, and restore withdrawal in its list of reasons a request is answered as cancelled.
3. Add a test that the agent receives `$/cancel_request` when Mu abandons a request.

### Abandoned requests stay registered

**Problem:** When a call's context ends, the SDK returns the error but doesn't call `Connection.Retire`. The call stays in `outgoingCalls` until a response arrives or the process exits. Each abandoned listing or load on a kept saved-agent process leaves one small entry, and these add up while the process runs. Mu can't release them; `*acp.Client` exposes no way to do so.

**When the SDK retires them:** remove the note from `AGENTS.md`. Mu needs no code change.

### Select values have two types

**Problem:** A select option's `CurrentValue` decodes as `string`, but `acp.SelectSessionConfigOption` stores `SessionConfigValueId`.

**Workaround in Mu:** `selectValue` in `internal/desktop/view.go` accepts both.

**When the SDK uses one type:** simplify `selectValue` to that type.

## Pi adapter

### Listing reads Pi's session files

**Problem:** Pi's RPC mode has no command to list sessions, and its `SessionManager.list()` is TypeScript only. `internal/piacp/sessions.go` reads the JSONL files directly, which ties it to their format. Files it can't read are skipped, so a format change would show as sessions going missing from Import rather than as an error.

**When Pi's RPC mode can list sessions:** use that, and remove the file reading.

### A write shows all its text as added

**Problem:** Pi's `write` tool reports neither what it replaced nor a patch. The adapter can't read the file itself first, because its view of the tool starting can lag behind the write. So overwriting a file reads as adding all of its lines.

**When Pi reports a patch for writes:** `patchDiffs` picks it up without any change.

### Permission requests are assumed to be about the running tool

**Problem:** An extension's dialog carries no tool call ID, so `askClient` assumes it's about the tool now running. With tools running in parallel, it can name the wrong one.

### Terminal commands typed into a thread reach the model

**Problem:** Pi's RPC mode sends `/model`, `/tree` and the like to the model as plain text. The picker doesn't offer them, but nothing stops someone typing one. Only `/compact` is intercepted.
