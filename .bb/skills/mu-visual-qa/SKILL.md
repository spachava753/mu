---
name: mu-visual-qa
description: "Capture and visually inspect Mu's native UI using its offline Go test harness. Use for Mu UI changes, layout or interaction regressions, common user workflows, screenshots, and animation QA. Not for backend-only changes without visible effects, web pages, or general desktop screenshots."
---

# Mu visual QA

Use the existing MyGo harness, not a second test framework or a mock UI. It runs Mu's actual `View.Draw` with scripted input and an offline ACP fixture. These are headless application renders, not captures of an OS window.

## Run it in its own thread

Visual QA runs in a BB thread of its own, so that its judgment of the screens isn't anchored on the implementation's. Unless the user chooses otherwise, that's **Claude Code, Opus 5.5, high**: provider `claude-code`, model `claude-opus-5-5`, reasoning level `high`. Check the target environment lists them (`bb provider models claude-code --environment <env> --json`) and ask before using anything else.

It is never the review-loop reviewer, or any other thread: each has its own job and its own context. Reuse the QA thread for later rounds of the same work, with `bb thread tell`, rather than starting another.

If you are the thread making the change, spawn it with a brief and continue other work:

```sh
bb thread spawn --project <project> --environment <this environment> --parent-self \
  --provider claude-code --model claude-opus-5-5 --reasoning-level high \
  --parent-notifications auto --visibility visible \
  --title 'Visual QA: <change>' --prompt-file <brief> --json
```

Use the same environment, so the thread sees the uncommitted change. The brief says what changed and what it's for, which screens and workflows it touches, the base commit, and that the thread is the QA thread for it, so it follows this skill from [Choose coverage](#choose-coverage) on rather than spawning another. Don't send your own conclusions about how it looks.

If you are the QA thread, do the QA: capture, inspect, and walk the workflows below. Don't change project files, which the thread that made the change owns. Write captures and anything else under `.qa/`. Report what you found and where, with links to the images. For each workflow the change touches that no `TestWorkflow*` script covers, propose the script's steps and assertions, so that the thread making the change can add it to `workflows_test.go`. End with what you checked and an explicit verdict: findings remain, or there are none. On a later round, check the fixes, then look at everything again.

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
| Worktrees: choosing what to work on, preview, listing, deleting and refusal | `TestWorktreesUI`, `TestWorktreeFolderSetting` in [worktrees_test.go](../../../internal/desktop/worktrees_test.go) | `worktree-*` |
| Connecting a new thread: failure, sending while it starts | `TestFailedConnectionOffersRetry`, `TestEnterWhileConnectingSendsWhenReady` in [composer_test.go](../../../internal/desktop/composer_test.go) | `connection-failed`, `send-when-ready*` |
| Slash completion and composer focus | `TestSlashCommandPicker` in [composer_test.go](../../../internal/desktop/composer_test.go) | `slash-commands-*` |
| Add to prompt popup on a reply's selection, and the quote in the draft | `TestAddSelectionToPrompt` in [composer_test.go](../../../internal/desktop/composer_test.go) | `add-to-prompt-*` |
| Attachments: cards in a draft, many files, a file being read, files in a sent message, composing during a turn | `TestAttachmentsFollowTheText`, `TestManyAttachmentsLeaveTheComposerInTheWindow`, `TestSendWaitsForFilesToBeRead`, `TestAttachingDuringATurn`, `TestComposingDuringATurn` in [attachments_test.go](../../../internal/desktop/attachments_test.go) | `attachments-*`, `composing-during-turn-*` |
| Queued prompts: in order, steering, held after Stop, long queue, light and dark | `TestWorkflowQueuePrompts`, `TestWorkflowSteer`, `TestWorkflowStopHoldsTheQueue`, `TestWorkflowCorrectionWhileStopping`, `TestWorkflowLongQueue` in [workflows_test.go](../../../internal/desktop/workflows_test.go), `TestQueuedPromptOpens` in [motion_test.go](../../../internal/desktop/motion_test.go) | `workflow-queue-*`, `workflow-steer-*`, `workflow-stop-holds-queue-*`, `workflow-correction-while-stopping-*`, `workflow-long-queue-*` |
| Discovery and selective import, scrolled list | `TestSelectiveSessionImportUI` in [imports_test.go](../../../internal/desktop/imports_test.go) | `session-import-*`, `session-import-scrolled-*` |
| Motion, reversal, reduced motion | Tests in [motion_test.go](../../../internal/desktop/motion_test.go) and [disclosure_test.go](../../../internal/desktop/disclosure_test.go) | `TestRecordMotion` writes `mu-motion.gif`; the other tests assert intermediate states |

Choose scenarios affected by the change. For a general visual review, run all static capture scenarios below. Layout scenarios cover 800×600 and 1100×780; the existing Settings capture is only 1100×780. Light/dark captures currently come from the conversation/sidebar scenario, not every screen. Extend coverage when an affected screen, size, or appearance is missing; do not claim it was tested merely because another scenario covers it.

## Walk the common workflows

A screen can look right in every state and still fail the person using it. Attachment QA passed every capture while the paperclip disappeared during a turn, which is when someone gets the next prompt ready. So for any UI change, also walk the workflows below that touch what changed, through the real controls and in order, as someone doing that work would.

At each step, and especially when a turn or connection starts or ends:

- Whatever the person reaches for next is there and enabled, and focus is where they would start typing. Compare the controls before, during and after the change of state.
- Nothing they entered is lost or sent early, and the composer doesn't jump.
- Capture the states between steps, not only the end, at both window sizes, and in light as well as dark for anything that looks different.
- Run the whole list against a fix for a visual finding too: a fix can remove what a workflow needs, as hiding that paperclip to stop a layout jump did.

The `TestWorkflow*` tests in [workflows_test.go](../../../internal/desktop/workflows_test.go) script some of these end to end at both window sizes, asserting each step, so they need no agent walking them when they pass: run them, and look only at what changed. With `MU_QA_RENDER_DIR` set they also capture every step as `workflow-<name>-<step>-<width>`. Prefer adding to that file over walking a workflow by hand.

The last column names the script, or otherwise the closest scenario, which covers pieces at most. Where a workflow has no script, add one, or report the workflow as not walked. Never count it as covered because its parts are.

| Workflow | Steps | Watch for | Closest scenario |
| --- | --- | --- | --- |
| New thread, first message | Create a thread, type, send | Focus goes to the draft; sending while the agent starts goes out once it's connected | **Scripted:** `TestWorkflowNewThreadFirstMessage`, `TestWorkflowFirstMessageBeforeTheAgentStarts` |
| Choose settings first | Create a thread, change the model or thinking level, type, send | Options appear before the first message; "Updating session…" doesn't lose what's typed or jump the composer; the turn uses the chosen value | **Scripted:** `TestWorkflowChooseSettingsFirst`, `TestWorkflowChooseSettingsWhileWriting` |
| Abandon a new thread | Create a thread, at once create another, then remove the first or just navigate away | Each action works while the first is still connecting, or says why not; no error lands on the wrong thread; the first's title and session stay correct when it finishes | **Scripted:** `TestWorkflowAbandonNewThread`, `TestWorkflowNavigateAwayFromAStartingThread` |
| Prepare the next message during a turn | While the model generates, write a prompt and attach files, then send it once the turn ends | The draft keeps the focus; typing, paste, slash completion, the paperclip, cards and remove buttons stay usable; nothing goes until Enter | **Scripted:** `TestWorkflowPrepareTheNextMessageDuringATurn`, `TestWorkflowNextMessageWhenATurnEndsByItself` |
| Queue prompts | During a turn, press Enter on several prompts, with files, and let the turn end | The draft empties, keeps the focus and doesn't move; prompts show in order, with their files, and go one per turn; a long queue scrolls and leaves the draft and Stop in the window; a queue goes on in a thread out of view; queueing leaves a reader where they scrolled, and steering goes to the latest | **Scripted:** `TestWorkflowQueuePrompts`, `TestWorkflowLongQueue`, `TestWorkflowQueueOutOfView`, `TestWorkflowQueueWhileReadingBack` |
| Steer | During a turn with prompts queued, press Ctrl+Enter, then **Steer** on a queued prompt | The turn is interrupted and the prompt goes at once; what's queued stays, in order, and carries on; a slow stop shows it waiting | **Scripted:** `TestWorkflowSteer`, `TestWorkflowSteerWithNothingRunning`, `TestWorkflowCorrectionWhileStopping` |
| Stop with a queue | Queue prompts, stop, remove one, send a correction | Nothing goes by itself after Stop, and a line says why; the correction goes first and the rest follow it; a held prompt's **Send** works | **Scripted:** `TestWorkflowStopHoldsTheQueue`, `TestWorkflowCorrectionWhileStopping`, `TestWorkflowQueueHeldAfterAnError` |
| Switch models mid-conversation | Exchange messages, change the model, send, change it during a turn if it's offered | History stays; usage and options update; a rejected change keeps the old value and shows the agent's error | **Scripted:** `TestWorkflowSwitchModelMidConversation`, `TestWorkflowSwitchModelDuringATurn`, except a rejected change |
| Stop and correct | Send, stop mid-reply, write a correction, send | Stop responds at once; partial text stays; the draft is ready straight away; unfinished tool calls read as cancelled | **Scripted:** `TestWorkflowStopAndCorrect`, except tool calls, which `TestQAConversation` covers |
| Answer permissions | Send, get a permission card, read its details, answer, get another | The composer and Stop stay in the window beside several cards; the card names the tool; the turn resumes | `TestQAPermissionCards`, `TestComposerKeepsRoomWithPermissions` |
| Work across threads | Send in one thread, switch to another while it runs, send there, switch back | Each keeps its own draft, files, scroll and status; the sidebar shows which are working or need permission; nothing streams into the wrong thread | **Scripted:** `TestWorkflowSwitchThreadsWhileOneRuns`, except scroll and files, which `TestDraftsKeepTheirFilesAcrossThreads` covers |
| Read back during a long reply | While a reply streams, scroll up to read, then return | The view stays where the person scrolled; **Jump to latest** appears and works; selecting text keeps working | `TestAddSelectionWhileAgentWorks`, `TestAddSelectionFollowsScroll`; queueing while scrolled up is **Scripted:** `TestWorkflowQueueWhileReadingBack` |
| Attach and send | Text with an image; files alone; a file the agent refuses, then a follow-up | Cards show kind and size; **Send** waits for reads; a refusal shows in the thread and the session goes on | `TestAttachmentsFollowTheText`, `TestFilesAloneCanBeSent` |
| Quote and reply | Select part of a reply, **Add to prompt**, write around it, send | The popup is near the pointer; the draft keeps what was there; focus returns | `TestAddSelectionToPrompt` |
| Slash command | Type `/`, filter, pick with the keyboard, add arguments, send | Completion never sends; the picker leaves the composer visible | `TestSlashCommandPicker` |
| Long draft | Write many lines, paste a large block, send | The draft scrolls; options and **Send** stay in the window, also beside permission cards | `TestLongModelOptionsScroll`, `TestManyOptionsStayReachable` |
| Failed connection | Open a thread whose agent can't start, read the error, retry, send | The draft survives; **Retry connection** works; the error says what to fix | `TestFailedConnectionOffersRetry` |
| Come back later | Quit, reopen, open an old thread, read its history, continue it | History loads with a sign of progress; options match the session; a load failure offers a retry | `TestStdioLifecycle` in `internal/app` is below the UI; nothing walks it in the view |
| Branch off | Fork a thread, continue the fork, return to the source | The fork opens with its history; the source is unchanged and still usable | `TestQAThreadControls` |
| Tidy up | Rename, archive, restore and continue; select several and archive or remove | Busy threads say why they're skipped; a restored thread loads; removing the open thread leaves a sensible screen | `TestRenameThreadUI`, `TestBulkThreadActionsUI`, `TestBulkRestoreLoadsOpenThread` |
| Project work | New project, thread on a new branch in a worktree, send, delete the worktree | The form says what will happen; the sidebar shows setup; deleting refuses unsafe cases and archives the thread | `TestProjectsUI`, `TestWorktreesUI` |
| Pick up an existing session | Import sessions, select one, open it, continue | Nothing imports by itself; history loads on opening; the thread joins its project | `TestSelectiveSessionImportUI` |
| Rearrange the window mid-turn | During a turn, collapse or drag the sidebar and shrink the window to 800×600 | Status, Stop and the draft stay reachable; the streaming reply isn't disturbed | `TestSidebarDrag`, `TestSidebarAnimationAndReversal` |

## Capture fresh artifacts

Use a fresh ignored directory so old images cannot be mistaken for results of a failed run:

```sh
mkdir -p .qa &&
out="$(mktemp -d "$PWD/.qa/visual-qa-XXXXXX")" &&
MU_QA_RENDER_DIR="$out" go test ./internal/desktop \
  -run '^(TestMatteSidebarAndSessionControls|TestAgentSettingsUI|TestProjectsUI|TestLongModelOptionsScroll|TestComposerOffersEveryOption|TestManyOptionsStayReachable|TestComposerKeepsRoomWithPermissions|TestSidebarDrag|TestGitLine|TestPullRequestLine|TestWorktreesUI|TestWorktreeFolderSetting|TestFailedConnectionOffersRetry|TestEnterWhileConnectingSendsWhenReady|TestSlashCommandPicker|TestWorkflow.*|TestAddSelectionToPrompt|TestAttachmentsFollowTheText|TestManyAttachmentsLeaveTheComposerInTheWindow|TestSendWaitsForFilesToBeRead|TestAttachingDuringATurn|TestComposingDuringATurn|TestSelectiveSessionImportUI|TestQAConversation)$' \
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

Return clickable absolute-path links to representative PNGs and any requested GIF. State which scenarios, workflows, dimensions, themes, and interactions were checked, and which affected workflows weren't walked; summarize visual findings and any remaining gaps. Never present old artifacts or uninspected images as reviewed evidence.

Headless rendering does not verify OS window decorations, compositor behavior, real desktop focus, or desktop input routing. For a specifically requested live-window check, use isolated Mu data and the offline fixture, verify the target window before sending input, and keep it separate from this headless result. Never send blind keystrokes to the desktop.

There are no full-image golden baselines today. Temporary paths, timestamps, and session IDs vary across runs. Do not claim pixel-perfect regression coverage; stabilize or mask those fields deliberately before introducing image comparisons.
