# The Agentry spec

This folder is the specification of Agentry as it is today: what it does, for whom, and how well. It is the document AI agents build from and audit against, Agentry's own agents included. The application follows this document. Its baseline, every file outside `spec/proposals`, describes behaviour that exists on dev now, so a scenario there is a promise the code already keeps. `spec/proposals` holds what is agreed and not built yet, written the same way so it can be built from.

## How to read it

Each feature file is a list of scenarios in Given, When, Then form. A scenario names one behaviour: the Given states the conditions or the load, the When is what happens, and every Then is something a person or a program can check, with a number or a yes/no outcome. Read the functional files to learn what Agentry does, one feature to a file, and the non-functional files to learn how it holds up: how it fails, what it refuses, what it costs and how it is run.

The glossary gives each noun Agentry uses its one meaning, so the scenarios can use them without explaining them. The constraints say how Agentry is built and must stay built, and how each constraint is checked.

## The folder

| File | What it is about |
| --- | --- |
| `spec/README.md` | this file: the layout, the rules and how to run the check |
| `spec/glossary.md` | every noun Agentry uses, one line each |
| `spec/constraints.md` | how Agentry is built and must stay built, with how each constraint is checked |
| `spec/functional/tasks-and-board.feature` | adding a task, the board and its columns, a task's page, running limits, search, the keys and the archive |
| `spec/functional/epics-and-stories.feature` | epics, their stories, Plan and closing an epic |
| `spec/functional/approvals-and-questions.feature` | actions the policy held for the user, and the questions coordinators ask |
| `spec/functional/ideas.feature` | the Visionaries' ideas and how the user accepts or dismisses them |
| `spec/functional/schedules.feature` | schedules: their events, the state they fold to and when each runs |
| `spec/functional/teams-and-templates.feature` | teams, teams of the user's own, templates and each agent's page |
| `spec/functional/policy.feature` | the policy profiles, the gate's rules and the Policy page |
| `spec/functional/sessions.feature` | going on with a session, fresh sessions, forks and the live terminal |
| `spec/functional/coordinator-and-handoffs.feature` | the coordinator: a task's life, briefs, reports, issues, decisions, safety stops and limits |
| `spec/functional/agents.feature` | each agent's turn: engineer, reviewer, tester, deployer and the other teams' agents |
| `spec/functional/worktrees-and-branches.feature` | each task's worktree and branch, and the Workspace page |
| `spec/functional/workers.feature` | a machine joined as a worker: joining, the service, its commands, the link, where a turn runs, going offline |
| `spec/functional/shared-files.feature` | the files shared with a task, and how they follow it |
| `spec/functional/sign-in.feature` | sign-in codes, devices, installing the app and the desktop app |
| `spec/functional/notifications.feature` | pushes to the user's devices |
| `spec/functional/settings.feature` | the Settings page and redacting a text |
| `spec/functional/claude-code-tools.feature` | Agentry used from Claude Code: the install, the MCP tools and the status line |
| `spec/functional/demo.feature` | the demo build: Agentry's pages on a pretend server in the browser, for a website |
| `spec/non-functional/reliability.feature` | what happens when a turn, a process or a machine fails |
| `spec/non-functional/security.feature` | sign-in, credentials, the gate and what no agent may do |
| `spec/non-functional/operations.feature` | running Agentry: the policy file, the settings, the log and its upkeep |
| `spec/non-functional/performance.feature` | measured numbers: how long things take |
| `spec/non-functional/limits.feature` | budgets, turn limits, sizes and caps |
| `spec/non-functional/usability.feature` | the page: keys, panels and what the user sees |
| `spec/non-functional/compatibility.feature` | the systems, engines and browsers Agentry runs on and with |
| `spec/non-functional/observability.feature` | what Agentry shows about itself: tokens, turns, events, the status line |
| `spec/proposals/*.feature` | features agreed and not built, one file each, as below in Proposals |

## IDs

Every scenario is tagged with an ID: the prefix of its file, a dash and a number of two or more digits. Each file has its own prefix, fixed here so that stories written side by side never clash:

| File | ID |
| --- | --- |
| `tasks-and-board.feature` | `@TASK-nn` |
| `epics-and-stories.feature` | `@EPIC-nn` |
| `approvals-and-questions.feature` | `@APPROVE-nn` |
| `ideas.feature` | `@IDEA-nn` |
| `schedules.feature` | `@SCHED-nn` |
| `teams-and-templates.feature` | `@TEAM-nn` |
| `policy.feature` | `@POLICY-nn` |
| `sessions.feature` | `@SESSION-nn` |
| `coordinator-and-handoffs.feature` | `@HANDOFF-nn` |
| `agents.feature` | `@TURN-nn` |
| `worktrees-and-branches.feature` | `@TREE-nn` |
| `workers.feature` | `@MACHINE-nn` |
| `shared-files.feature` | `@FILES-nn` |
| `sign-in.feature` | `@SIGNIN-nn` |
| `notifications.feature` | `@NOTIFY-nn` |
| `settings.feature` | `@SET-nn` |
| `claude-code-tools.feature` | `@TOOLS-nn` |
| `demo.feature` | `@DEMO-nn` |
| `reliability.feature` | `@REL-nn` |
| `security.feature` | `@SEC-nn` |
| `operations.feature` | `@OPS-nn` |
| `performance.feature` | `@PERF-nn` |
| `limits.feature` | `@LIM-nn` |
| `usability.feature` | `@USE-nn` |
| `compatibility.feature` | `@COMPAT-nn` |
| `observability.feature` | `@OBS-nn` |

An ID is never renumbered or reused once it is published. The functional IDs were renumbered once, when the files were regrouped from one file per role into one file per feature; the rule holds for every change after it. A new scenario takes the next number in its file. A scenario that is removed leaves a gap; nothing moves up to fill it. A feature file with a name outside this table is refused by the check, except under `spec/proposals`, whose IDs are `@PROP-nn`.

## Role tags

Every functional scenario carries exactly one role tag, straight after its ID: `@user`, `@coordinator`, `@agent`, `@claude-code` or `@worker`, the role whose point of view it takes. A non-functional scenario carries none. A proposal follows the rule of the file it moves to, as below in Proposals.

## The rules for every scenario

Every scenario, with no exceptions:

1. Is tagged with a unique ID, as above.
2. Is tagged with exactly one of `@test`, `@benchmark` and `@manual`:
   - `@test`: an automated test exists for it, named in a comment line directly under the scenario, such as `# internal/domain/policy/default_test.go`.
   - `@benchmark`: a measured number, with where and when it was measured.
   - `@manual`: a person checks it, and the steps they take are the Given, When and Then.
3. Has a Given that states the conditions or the load.
4. Has a number or a yes/no outcome word in every Then, and in every And or But that continues a Then. The outcome words, matched as whole words in any case, are: not, no, never, nothing, cannot, yes, exactly, every, each, once, only, same, empty, refused, denied, allowed, asked, shown, hidden, kept. Any contraction (doesn't, isn't, can't, won't) is one word and counts for nothing: write does not, is not, cannot, will not.
5. Covers what cannot happen too: a refusal, a denial or a cap is a scenario of its own.
6. Is written in plain sentences, in Agentry's own words, as the README and the page use them. No marketing language, no em-dash, and never the words quickly, properly or appropriately.
7. Names where it comes from in a comment line, such as `# README.md, Use it` or `# docs/decisions.md, 2026-10-16`, and for `@test` the test file.
8. Names no host, machine name, address or email address: say the machine, the user.

Scenario Outline and Examples tables are not used; write one scenario per case, so every Then is a plain sentence the checker can read.

## When you change behaviour

Anyone who changes what Agentry does, an agent or a person, keeps this folder true in the same change:

1. A change to behaviour updates or adds its scenarios in `spec/` in the same commit. A behaviour that is removed removes its scenario, and its ID is never reused. A new scenario takes the next number after the highest one in its file, never a gap a removed scenario left. Building a proposal moves its scenarios out of `spec/proposals` and deletes its file in the same commit, as below in Proposals.
2. Every scenario added or changed follows the rules for every scenario above.
3. The summary or report of the change names the scenario IDs it added, changed or removed, such as `added @HANDOFF-43, changed @TASK-12`, or says `no behaviour change`.
4. A reviewer counts a behaviour change with no matching change in `spec/` as a finding.
5. A reviewer reads the diff of `internal/web/testdata/routes.txt` and `internal/mcp/testdata/tools.txt`, the checked tables of every route and tool: a route or tool added or dropped, or one whose owner or access kind changed, is a change to check, such as a route that should need the UI token now open.
6. A tester runs `spec/check` (or `make spec`) and the tests that the touched `@test` scenarios name. The tester walks the touched `@manual` scenarios where it can, and reports each one it could not walk as `not walked`, with its ID.

## Proposals

`spec/proposals` holds the features that are agreed and not built, one `.feature` file each, under any name. A file holds every scenario its feature needs, functional and non-functional together (its limits, its security, its cost), so each proposal is read and agreed as a whole.

A proposal file has a Feature line, then a short paragraph of plain prose: what the feature adds, why, and what Agentry does instead today. Then come its scenarios. Proposals come from the decisions in `docs/decisions.md` and the design notes in `docs/design/`. Where they leave a choice open, the scenario states the recommended option, and the file ends with an `# Open questions` comment block, one question to a line.

Every proposal scenario follows the rules for every scenario, with these tags:

- An ID `@PROP-nn`, numbered from 01 across the whole folder, not per file. A PROP ID is never reused, even after its proposal is built and its file deleted.
- One target tag, the feature file the scenario moves to when it is built: `@to-<stem>`, the name of a file in the ID table without `.feature`, such as `@to-policy` or `@to-security`, or `@to-new-<stem>` for a file that does not exist yet, such as `@to-new-output-gate`, in lower-case words joined by dashes.
- The check tag it is meant to have once built. A `@test` proposal may name a test that does not exist yet.
- The role tag rule of its target: exactly one role tag for a functional file, none for a non-functional one, and one or none for a new file, whose kind is decided when it is made.

When a proposal is built, its scenarios move to their `@to-` files in the same change, each taking the next free ID there and a real check tag, and dropping its `@to-` tag. A `@test` scenario must then cite a test that exists. A `@to-new-` target means creating that file and adding it, with its prefix, to the ID table and to the checker. The proposal file is deleted in the same change, so the folder only ever holds what is agreed and not built.

## Running the check

```sh
spec/check        # from any folder
make spec         # the same, from the repo root
```

`spec/check` runs `go run ./tools/speccheck` from the repo root; it needs Go and nothing else, as the CI image has. An optional argument names another folder to check, relative to the repo root.

It walks every `.feature` file under `spec/` and checks each scenario against rules 1, 2, 4 and 6: the ID tag and its prefix, exactly one check tag, exactly one role tag in a functional file, no ID used twice anywhere in the folder, a number or an outcome word in every Then, and none of the three words. It refuses a Scenario Outline and does not count it. It also fails on an em-dash anywhere under `spec/`, in any file, this one included. For rule 7 it checks the citations of each `@test` scenario, as below; the rest of rule 7, and rules 3, 5 and 8 of the rules for every scenario, are the reviewer's. Under `spec/proposals` it checks the same rules, with a `@PROP-nn` ID in place of the file's prefix, exactly one `@to-` tag naming a file in the ID table or a `new-` stem, and the target's role tag rule, as above in Proposals; it does not look up a proposal's citations. A `@to-` tag on a scenario outside `spec/proposals` fails: a built scenario drops it. Only the `proposals` folder directly under `spec/` holds proposals.

## Citations

The check reads every comment line in a scenario's body, from the `Scenario:` line to the next scenario's tags, and splits it into words, with commas, semicolons, colons, full stops, brackets and quotes cut off their ends, and a line number such as `:42` or `:3-5` cut off the end of a path. A comment line between two scenarios belongs to the one above it.

- A path is a word that starts with one of the repo's top folders and a slash (`cmd/`, `deploy/`, `docs/`, `internal/`, `spec/`, `tools/`, `ui/`), or is the bare name of a root file (`.dockerignore`, `.gitignore`, `.gitlab-ci.yml`, `Dockerfile`, `LICENSE`, `Makefile`, `NOTICE`, `README.md`, `docker-stack.dev.yml`, `go.mod`, `go.sum`). It is read from the repo root, the folder above `spec/`. A word under any other folder that is at the repo root, such as `scripts/build.sh` once there is a `scripts/` folder, fails the check with a note to add that folder to the list, so the list cannot fall behind what the spec cites. A word such as `and/or` passes when there is no `and/` folder, and so do a dot folder such as `.agentry/` and a folder `.gitignore` names on a line of its own, such as `bin/`. A test fails when an entry in either list is not at the repo root. A short name such as `systemd.go` or `organisms/TopBar.tsx` is not a path and is not checked: write the whole path.
- A word with `*`, `?`, `[`, `{` or `<` in it, such as `ui/src/**/*.tsx`, is a glob or a placeholder. It is skipped, not matched.
- A Go test is a word that starts with `Test` and a capital letter, a digit or an underscore. It belongs to the `_test.go` file named right after it, as in `TestA in internal/x/x_test.go`, or else to the last `_test.go` file cited above it in the same scenario, on the same line or an earlier one. So `internal/x/x_test.go TestA and TestB`, `...; the count: TestC in the same file` and a `TestD` on a later comment line all belong to `internal/x/x_test.go`. A path to a file that is not a Go test, such as `internal/x/x.go`, does not change which file a test belongs to.
- A path under `ui/` is checked for the file only; a test named in it is not looked up.

In a `@test` scenario, the check fails on a path that does not exist (`cited path internal/x/gone_test.go does not exist`), on one that is not a path into the repo, with an empty, `.` or `..` part such as `internal/../x` (`cited path internal/../x isn't a path into the repo`), on a Go test that has no `func TestName(` line at the start of a line in its file (`TestA is not defined in internal/x/x_test.go`), and on a Go test with no `_test.go` file before it. Each part's case must match as written, so `internal/x/X_test.go` fails on the Mac as on Linux and is not read for its tests. A word under a top folder in the wrong case, such as `Internal/x`, or one letter off a top folder of four letters or more, such as `intenal/x`, fails as a path that does not exist. A `@manual` or `@benchmark` scenario may cite what it likes. A passing run prints how many paths and Go tests were checked.

Every functional file names the code behind it in a `# Sources:` comment under its description, ending with `and their tests.`, and the check fails on a functional file without one. The check reads it up to the line that ends with a full stop, or to a blank line, one that isn't a comment or a `# ----` rule. Every word in it but `Sources`, `and`, `their`, `its` and `tests` is a path from the repo root, with each part's case as written, so a typo such as `intenal/x` or `Internal/x`, a bare `TaskPage.tsx` or a stray word fails. It fails, in any feature file, on a path that does not exist (`Sources names internal/x, which doesn't exist`), on one that starts with `./`, and on a word that is not a path into the repo: `/`, one that starts with `/`, or one with an empty, `.` or `..` part, such as `internal//x` (`Sources names /, which isn't a path into the repo`).

Every Go package under `internal/domain` and `internal/extensions` is named by the Sources of a feature file outside `spec/proposals`, as its folder: a package nobody specified is either dead or missing its spec. A folder names its own package and not its subpackages, so `internal/extensions` is the framework, not `internal/extensions/ideas`, and `internal/domain/workers` does not name `internal/domain/workers/workerlink`; a file such as `internal/domain/tasks/resume.go` names no package. A test helper, a `<name>test` package beside its `<name>` such as `taskstest`, needs no Sources. The check fails on a package no Sources names (`internal/x is named by no feature file's # Sources: line`) unless it is on the list below, Code with no spec yet.

Line numbers go stale as dev moves, so a new scenario cites a function or a test by name instead. The check fails on `line N` or `lines N`, in any case, or on a path with a line number after it such as `internal/x/x.go:42` or `internal/x/x.go:3-5`, in a comment line of any scenario numbered above the last ID each file had when this rule came in, or, for a functional file, when the files were regrouped by feature: TASK 55, EPIC 19, APPROVE 7, IDEA 17, TEAM 14, POLICY 46, SESSION 5, HANDOFF 42, TURN 19, TREE 12, MACHINE 34, FILES 8, SIGNIN 7, NOTIFY 3, SET 4, TOOLS 54, SCHED 0, REL 54, SEC 68, OPS 42, PERF 16, LIM 35, USE 29, COMPAT 27 and OBS 34, and PROP 0, so the rule holds for every proposal. These numbers are fixed, in `lastIDBeforeLineRule` in `tools/speccheck/rules.go`, and the scenarios at or below them keep their line numbers. A step is not a citation, so a Then such as "line 1 of the report is the verdict" passes.

A passing run prints the scenarios per file, the totals per check tag for the baseline and then, on a line of their own, for the proposals, and exits 0:

```
spec/functional/agents.feature: 19 scenarios
spec/functional/approvals-and-questions.feature: 7 scenarios
...
24 files, 651 scenarios: 485 @test, 16 @benchmark, 150 @manual
proposals: 0 files, 0 scenarios: 0 @test, 0 @benchmark, 0 @manual
cited by @test: 1498 paths, 813 Go tests
```

A failing run prints the same counts, then each problem as `file:line: message`, and exits 1:

```
spec/functional/tasks-and-board.feature:8: Then has no number and no outcome word; the words are not, no, never, ...
spec/README.md:3: em-dash; write a comma, a colon or a full stop
spec/check: 2 problems
exit status 1
```

The last line is `go run` reporting the exit code.

## Code with no spec yet

The packages under `internal/domain` and `internal/extensions` that no feature file's Sources names yet, one per line as a path from the repo root in backquotes. The check reads every line under this heading that starts with a dash and a backquote as an entry, so no other line here starts that way. The rules:

- The list may only shrink. When a feature file's Sources names an entry, the entry comes off in the same change.
- The checker keeps what the list held when this rule came in, `noSpecYet` in `tools/speccheck/specified.go`, and it shrinks with the list.
- The check fails on an entry a Sources names, on an entry that is no such package, and on an entry `noSpecYet` does not hold.
- The check fails on a package `noSpecYet` holds that the list no longer does.

The list is empty: every package under `internal/domain` and `internal/extensions` is named by a feature file's Sources.
