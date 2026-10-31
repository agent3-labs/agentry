Feature: The agents' turns
  The engineer, reviewer, tester, deployer and the other teams' agents, each in its own
  worktree and session: what each one's turn does.
  # Sources: internal/runtime/turns, internal/domain/agents, internal/runtime/coordinator/brief.go,
  # internal/runtime/coordinator/brief_finish.go, internal/runtime/coordinator/brief_notes.go, internal/runtime/coordinator/brief_plan.go,
  # internal/runtime/coordinator/brief_task.go, internal/runtime/coordinator/notes.go,
  # internal/runtime/coordinator/checks.go, internal/domain/policy/hooks/handler.go and their tests.

  # ---- Every agent's turn ----------------------------------------------------

  @TURN-01 @agent @test
  Scenario: The agent's plan for the turn is kept as it last wrote it
    # internal/domain/policy/hooks/handler_test.go TestThePlanFollowsTheFile
    Given a turn whose brief asked for a plan in .agentry/turn-N-plan.md before anything else, as a Markdown checklist
    When the agent writes the file and then ticks steps in it
    Then the plan kept on the turn is the same as the file's last content
    And the plan is kept when the turn ends
    # the plan file name: internal/runtime/machines/local.go PlanFile; the brief's wording: internal/runtime/coordinator/brief_plan.go plan

  @TURN-02 @agent @test
  Scenario: An agent starts fresh with only its brief and the repository
    # internal/runtime/coordinator/loop_test.go TestTheLoopTakesATaskRoundTheCoordinator
    Given the coordinator assigned the engineer with the instructions "Remove every TODO in src/."
    When the engineer's turn starts in a fresh session
    Then its own prompt, "You are the engineer", and those instructions are shown in its brief
    # not under test, checked by hand: the brief also has the task, where the work is, the shared files and the plan file, and not the coordinator's prompt: internal/runtime/coordinator/brief.go

  @TURN-03 @agent @test
  Scenario: An agent's turn starts from its own prompt and the coordinator's brief
    # internal/runtime/coordinator/loop_test.go TestTheLoopTakesATaskRoundTheCoordinator
    Given the coordinator assigned the engineer with the instructions "Remove every TODO in src/."
    When the engineer's turn starts
    Then its own prompt, "You are the engineer", and those instructions are shown in its brief
    # not under test, checked by hand: the brief opens with the prompt, and the prompt is the engineer's own, not the coordinator's: internal/runtime/coordinator/brief.go workerBrief
    # each prompt is embedded from defaults/<name>.md, or defaults/<team folder>/<name>.md for the other teams: internal/domain/agents/defaults.go, teams.go Prompted

  @TURN-04 @agent @test
  Scenario: An agent's turn ends with a report, and every report goes back to the coordinator
    # internal/runtime/coordinator/loop_test.go TestTheLoopTakesATaskRoundTheCoordinator
    Given the engineer's brief, whose "When you finish" section asks for 1 short report for the coordinator
    When the engineer ends its turn with the report "Removed 3 TODOs and committed."
    Then the next turn is the coordinator's, and no other agent's turn starts first
    And the report is shown in the coordinator's brief as the step "engineer → tech-lead: Removed 3 TODOs and committed."
    # not under test, checked by hand: the step is shown once, and the brief tells the agent not to stop to ask questions and to put them in the report: internal/runtime/coordinator/brief_finish.go line 13
    # finish: internal/runtime/coordinator/brief_finish.go

  @TURN-05 @agent @test
  Scenario: An agent works only in the task's worktree, on branch agentry/<task>
    # internal/runtime/coordinator/carried_brief_test.go TestABriefFromASiblingsSessionSaysItIsANewTask
    # the words on repos and folders outside it: internal/runtime/coordinator/ideas_test.go TestAVisionariesBriefIsAsItWasBeforeTheHook (testdata/visionaries-brief.md); PORT: internal/runtime/turns/turns_test.go TestStartRunsTheAgentOnAOneLinePrompt
    Given a task task-3 whose worktree is ready on branch agentry/task-3, with port 7903
    When any agent's turn starts on it
    Then the brief's "Where the work is" names that worktree and branch, and says never to create or change repos or folders outside it
    And the worktree's port is in the PORT environment variable of the turn: yes
    # not under test, checked by hand: the port is task-3's own 7903; the turns test checks PORT=7901 for its own worktree
    # whereText: internal/runtime/coordinator/brief_task.go; PORT: internal/runtime/turns/turns.go environment; the branch: internal/platform/worktrees/worktree_test.go TestCreateSetsTheWorktreeUp

  @TURN-06 @agent @test
  Scenario: An agent writes the plan file its brief asks for, and the plan follows the file
    # internal/domain/policy/hooks/handler_test.go TestThePlanFollowsTheFile
    Given a brief whose "Your plan" section asks for a Markdown checklist in .agentry/turn-N-plan.md before anything else
    When the agent writes 2 unticked steps, then ticks both before its turn ends
    Then the plan kept on the turn is the same as the file's last content, with both steps ticked
    And an unchanged plan is recorded 0 more times
    # PlanFile: internal/runtime/machines/local.go; the section's words: internal/runtime/coordinator/brief_plan.go plan

  @TURN-07 @agent @test
  Scenario: An agent keeps its notes across tasks in .agentry/knowledge.md
    # internal/domain/policy/hooks/handler_test.go TestWhatTheAgentLearnedIsKept
    Given the brief's "What you know from earlier tasks" shows the agent's notes and asks it to update .agentry/knowledge.md before it finishes
    When the engineer's turn ends with "This repo formats with gofmt -s." in that file
    Then the engineer's knowledge is exactly that text, recorded once, by the engineer
    # not under test, checked by hand: its next brief shows the same notes under "What you know from earlier tasks": internal/runtime/coordinator/brief_notes.go knowledge, line 18
    # a section headed "## task-N" or "## epic-N" shows there as its heading and size only, and stays in the file: internal/runtime/coordinator/notes_test.go TestTheBriefCarriesGeneralNotesAndOnlyAnIndexOfTaskNotes
    # KnowledgeFile: internal/runtime/machines/local.go; knowledge: internal/runtime/coordinator/brief_notes.go

  # ---- The engineer, the reviewer and the tester -----------------------------

  @TURN-08 @agent @manual
  Scenario: The engineer changes the code with tests and commits on the branch, and never pushes
    Given the engineer's brief asks for a change
    When its turn runs
    Then its prompt says to write 1 failing test first for a bug and tests for new behaviour, and to run the project's tests, linters and type checks
    And it says to commit in small working steps with conventional messages, and never to push: the deployer does
    And its report says what changed and why, each commit, how it was tested with commands and results, and anything left or risky
    # the prompt: internal/domain/agents/defaults/engineer.md
    # Steps: start a task that needs a small code change, read the engineer's report and its commits with git log on the task branch;
    # check that no push happened (the task's Actions list no git push from the engineer).

  @TURN-09 @agent @manual
  Scenario: The reviewer reads every change and reports what it finds, and changes nothing
    Given the reviewer's brief names the task branch's changes
    When its turn runs
    Then its prompt says it changes nothing: no edits, no commits
    And it reads every change with git log and git diff from the base commit, and labels each finding must fix, should fix or nit, with the file and line
    And its report ends with the must-fix list, then exactly 1 verdict line last
    # the prompt: internal/domain/agents/defaults/reviewer.md
    # Steps: on a task with a committed change, let the reviewer take a turn; check its report's last line is a verdict and
    # that git status in the worktree shows 0 changes from it.

  @TURN-10 @agent @test
  Scenario: The verdict is read from the end of a checker's report
    # internal/runtime/coordinator/checks_test.go TestVerdictOf
    Given a checking agent's report
    When Agentry reads its verdict
    Then "Verdict: approved" and "verdict - approved, with two nits" are approved, and "**Verdict: Changes needed**" and "Verdict: not approved" are changes needed
    And a report such as "I approved of the naming." has no verdict
    # not under test, checked by hand: a verdict above the last 5 lines is not read (internal/runtime/coordinator/checks.go line 40), and the coordinator is told to ask that agent again for one (line 140)
    # verdictOf: internal/runtime/coordinator/checks.go; the message for no verdict: unchecked in the same file
    # verdictOf also reads "Verdict: rejected" as changes needed (checks.go, the rejected prefix); TestVerdictOf has no such case

  @TURN-11 @agent @test
  Scenario: Every checking agent's brief ends with how to give its verdict
    # internal/runtime/coordinator/resumed_brief_test.go TestAResumedReviewerBriefStillEndsWithTheVerdict; fresh: internal/runtime/coordinator/handoff_brief_test.go TestAReReviewsHandoffHasItsEarlierFindingsAndWhatChangedSince
    Given an agent whose work counts as review, such as the reviewer
    When its brief is written, fresh or for a resumed session
    Then the brief ends with exactly the 2 lines it may use, "Verdict: approved" and "Verdict: changes needed", and says to approve only work it would ship as it is
    # not under test, checked by hand: an agent whose work counts as building gets no verdict section
    # VerdictFormat: internal/runtime/coordinator/checks.go; finish: internal/runtime/coordinator/brief_finish.go

  @TURN-12 @agent @manual
  Scenario: The tester runs the tests, adds missing ones and tries the change for real
    Given the tester's brief gives the coordinator's acceptance criteria
    When its turn runs
    Then its prompt says to run every test in the suite, the linters and the build, and record the exact commands and results
    And to exercise the change as a user would, on the port in PORT, leaving no server running when it finishes
    And to fill the gaps with tests committed on the task branch, with 0 changes to the code under test, and never to push
    And its report ends with the failures, each with steps to reproduce, then exactly 1 verdict line last
    # the prompt: internal/domain/agents/defaults/tester.md
    # Steps: on a task with a reviewed change, let the tester take a turn; check its commits touch only tests, and its report's
    # last line is a verdict.

  # ---- The deployer ----------------------------------------------------------

  @TURN-13 @agent @manual
  Scenario: The deployer merges the finished branch into dev and pushes it with one exact command
    Given the deployer's brief says the work was reviewed and tested
    When its turn runs
    Then its prompt says to stop and report when the working tree is not clean or the instructions do not say the work was reviewed and tested
    And to run git fetch origin, then git merge origin/dev into the task branch, and on a conflict git merge --abort and report it, resolving 0 conflicts itself
    And to run the tests and build once more after the merge, and push only when they pass
    And the push is exactly git push origin HEAD:dev, as a command of its own, with nothing before it and nothing chained
    And never to force push, rewrite history, or push to any branch but dev
    # the prompt: internal/domain/agents/defaults/deployer.md
    # Steps: ship a reviewed task; check the deployer's Actions show git fetch origin, git merge origin/dev, the tests, then
    # git push origin HEAD:dev on its own; then move an unreviewed task to the deployer and check its report stops.

  @TURN-14 @agent @test
  Scenario: Agentry counts a turn as pushed only by an approved git push that finished
    # internal/domain/tasks/pushed_test.go TestOnlyAFinishedApprovedPushIsPushed
    Given a turn's actions
    When Agentry asks whether the turn pushed the work
    Then yes only for an action whose command starts with git push, approved, with a result
    And a denied push, a push with no result yet, and git status each count as no
    # not under test, checked by hand: a push wrapped in timeout or chained after cd is not seen, which is why the prompt gives the exact command: internal/domain/tasks/task.go line 397
    # Pushed: internal/domain/tasks/task.go; a done task shows Shipped only then: internal/domain/tasks/summary_test.go TestOnlyATaskWithAFinishedApprovedPushIsPushed

  @TURN-15 @agent @test
  Scenario: A ship the user chose needs no earlier review, and counts only once the push went through
    # internal/runtime/coordinator/next_test.go TestYourShipTakesTheWorkAsItIs
    Given the user moved the task to Shipping and the coordinator handed it to the deployer
    When the deployer's turn starts
    Then its brief adds "The user chose to ship this as it is, so no earlier review or test is needed", and that failing checks after the merge mean no push
    And once an approved push finished in that turn, done is allowed with 0 reviews
    And after a push that was denied, hung, or stopped by failed tests, done is refused with "the work isn't checked yet", and a change after the ship needs checking again
    # YouChoseToShip: internal/runtime/coordinator/brief.go; internal/runtime/coordinator/next_test.go TestYourShipTellsTheShipperToShipItAsItIs

  # ---- The other teams -------------------------------------------------------

  @TURN-16 @agent @manual
  Scenario: The Writing team researches, drafts, fact-checks and edits a piece
    Given a task of the Writing team
    When its agents take their turns
    Then the editor writes brief.md and does not write the piece itself
    And the researcher records every fact with its source in research/notes.md, invents 0 sources, and does not write the piece
    And the writer drafts draft.md with every fact sourced or marked [NEEDS SOURCE: what], and commits it
    And the fact-checker writes fact-check.md with a verdict per claim, does not rewrite the draft, and ends with exactly 1 verdict line
    # the prompts: internal/domain/agents/defaults/writing/editor.md, researcher.md, writer.md, fact-checker.md
    # Steps: give the Writing team a short article; read brief.md, research/notes.md, draft.md and fact-check.md on the branch,
    # and check the fact-checker's report ends with a verdict.

  @TURN-17 @agent @manual
  Scenario: The Research team frames a question, gathers evidence, analyses it reproducibly and challenges it
    Given a task of the Research team
    When its agents take their turns
    Then the research-lead writes question.md and does not do the research itself
    And the investigator records sources in sources.md and data in data/ as CSV with a README, and never changes raw data after recording it
    And the analyst writes scripts in analysis/ that anyone can rerun, with uncertainty on every estimate, and SVG charts in analysis/charts/
    And the critic reruns the analysis, writes review.md, changes nothing else, and ends with exactly 1 verdict line
    # the prompts: internal/domain/agents/defaults/research/research-lead.md, investigator.md, analyst.md, critic.md
    # Steps: give the Research team a question answerable from a CSV in the repo; read question.md, sources.md, analysis/ and
    # review.md on the branch, rerun the scripts, and check the critic's report ends with a verdict.

  @TURN-18 @agent @manual
  Scenario: The Design team frames the problem, draws every state, writes the words and critiques it
    Given a task of the Design team
    When its agents take their turns
    Then the product-lead writes spec.md with the problem in 1 sentence and a measure of success, and does not design it itself
    And the product-designer writes flows.md and SVG wireframes in designs/ for every state, with text contrast at least 4.5:1 and targets at least 44 by 44 pixels
    And the ux-writer writes the copy deck copy.md, naming every button with a verb and an object
    And the design-critic reviews against the spec and WCAG 2.2 AA, writes critique.md with a severity per finding, changes nothing else, and ends with exactly 1 verdict line
    # the prompts: internal/domain/agents/defaults/design/product-lead.md, product-designer.md, ux-writer.md, design-critic.md
    # Steps: give the Design team a small screen to design; read spec.md, flows.md, designs/, copy.md and critique.md on the
    # branch, and check the critic's report ends with a verdict.

  @TURN-19 @agent @manual
  Scenario: Networking touches nothing live without the user's go-ahead
    Given a task of the Networking team
    When its agents take their turns
    Then the lead's prompt says nothing live changes without the user's explicit go-ahead in this task, which the lead asks for only once the change has the exact commands, the effect, a check, a tested rollback and a way back in
    And the engineer changes config in the repo and never a live machine's firewall, routes, VPN, DNS records or proxy
    And the verifier and the security reviewer never change live state and cannot write files, so their evidence is their report, ending with exactly 1 verdict line
    And the lead's prompt says the shipped policy allows everything, so these rules are the only guard until the user turns that line off
    # the prompts: internal/domain/agents/defaults/networking/network-lead.md, network-engineer.md, network-verifier.md, network-security.md
    # Steps: give the Networking team a config change in a repo; check the engineer's commits touch only the repo, that its report
    # proposes the live change with commands, check and rollback, and that the lead asks the user before any of it runs.
