---
name: mu-visual-qa
description: "Capture and visually inspect Mu's native UI using its offline Go test harness. Use for Mu UI changes, layout or interaction regressions, screenshots, and animation QA. Not for backend-only changes without visible effects, web pages, or general desktop screenshots."
---

# Mu visual QA

Use the existing MyGo harness, not a second test framework or a mock UI. It runs Mu's actual `View.Draw` with scripted input and an offline ACP fixture. These are headless application renders, not captures of an OS window.

## Choose coverage

Run commands from the repository root, not this skill directory. Read the relevant scenario and helpers before changing a test. The source links below are relative to this skill directory.

| Area | Tests and source | Captures |
| --- | --- | --- |
| Conversation, sidebar, session controls, expanded tool details, light/dark | `TestMatteSidebarAndSessionControls` in [features_test.go](../../../internal/desktop/features_test.go) | `matte-dark-*`, `tool-expanded-*`, `matte-light-*`, `collapsed-*`, `session-options-*` |
| Permission card and running turn (1100×780 only) | `TestQAConversation` in [qa_test.go](../../../internal/desktop/qa_test.go) | `permission-1100`, `running-1100` |
| Saved agents | `TestAgentSettingsUI` in [features_test.go](../../../internal/desktop/features_test.go) | `agent-settings.png` |
| Working directory, projects, new-thread form, project groups in the sidebar | `TestProjectsUI` in [features_test.go](../../../internal/desktop/features_test.go) | `project-settings-*`, `new-thread-*`, `new-thread-project-*`, `project-sidebar-*` |
| Long model menus, visible usage, long drafts | `TestLongModelOptionsScroll` in [composer_test.go](../../../internal/desktop/composer_test.go) | `scrollable-models-*`, `composer-usage-*`, `usage-details-*`, `composer-multiline-*` |
| Composer options: every type, many options, a full composer beside permissions | `TestComposerOffersEveryOption`, `TestManyOptionsStayReachable`, `TestComposerKeepsRoomWithPermissions` in [composer_test.go](../../../internal/desktop/composer_test.go) | `composer-options`, `composer-many-options`, `composer-full-with-permissions` |
| Sidebar drag: following, half-window limit, minimum, snap to rail | `TestSidebarDrag` in [motion_test.go](../../../internal/desktop/motion_test.go) | `sidebar-drag-*` |
| Git line below the composer, linked worktrees, pull requests (from a stand-in `gh`) | `TestGitLine`, `TestPullRequestLine` in [composer_test.go](../../../internal/desktop/composer_test.go) | `git-line-*`, `pull-request-*` |
| Connecting a new thread: failure, sending while it starts | `TestFailedConnectionOffersRetry`, `TestEnterWhileConnectingSendsWhenReady` in [composer_test.go](../../../internal/desktop/composer_test.go) | `connection-failed`, `send-when-ready*` |
| Slash completion and composer focus | `TestSlashCommandPicker` in [composer_test.go](../../../internal/desktop/composer_test.go) | `slash-commands-*` |
| Discovery and selective import, scrolled list | `TestSelectiveSessionImportUI` in [imports_test.go](../../../internal/desktop/imports_test.go) | `session-import-*`, `session-import-scrolled-*` |
| Motion, reversal, reduced motion | Tests in [motion_test.go](../../../internal/desktop/motion_test.go) and [disclosure_test.go](../../../internal/desktop/disclosure_test.go) | `TestRecordMotion` writes `mu-motion.gif`; the other tests assert intermediate states |

Choose scenarios affected by the change. For a general visual review, run all static capture scenarios below. Layout scenarios cover 800×600 and 1100×780; the existing Settings capture is only 1100×780. Light/dark captures currently come from the conversation/sidebar scenario, not every screen. Extend coverage when an affected screen, size, or appearance is missing; do not claim it was tested merely because another scenario covers it.

## Capture fresh artifacts

Use a fresh ignored directory so old images cannot be mistaken for results of a failed run:

```sh
mkdir -p .qa &&
out="$(mktemp -d "$PWD/.qa/visual-qa-XXXXXX")" &&
MU_QA_RENDER_DIR="$out" go test ./internal/desktop \
  -run '^(TestMatteSidebarAndSessionControls|TestAgentSettingsUI|TestProjectsUI|TestLongModelOptionsScroll|TestComposerOffersEveryOption|TestManyOptionsStayReachable|TestComposerKeepsRoomWithPermissions|TestSidebarDrag|TestGitLine|TestPullRequestLine|TestFailedConnectionOffersRetry|TestEnterWhileConnectingSendsWhenReady|TestSlashCommandPicker|TestSelectiveSessionImportUI|TestQAConversation)$' \
  -count=1 -v -timeout 120s &&
printf 'Screenshots: %s\n' "$out"
```

For a focused change, narrow `-run` to the relevant test names. Confirm those tests actually ran and passed; a successful command matching zero tests is not coverage. `-count=1` is required to bypass the test cache and produce fresh files.

For an animation recording, use the same command with `-run '^TestRecordMotion$'`. The GIF is more expensive than static captures; generate it for motion changes or when requested. For the complete desktop suite, omit `-run`; with `MU_QA_RENDER_DIR` set, this also records the GIF. These visual checks complement, not replace, the repository's required race/vet/build checks after code changes.

The tests use temporary app and agent data. Do not use normal Mu state, `.tools/` launch settings, or provider credentials. Do not start a real agent or make model requests to obtain screenshots. A QA-only task does not require wiping normal data.

## Make captures representative

When adding or adjusting a scenario:

- Reuse `ui.NewTester`, `testutil.Agent`, and the `click`, `waitView`, and `screenshot` helpers in [features_test.go](../../../internal/desktop/features_test.go).
- Drive actual controls by their labels. Assert the resulting application state and relevant widget bounds, not just that a screenshot file exists.
- Wait for asynchronous work to reach the intended state. Render a fresh frame after that condition succeeds; the preceding frame may contain an older snapshot.
- Advance the tester's animation clock before a settled capture. The current helpers advance 320 ms. Wall-clock sleeps do not advance headless UI animations.
- For motion checks, deliberately sample intermediate frames, interruption/reversal, and reduced-motion behavior instead of settling every frame.
- Include the open menu, scrolled list, focused composer, error, or empty state relevant to the change. A screenshot of the default screen is not evidence for a changed interaction.

## Open and inspect the images

Use the image-reading tool to open the fresh PNGs. Passing interaction tests alone is not visual QA. Inspect each affected state at both window sizes, plus relevant light/dark or accessibility preferences. For broad runs, review every affected screen family and state; report any captures not inspected.

Check:

- Text is legible; labels, controls, and action buttons fit within their intended containers.
- Long menus and lists scroll within the viewport. Partial rows at a scroll boundary are normal; unreachable actions or clipped popovers are not.
- Hover, selection, disabled, and keyboard-focus states are distinguishable.
- Slash suggestions leave the composer and context-usage indicator visible. Completion restores focus and inserts text without submitting it.
- Dialogs and popovers are positioned correctly and close through their intended interactions.
- Sidebar collapse, empty/error states, and import selection remain understandable at 800×600.
- Streamed text is not delayed by presentation effects. Motion respects reduced motion and does not jump during reversal.

For GIFs, inspect the animation or extract and inspect intermediate frames with available tools. If only the first frame can be viewed, say that motion was not visually reviewed.

When implementing a UI change, fix regressions within the task's scope and rerun the affected scenarios. For a standalone screenshot/review request, report issues without silently redesigning the application. Stop once the relevant checks pass and the rendered result has been inspected.

## Report evidence and limits

Return clickable absolute-path links to representative PNGs and any requested GIF. State which scenarios, dimensions, themes, and interactions were checked; summarize visual findings and any remaining gaps. Never present old artifacts or uninspected images as reviewed evidence.

Headless rendering does not verify OS window decorations, compositor behavior, real desktop focus, or desktop input routing. For a specifically requested live-window check, use isolated Mu data and the offline fixture, verify the target window before sending input, and keep it separate from this headless result. Never send blind keystrokes to the desktop.

There are no full-image golden baselines today. Temporary paths, timestamps, and session IDs vary across runs. Do not claim pixel-perfect regression coverage; stabilize or mask those fields deliberately before introducing image comparisons.
