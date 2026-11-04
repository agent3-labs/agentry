Feature: The demo build
  Agentry's own pages as static files for a website, on a pretend server in the browser, so a visitor can look around with nothing running.
  # Sources: ui/vite.demo.config.ts, ui/src/demo, ui/src/lib/host.ts and their tests.

  @DEMO-01 @user @manual
  Scenario: The demo build has its own folder, and the normal build holds none of the demo
    # ui/vite.demo.config.ts, copyPublicDir and noInstall; ui/package.json, build:demo
    Given the UI's packages are installed
    When the user runs npm run build:demo in ui
    Then the files are written to ui/dist-demo only, and nothing is written to internal/web/static/dist
    And its index.html has no inline script and no link to the manifest, and every path in it starts with ./
    And it holds no sw.js, no manifest and no install icon: of the icons, only icons/icon.svg
    When the user runs npm run build:demo -- --outDir with another folder
    Then that folder holds no sw.js, no manifest and no install icon either
    When the user runs npm run build in ui
    Then not one file in internal/web/static/dist holds the banner's words Demo: nothing runs

  @DEMO-02 @user @test
  Scenario: Every read a page makes is answered by the pretend server
    # ui/src/demo/server.test.ts, the demo's pretend server
    Given the demo open in a browser
    When a page reads an API path that the page's source names
    Then every read is answered with 200 and the log position, with nothing sent over the network
    And each task's page and each page of its events is answered, the events oldest first
    And the long poll is answered once at once, then waits until the log moves or the page stops waiting
    And a path the demo does not have is refused with 404, and the refusal names the path

  @DEMO-03 @user @test
  Scenario: The demo refuses every change but a new task, an answer and a yes or no
    # ui/src/demo/server.test.ts, refuses any other change
    Given the demo open in a browser
    When the page sends any other change, such as a message to the coordinator, a card moved, an agent, the policy, the settings, a team, a machine, an idea or a redaction
    Then it is refused with 403 and the words This is a demo: nothing runs. Download Agentry to use it.
    And nothing in the demo changes

  @DEMO-04 @user @test
  Scenario: The demo keeps nothing in the browser, and a reload starts fresh
    # ui/src/demo/server.test.ts, keeps the page's settings in memory and starts fresh on each load
    Given the demo open in a browser
    When the user changes a setting of the page's own, such as the theme
    Then it is kept in memory only: nothing is written to localStorage or sessionStorage
    When the user reloads the page
    Then the demo starts again from the same seed

  @DEMO-05 @user @manual
  Scenario: The demo makes no request to a server, from any folder of a site
    # ui/src/demo/host.tsx; checked in headless Chrome over its network log
    Given the files of ui/dist-demo served by a plain static server from a sub-folder, such as /agentry/try/
    When the user opens every page and every tab of a task's page, and presses Ctrl K and Ctrl J
    Then no request goes to the API, no websocket opens, no service worker is registered and nothing is written to the browser's storage
    And every file the page loads comes from that folder: not one comes from the root path
    And every page shows the banner Demo: nothing runs. Download Agentry to use it., its link to the latest release
    And the banner's link opens in a new tab, so the demo's own tab or frame is kept

  @DEMO-06 @user @test
  Scenario: A terminal in the demo shows read-only lines
    # ui/src/demo/TerminalView.test.tsx
    Given the demo open on a task's page
    When the user opens its Terminal tab
    Then a few lines say nothing runs in the demo, and no connection is made
    And no one has control, and nothing the user types is sent anywhere

  @DEMO-07 @user @manual
  Scenario: The demo loads no xterm and no Monaco on any screen
    # ui/vite.demo.config.ts, the stand-ins' aliases; ui/src/demo/standins.test.tsx; checked in headless Chrome over its network log
    Given the files of ui/dist-demo served by a plain static server from a sub-folder
    When the user opens every page, a task's Terminal and Files tabs, an agent's page and the Policy page
    Then not one xterm, Monaco, editor worker or codicon file is loaded
    And terminals, files and code are shown as plain text, read only
    And a changed file is shown beside its last committed version, each labelled

  @DEMO-08 @user @manual
  Scenario: A link to a screen in the address's hash opens that screen
    # ui/src/demo/host.tsx, createRouter; ui/src/demo/server.test.ts keeps task-11 and its git push; checked in headless Chrome
    Given the files of ui/dist-demo served by a plain static server from a sub-folder
    When the user opens #/tasks?view=list, #/tasks?view=board, #/tasks/task-12, #/agents and #/tasks/task-11 on a fresh load
    Then each opens its own screen: the list, the board, task-12's page, the agents and task-11's page
    And task-11's page shows the pending approval for git push origin dev, with Approve and Deny shown
    When the same links are set as the hash of a page already open
    Then each screen is shown with no reload, the same as on a fresh load

  @DEMO-09 @user @manual
  Scenario: Inside an iframe the demo keeps to its frame
    # ui/src/demo/host.tsx; checked in headless Chrome with the demo in an iframe of another page
    Given the demo in an iframe of a page of the same site, served from a sub-folder
    When the user opens every page, follows the links and presses Ctrl K and Ctrl J
    Then the demo's page does not read or change window.top or window.parent, and the outer page's address is kept
    And not one request or link opens the root path of the site

  @DEMO-10 @user @manual
  Scenario: A demo build that would hold xterm, Monaco or the QR encoder is refused
    # ui/vite.demo.config.ts, noHeavyParts; checked by breaking a stand-in's alias, such as QRCode to QRCodeX, and running the build
    Given a demo build in which an import of xterm, Monaco or the QR encoder matches no stand-in's alias
    When the user runs npm run build:demo in ui
    Then the build fails with an error that names the file it refused
    And nothing is written to the output folder, so nothing is shipped

  @DEMO-11 @user @test
  Scenario: A new task runs from the coordinator to the deployer's push while the visitor watches
    # ui/src/demo/sim.ts; ui/src/demo/sim.test.ts, a new task runs to the deployer's push
    Given the demo open in a browser
    When the visitor makes a new task with a title and a description
    Then it is shown on the board as the next task, with the visitor's title, kept in memory only
    And every few seconds one beat is shown: the coordinator hands it to the engineer, the reviewer, the tester and the deployer, each turn running until it reports
    And within about a minute the deployer waits for the visitor to approve git push origin dev, shown in Questions
    And every tab of the task, the board, Questions and search show the same step at each beat

  @DEMO-12 @user @test
  Scenario: Approving the push ships the task
    # ui/src/demo/sim.ts; ui/src/demo/sim.test.ts, approve ships it
    Given a task of the demo whose deployer waits for the visitor to approve git push origin dev, such as task-11
    When the visitor approves it
    Then the approval leaves Questions at once
    And the deployer reports the push, and the task is shown as done and shipped
    And no timer is kept once the task is done

  @DEMO-13 @user @test
  Scenario: Denying the push closes the task with its branch left unpushed
    # ui/src/demo/sim.ts, unpushed; ui/src/demo/sim.test.ts, denying task-11's push
    Given a task of the demo whose deployer waits for the visitor to approve git push origin dev, such as task-11
    When the visitor denies it
    Then the approval leaves Questions at once, and the command is shown as denied
    And the deployer reports that nothing was pushed, and the coordinator closes the task as done, not shipped, saying its branch is left unpushed

  @DEMO-14 @user @test
  Scenario: Answering task-10's question sends it on with the visitor's choice
    # ui/src/demo/sim.ts, answer; ui/src/demo/sim.test.ts, an answer to task-10
    Given the demo open in a browser, with task-10's question open in Questions
    When the visitor picks an option or writes an answer
    Then the question leaves Questions at once
    And the coordinator hands the task to the engineer with the visitor's choice, then to the reviewer, the tester and the deployer, who is asked about the push the same as for a new task

  @DEMO-15 @user @test
  Scenario: A second yes or no, or one to what does not exist, is refused as the real server refuses it
    # ui/src/demo/server.ts, decide; ui/src/demo/sim.test.ts, approving or denying twice
    Given the demo open in a browser
    When the page approves or denies a command already decided, or one the task does not have
    Then it is refused with 409 and the real server's words, such as action toolu_005 was already approved
    And a task the demo does not have is refused with 404, and nothing in the demo changes

  @DEMO-16 @user @test
  Scenario: A visit makes at most 5 new tasks
    # ui/src/demo/server.ts, MOST_NEW; ui/src/demo/sim.test.ts, makes at most 5 new tasks
    Given a visitor who has made 5 new tasks in the demo
    When the visitor makes a 6th
    Then it is refused with 403, and the form shows that the demo makes at most 5 new tasks a visit
    And no task is added

  @DEMO-17 @user @test
  Scenario: The demo refuses an epic or a story of one
    # ui/src/demo/server.ts, NO_EPICS; ui/src/demo/sim.test.ts, refuses an epic
    Given the demo open in a browser
    When the visitor makes a new task as an epic, or as a story of an epic
    Then it is refused with 403, and the form shows that the demo runs single tasks only
    And no task is added

  @DEMO-18 @user @test
  Scenario: Seeded tasks stand still until the visitor acts
    # ui/src/demo/sim.ts; ui/src/demo/sim.test.ts, moves nothing before the visitor acts
    Given the demo freshly loaded
    When the visitor waits and does nothing
    Then not one task moves and no timer runs
    And task-11 shows its pending approval for git push origin dev, and task-12 is shown the same as on every load
    And a reload starts fresh, with every task as seeded

  @DEMO-19 @user @test
  Scenario: An answer to a task that is not waiting for one is refused as the real server refuses it
    # ui/src/demo/server.ts, answer; ui/src/demo/sim.test.ts, an answer to a task not waiting for one
    Given the demo open in a browser
    When the page answers a task that is not waiting for an answer, such as task-10 answered once already or task-12
    Then it is refused with 409 and the words the task isn't waiting for an answer
    And a task the demo does not have is refused with 404, and nothing in the demo changes
