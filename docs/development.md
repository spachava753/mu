# Development

## Run from source

Requires **Go 1.27.1+**. On Linux, native windows need **GTK 3** and a graphical desktop; WebKitGTK is not needed. MyGo also supports Windows, but this version has been exercised on Linux and macOS only.

```sh
go run .
```

Or build a standalone executable:

```sh
go build -o bin/mu .
./bin/mu
```

Only one Mu can use a data directory at a time. To run a development copy alongside an installed one, give it its own with `--data-dir`.

## Release

`mygo.json` holds the version. Pushing a tag for it tests on Linux and macOS, then builds a draft release with [`.github/workflows/release.yml`](../.github/workflows/release.yml), for you to review and publish:

```sh
git tag v0.1.0
git push origin v0.1.0
```

The workflow needs the secret key of the pair whose public half is in `mygo.json`, once, in the repository's secrets. `mygo keygen` made it:

```sh
gh secret set MYGO_UPDATER_PRIVATE_KEY < ~/.config/mygo/update-keys/mygo-update.key
```

To build the packages locally, without publishing, run `go tool mygo build`, which writes them to `dist/`.

## Check

```sh
go test -race ./...
go vet ./...
go build -o bin/mu .
```

The normal suite needs no credentials or model calls. It includes a real stdio fixture process, settings persistence, session configuration and updates, agent replay and native fork behavior, paginated discovery/selective import, external deletion, permissions, graceful and forced cancellation, agent failures, worktree creation and deletion against real local repositories, and MyGo headless interaction tests at 1100×780 and 800×600. The UI tests exercise row highlights, sidebar collapse, saved agents, session option changes, 100-model menus, and slash completion with 80 commands using mouse and keyboard.

To save native-renderer QA screenshots in both sizes and themes:

```sh
MU_QA_RENDER_DIR="$PWD/.qa/session-ui" go test ./internal/desktop -run 'TestMatteSidebarAndSessionControls|TestAgentSettingsUI|TestLongModelOptionsScroll|TestSlashCommandPicker' -count=1
```

Motion tests sample intermediate frames, reversal, and reduced-motion behavior. To record an animated native-renderer demo (using only the offline fixture):

```sh
MU_QA_RENDER_DIR="$PWD/.qa/motion" go test ./internal/desktop -run '^TestRecordMotion$' -count=1
```

The code layout is described in [AGENTS.md](../AGENTS.md).
