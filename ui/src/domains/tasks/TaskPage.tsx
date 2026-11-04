import { FocusEventHandler, Fragment, ReactNode, useState } from "react";
import { Link, useParams, useSearchParams } from "react-router-dom";
import { CircleCheck, EyeOff, FileQuestion, FolderGit2, SquareTerminal } from "lucide-react";
import { Button } from "@/components/atoms/Button";
import { Input } from "@/components/atoms/Input";
import { useToast } from "@/shell/Toasts";
import { api } from "@/api/client";
import { useBreadcrumb } from "@/lib/breadcrumb";
import { Badge } from "@/components/atoms/Badge";
import { PageHeader } from "@/components/molecules/PageHeader";
import { Panel } from "@/components/molecules/Panel";
import { Clamp } from "@/components/molecules/Clamp";
import { Tabs } from "@/components/molecules/Tabs";
import { HandoffTimeline } from "@/domains/tasks/HandoffTimeline";
import { ResourceTable, Column } from "@/components/molecules/ResourceTable";
import { ActionBadge, TaskStatusBadge } from "@/components/molecules/StatusBadges";
import { KeyValueList } from "@/components/molecules/KeyValueList";
import { EmptyState } from "@/components/molecules/EmptyState";
import { Banner } from "@/components/molecules/Banner";
import { Mono } from "@/components/molecules/Mono";
import { AgentName } from "@/components/molecules/AgentName";
import { TokenMeter } from "@/components/molecules/TokenMeter";
import { ActivityFeed, Now } from "@/domains/tasks/ActivityFeed";
import { NeedsYouPanel } from "@/components/organisms/NeedsYouPanel";
import { LimitEditor, turnsLine } from "@/components/organisms/LimitEditor";
import { OpenEpic } from "@/domains/tasks/OpenEpic";
import { LabelEditor } from "@/domains/tasks/LabelEditor";
import { LabelList } from "@/components/molecules/LabelChip";
import { TerminalView } from "@/components/organisms/TerminalView";
import { PlanRail } from "@/domains/tasks/PlanRail";
import { CoordinatorChat } from "@/domains/tasks/CoordinatorChat";
import { HandoffPath } from "@/components/molecules/HandoffPath";
import { terminalURL } from "@/api/client";
import { StoryTable } from "@/domains/tasks/StoryTable";
import { EpicPicker } from "@/domains/tasks/EpicPicker";
// eslint-disable-next-line no-restricted-imports -- the one allowed import between domains: a task's page shows its shared files
import { SharedFiles } from "@/domains/sharedfiles/SharedFiles";
import { EraseTask } from "@/domains/tasks/EraseTask";
import { ErasedTask } from "@/domains/tasks/ErasedTask";
import { RedactText } from "@/components/organisms/RedactText";
import { EpicBadge, StoryProgress } from "@/components/molecules/StoryProgress";
import { waitingLine } from "@/lib/epics";
import { shownId } from "@/lib/shown-id";
import type { Action, Agent, Erasure, LogEvent, TaskDetail, TokenSplit, Turn } from "@/api/types";
import { teamOf, teamsOf } from "@/lib/teams";
import { SHOWS, Show, activityOf, rowFor } from "@/domains/tasks/activity";
import { LOCAL, machineName } from "@/lib/machines";
import { tokenParts, tokenSplit, tokens } from "@/lib/tokens";
import { clockTime, dateTime, duration, timeAgo } from "@/lib/time";
import { cn } from "@/lib/cn";
import { keepFocusInView } from "@/lib/focus-in-view";
import { useMarkedText } from "@/lib/marked-text";
import { useResource } from "@/live/use-resource";
import { useEvents } from "@/live/use-events";
import { LoadEarlier } from "@/components/molecules/LoadEarlier";
import { useNow } from "@/live/use-now";
import { useTaskCommits, useTaskLinks } from "@/live/refs";
import { COLUMN_TITLES, stageOf } from "@/lib/columns";
import { EXTENSIONS, extensionLink } from "@/extensions";
import { pageURL } from "@/lib/host";

// A task in full. The header and a strip of facts stay put. Under them, on
// the left, the agents' plans stay on every tab; on the right, the path of
// handoffs, then the tabs share the rest of the screen: your chat with the
// coordinator, where the task opens, everything that happened, the live
// terminal, the handoffs, and the raw actions, events and details, every
// list but the chat newest first. A handoff opens the activity of its
// turn; the URL keeps the tab, the turn and the rows shown, so any view can
// be linked to and Back returns from it. On a phone the page scrolls
// instead, and the tab keeps most of the screen.

const ACTION_COLUMNS: Column<Action>[] = [
  { header: "Time", className: "w-20", render: (a) => <Mono className="text-ink-muted">{clockTime(a.requestedAt)}</Mono> },
  { header: "Turn", className: "w-20", render: (a) => <Mono className="text-ink-muted">{a.turn}</Mono> },
  { header: "Agent", className: "w-28", render: (a) => <AgentName name={a.agent} /> },
  { header: "Tool", className: "w-24", render: (a) => <Mono className="text-ink-muted">{a.tool}</Mono> },
  {
    header: "Command",
    className: "w-full max-w-0",
    render: (a) => (
      <p className="line-clamp-3 font-mono text-[12px] break-all whitespace-pre-wrap text-ink-primary" title={a.command}>
        {a.command}
      </p>
    ),
  },
  {
    header: "Decision",
    className: "w-56",
    render: (a) => (
      <div className="flex flex-col items-start gap-0.5 py-0.5">
        <ActionBadge action={a} />
        {a.reason && <span className="line-clamp-2 text-[11px] text-ink-muted">{a.reason}</span>}
      </div>
    ),
  },
];

const EVENT_COLUMNS: Column<LogEvent>[] = [
  { header: "#", className: "w-14 text-right", render: (e) => <Mono className="text-ink-muted">{e.position}</Mono> },
  { header: "Time", className: "w-20", render: (e) => <Mono className="text-ink-muted">{clockTime(e.at)}</Mono> },
  { header: "Type", className: "w-44", render: (e) => <Mono className="text-info">{e.type}</Mono> },
  { header: "Actor", className: "w-36", render: (e) => <Mono>{e.actor}</Mono> },
  {
    header: "Data",
    className: "w-full max-w-0",
    render: (e) => {
      const data = JSON.stringify(e.data ?? {});
      return (
        <p className="line-clamp-2 font-mono text-[11px] break-all text-ink-muted" title={data}>
          {data}
        </p>
      );
    },
  },
];

// A turn's split, one part per line under its total, so the Tokens
// column stays narrow.
function TurnSplit({ split }: { split: TokenSplit }) {
  return (
    <dl className="grid grid-cols-[auto_auto] gap-x-2 text-[11px] text-ink-muted">
      {tokenParts(split).map(([label, value]) => (
        <Fragment key={label}>
          <dt className="text-left whitespace-nowrap">{label}</dt>
          <dd className="font-mono">{value}</dd>
        </Fragment>
      ))}
    </dl>
  );
}

// How a turn started, under its tokens: it resumed a session, or forked one
// another turn was busy in (its id on hover), from another story of the epic
// when it was; or it started fresh and why. Nothing for a turn whose log
// didn't say. It takes no width of its own and wraps within what the split
// sets, so a long reason never widens the table.
function TurnStart({ turn }: { turn: Turn }) {
  const style = "self-stretch text-right text-[11px] whitespace-normal break-words text-ink-muted [contain:inline-size]";
  if (turn.fromSession) {
    return (
      <span className={style} title={turn.fromSession}>
        {turn.forked ? "forked" : "resumed"}
        {turn.sessionFrom && (
          <>
            {" from "}
            <TaskLink id={turn.sessionFrom} />
          </>
        )}
      </span>
    );
  }
  return turn.freshBecause ? <span className={style}>fresh: {turn.freshBecause}</span> : null;
}

const TURN_COLUMNS: Column<Turn>[] = [
  { header: "Turn", className: "w-20", render: (t) => <Mono className="text-ink-muted">{t.id}</Mono> },
  { header: "Agent", className: "w-32", render: (t) => <AgentName name={t.agent} /> },
  { header: "Machine", className: "w-28", render: (t) => <Mono className="text-ink-muted">{machineName(t.worker || LOCAL)}</Mono> },
  { header: "Started", className: "w-24", render: (t) => <Mono className="text-ink-muted">{clockTime(t.startedAt)}</Mono> },
  { header: "Took", className: "w-24", render: (t) => <Mono>{t.ended ? duration(t.startedAt, t.lastEventAt) : "running"}</Mono> },
  // A short reason why tokens are unknown keeps its own width on one line;
  // a long one wraps past about 23 characters, about the stacked split's width.
  {
    header: "Tokens",
    className: "w-20 text-right",
    render: (t) => (
      <span className="flex flex-col items-end">
        {t.tokensUnknown ? (
          <Mono className="ml-auto block w-max max-w-[23ch] text-[11px] whitespace-normal text-warning">{t.tokensUnknown}</Mono>
        ) : (
          <>
            <Mono>{t.tokens > 0 ? tokens(t.tokens) : ""}</Mono>
            {t.split && <TurnSplit split={t.split} />}
          </>
        )}
        <TurnStart turn={t} />
      </span>
    ),
  },
  {
    header: "Outcome",
    className: "w-full max-w-0",
    render: (t) =>
      t.issue ? (
        <span className="flex min-w-0 items-center gap-2">
          <Badge variant="danger">issue</Badge>
          <span className="truncate text-[12px] text-danger" title={t.issue}>
            {t.issue}
          </span>
        </span>
      ) : t.ended ? (
        <Badge>ended</Badge>
      ) : (
        <Badge variant="info">running</Badge>
      ),
  },
];

// Most used first: talk to the coordinator, watch the work, then the records.
const TABS = ["stories", "coordinator", "activity", "terminal", "files", "handoffs", "actions", "events", "details"] as const;
type TabId = (typeof TABS)[number];

export function TaskPage() {
  const { taskId = "" } = useParams();
  const [params, setParams] = useSearchParams();
  const { data: task, error, status, position, reload } = useResource<TaskDetail>(`/api/tasks/${encodeURIComponent(taskId)}`);
  useBreadcrumb({ label: "Tasks", to: "/tasks" }, { label: shownId(taskId, task?.kind) });
  const clock = useNow(Boolean(task && task.status !== "done"));
  const roster = useResource<Agent[]>("/api/agents").data ?? [];
  const links = useTaskLinks(taskId, useTaskCommits(taskId));
  // The Activity and Events tabs share the task's events, newest first from
  // where the detail was read, and the rest of the page never waits on them.
  const showsLog = ["activity", "events"].includes(params.get("tab") ?? "");
  const log = useEvents(task && showsLog ? { task: taskId, newestBefore: position + 1 } : null);
  // The text to redact, from what was marked on the page, while you are asked.
  const [redacting, setRedacting] = useState<string | null>(null);
  const markedText = useMarkedText();
  const [erasure, setErasure] = useState<Erasure | null>(null);

  // An erased task is gone for good: erased here, the page keeps what the
  // erasure said; read again, the server says when (410). The route keeps
  // this page when only the id changes, so the erasure holds for its own task.
  if (erasure?.id === taskId) return <ErasedTask id={shownId(taskId, task?.kind)} erasure={erasure} />;
  if (status === 410) return <ErasedTask id={taskId} said={error} />;
  if (error && !task) {
    return (
      <EmptyState icon={<FileQuestion />} title={`No task ${taskId}`}>
        {error}
      </EmptyState>
    );
  }
  if (!task) return null;

  const asked = params.get("tab") as TabId | null;
  const isEpic = task.kind === "epic";
  // An archived task is read back from its archive and takes no events, so
  // nothing on the page offers to change it.
  const archived = Boolean(task.archived);
  const tab: TabId = asked && TABS.includes(asked) && (isEpic || asked !== "stories") ? asked : isEpic ? "stories" : "coordinator";
  const turn = params.get("turn") ?? "";
  const row = params.get("row") ?? "";
  const askedShow = params.get("show") as Show | null;
  const show: Show = askedShow && SHOWS.includes(askedShow) ? askedShow : "all";
  const go = (next: { tab: TabId; turn?: string; show?: Show; row?: string }, push = false) => {
    const query: Record<string, string> = { tab: next.tab };
    if (next.turn) query.turn = next.turn;
    if (next.row) query.row = next.row;
    if (next.show && next.show !== "all") query.show = next.show;
    setParams(query, { replace: !push });
  };
  const openTurn = (id: string) => go({ tab: "activity", turn: id }, true);
  // An action or event opens its turn's activity with its own row picked
  // out, and every kind of row shown, so the row is there to see. An
  // action's row is found once the activity has read its events.
  const openRow = (target: { action?: string; event?: LogEvent }, turn = "") =>
    go({ tab: "activity", turn, row: target.action ? `action:${target.action}` : rowFor(items, target) }, true);
  const watch = () => go({ tab: "terminal" });

  const working = task.status !== "done" && task.currentAgent ? task.currentAgent : "";
  const actions = task.actions ?? [];
  const turns = task.turnList ?? [];
  const approvals = task.approvals?.length ?? 0;
  const items = activityOf(task, log.events);
  const picked = row.startsWith("action:") ? rowFor(items, { action: row.slice("action:".length) }) : row;
  const logFoot = (
    <>
      {log.error && <p className="px-6 py-3 text-center text-[12px] text-warning">Couldn't read the events: {log.error}</p>}
      <LoadEarlier earlier={log.earlier} loadingEarlier={log.loadingEarlier} onLoad={log.loadEarlier} />
    </>
  );
  const now: Now | null =
    task.status === "needs you"
      ? { label: "Waiting for you", tone: "warning" }
      : working && task.status === "working"
        ? { label: `${working} is working`, tone: "success" }
        : task.waitingOn
          ? { label: `Waiting on ${task.waitingOn}, from another team`, tone: "success" }
          : waitingLine(task)
            ? { label: waitingLine(task), tone: "success" }
            : null;
  const w = task.worktree;
  const coordinatorAgent = roster.find((a) => a.name === task.coordinator);
  const team = coordinatorAgent && teamsOf(roster).length > 1 ? teamOf(coordinatorAgent) : "";
  const turnOfEvent = (e: LogEvent) =>
    e.correlation && turns.some((t) => t.id === e.correlation) ? e.correlation : actions.find((a) => a.id === e.correlation)?.turn;

  const openEpic = Boolean(task.closing) && !task.hasQuestion && task.status === "working" && !working;
  const top = error || (task.status === "needs you" && !task.hasQuestion) || approvals > 0 || task.status === "done" || openEpic;
  return (
    <div className="flex flex-col md:h-full md:min-h-0">
      <PageHeader
        title={task.title}
        badges={
          <>
            {isEpic && <EpicBadge />}
            <TaskStatusBadge status={task.status} />
            {stageOf(task) && <Badge>{stageOf(task)}</Badge>}
            {archived && <Badge>Archived {task.archivedAt && dateTime(task.archivedAt)}</Badge>}
          </>
        }
        actions={[
          ...(w
            ? [
                {
                  label: "Workspace",
                  icon: <FolderGit2 />,
                  onClick: () => window.open(pageURL(`/tasks/${task.id}/workspace`), "_blank", "noopener"),
                },
              ]
            : []),
          ...(working ? [{ label: "Terminal", icon: <SquareTerminal />, onClick: watch }] : []),
          { label: "Redact a text", icon: <EyeOff />, onClick: () => setRedacting(markedText()) },
        ]}
        meta={
          <>
            <Mono>{shownId(task.id, task.kind)}</Mono>
            {team && (
              <>
                <span>·</span>
                <span>{team} team</span>
              </>
            )}
            {task.epic && (
              <>
                <span>·</span>
                <span>
                  story of <TaskLink id={task.epic} kind="epic" />
                  {task.epicTitle && <span className="text-ink-muted"> {task.epicTitle}</span>}
                </span>
              </>
            )}
            {task.madeFrom && (
              <>
                <span>·</span>
                <span>
                  made from <MadeFromLink stream={task.madeFrom} />
                </span>
              </>
            )}
            {task.parent && (
              <>
                <span>·</span>
                <span>
                  asked for by <TaskLink id={task.parent} />
                </span>
              </>
            )}
            {task.waitingOn && (
              <>
                <span>·</span>
                <span>
                  waiting on <TaskLink id={task.waitingOn} />
                </span>
              </>
            )}
            <span>·</span>
            <Mono>{task.repo}</Mono>
            {w ? (
              <>
                <span>·</span>
                <Mono>{w.branch}</Mono>
              </>
            ) : (
              task.status !== "done" && <span>· setting up the worktree</span>
            )}
            {task.firstTitle && <span title={task.firstTitle}>· renamed by the coordinator from "{task.firstTitle.slice(0, 60)}"</span>}
          </>
        }
      />

      <div className="flex shrink-0 flex-wrap items-stretch border-b border-divider">
        {isEpic && (
          <Fact label="Stories" className="w-40">
            <StoryProgress done={task.storiesDone} total={task.stories} />
          </Fact>
        )}
        <Fact label="Agent">
          {working ? <AgentName name={working} className="text-[12px]" /> : <Mono className="text-ink-muted">none</Mono>}
        </Fact>
        <Fact label="Tokens" className="min-w-56">
          <LimitEditor task={task} kind="budget" compact onChanged={reload}>
            <span className="flex flex-col gap-1">
              <TokenMeter used={task.tokensUsed} budget={task.budget} />
              {task.tokensSplit && (
                <Mono className="text-[11px] leading-4 text-ink-muted">
                  {tokenSplit(task.tokensSplit)}
                  {task.tokensUnsplit ? ` · ${tokens(task.tokensUnsplit)} not split` : ""}
                </Mono>
              )}
              {task.tokensUnknown && <span className="text-[11px] leading-4 text-warning">{task.tokensUnknown}</span>}
            </span>
          </LimitEditor>
        </Fact>
        <Fact label={task.maxTurns > 0 ? "Turns since your answer" : "Turns"}>
          <LimitEditor task={task} kind="turns" compact onChanged={reload}>
            <Mono className={cn("whitespace-nowrap", task.atTurnLimit && "text-danger")}>{turnsLine(task)}</Mono>
          </LimitEditor>
        </Fact>
        <Fact label="Actions">
          <Mono>{actions.length}</Mono>
          {approvals > 0 && <span className="ml-1.5 text-[12px] text-warning">{approvals} waiting</span>}
        </Fact>
        <Fact label="Machine">
          <Mono>{w ? machineName(w.worker) : "none yet"}</Mono>
        </Fact>
        <Fact label="Last active">
          <span className="text-[12px]">{timeAgo(task.lastActiveAt, clock)}</span>
        </Fact>
        <div className="ml-auto flex items-center px-6 py-2">
          {archived ? (
            <LabelList labels={task.labels} />
          ) : (
            <LabelEditor key={task.labels.join(",")} taskId={task.id} labels={task.labels} />
          )}
        </div>
      </div>

      {top && (
        <div className="flex max-h-[45%] shrink-0 scrollbar-thin flex-col gap-4 overflow-y-auto border-b border-divider px-6 py-4">
          {error && (
            <Banner tone="warning" title="Showing the last copy: couldn't refresh">
              {error}
            </Banner>
          )}
          <NeedsYouPanel task={task} onChanged={reload} onOpenTerminal={watch} />
          {openEpic && <OpenEpic task={task} onChanged={reload} />}
          {task.status === "done" && (
            <div className="flex gap-3 rounded-lg border border-success/40 px-4 py-3">
              <span className="flex shrink-0 items-center gap-1.5 self-start text-[13px] font-semibold text-success">
                <CircleCheck className="size-4" />
                {task.pushed ? "Shipped" : "Done"}
                {task.doneByYou && <span className="font-normal text-ink-muted">· Moved to Done by you</span>}
              </span>
              <Clamp text={task.summary} lines={2} className="min-w-0 flex-1" />
            </div>
          )}
        </div>
      )}

      <div className="flex flex-col md:min-h-0 md:flex-1 lg:flex-row">
        <aside
          aria-label="Plan"
          className="max-h-64 shrink-0 scrollbar-thin overflow-y-auto border-b border-divider lg:max-h-none lg:w-80 lg:border-r lg:border-b-0"
        >
          <PlanRail turns={turns} coordinator={task.coordinator} onOpenTurn={openTurn} />
        </aside>
        <div className="flex min-w-0 flex-1 flex-col md:min-h-0">
          <HandoffPath turns={turns} coordinator={task.coordinator} done={task.status === "done"} onOpenTurn={openTurn} />
          <div className="shrink-0 pt-1">
            <Tabs
              active={tab}
              onChange={(id) => go({ tab: id as TabId })}
              tabs={[
                ...(isEpic ? [{ id: "stories", label: "Stories", count: task.stories }] : []),
                {
                  id: "coordinator",
                  label: task.hasQuestion ? "Coordinator · asks you" : "Coordinator",
                  live: turns.some((t) => !t.ended && t.agent === task.coordinator),
                },
                { id: "activity", label: "Activity", live: Boolean(now) },
                { id: "terminal", label: "Terminal", live: Boolean(working) },
                { id: "files", label: "Files", count: task.shared?.length ?? 0 },
                { id: "handoffs", label: "Handoffs", count: task.trail?.length ?? 0 },
                { id: "actions", label: "Actions", count: actions.length },
                { id: "events", label: "Events" },
                { id: "details", label: "Details" },
              ]}
            />
          </div>

          <div className="h-[80dvh] min-h-0 md:h-auto md:flex-1">
            {tab === "handoffs" && (
              // Focus stops under the stuck card header.
              <Scroll className="scroll-pt-10" onFocus={keepFocusInView}>
                <h2 className="sr-only">Handoffs</h2>
                <div className="max-w-4xl px-6 py-5">
                  {task.trail?.length ? (
                    <HandoffTimeline
                      steps={task.trail}
                      turns={turns}
                      coordinator={task.coordinator}
                      links={links}
                      actionsIn={(id) => actions.filter((a) => a.turn === id).length}
                      working={task.status === "working" ? working : ""}
                      onOpen={openTurn}
                      onWatch={watch}
                    />
                  ) : (
                    <EmptyState title="No handoffs yet">When the coordinator hands the task to an agent, the brief shows here.</EmptyState>
                  )}
                </div>
              </Scroll>
            )}
            {tab === "stories" && <StoryTable epic={task} onChanged={reload} />}
            {tab === "files" && (
              <SharedFiles task={task.id} kind={task.kind} files={task.shared ?? []} readOnly={archived} onChanged={reload} />
            )}
            {tab === "coordinator" && <CoordinatorChat task={task} onSent={reload} />}
            {tab === "activity" && (
              <ActivityFeed
                items={items}
                agents={roster}
                links={links}
                turn={turn}
                show={show}
                row={picked}
                now={now}
                loading={log.loading && !log.error}
                // With no events read, the error is the tab's; once some are, it is the foot's line.
                error={log.events.length ? undefined : log.error}
                foot={logFoot}
                onRetry={log.retry}
                onTurn={(id) => go({ tab: "activity", turn: id, show }, Boolean(id))}
                onShow={(next) => go({ tab: "activity", turn, show: next })}
              />
            )}
            {tab === "terminal" &&
              (working ? (
                // A new key each turn: every turn is a new session.
                <TerminalView key={`${task.id}-${task.turns}`} url={terminalURL(task.id)} title={working} />
              ) : (
                <EmptyState icon={<SquareTerminal />} title="No agent is working on this task">
                  The terminal of the agent at work shows here, live.
                </EmptyState>
              ))}
            {tab === "actions" && (
              <Scroll>
                <ResourceTable
                  columns={ACTION_COLUMNS}
                  rows={[...actions].reverse()}
                  rowKey={(a) => a.id}
                  onOpen={(a) => openRow({ action: a.id }, a.turn)}
                  empty={
                    <EmptyState title="No actions yet">Every tool call an agent makes shows here with the policy's decision.</EmptyState>
                  }
                />
              </Scroll>
            )}
            {tab === "events" && (
              <Scroll>
                {log.loading && !log.error ? (
                  <p className="px-6 py-6 text-center text-[12px] text-ink-faint">Loading the events…</p>
                ) : (
                  <ResourceTable
                    columns={EVENT_COLUMNS}
                    rows={[...log.events].reverse()}
                    rowKey={(e) => String(e.position)}
                    onOpen={(e) => openRow({ event: e }, turnOfEvent(e))}
                  />
                )}
                {logFoot}
              </Scroll>
            )}
            {tab === "details" && (
              <Scroll>
                <div className="grid gap-4 p-6 lg:grid-cols-2">
                  <Panel title="Task">
                    <KeyValueList
                      items={[
                        { label: "Id", value: <Mono>{shownId(task.id, task.kind)}</Mono> },
                        { label: "Status", value: <TaskStatusBadge status={task.status} /> },
                        ...(isEpic ? [] : [{ label: "Board column", value: COLUMN_TITLES[task.column] }]),
                        {
                          label: "Repo",
                          value: archived ? <Mono className="break-all">{task.repo}</Mono> : <RepoEditor task={task} onChanged={reload} />,
                        },
                        ...(isEpic
                          ? []
                          : [
                              {
                                label: "Epic",
                                value: archived ? (
                                  task.epic ? (
                                    <TaskLink id={task.epic} kind="epic" />
                                  ) : (
                                    <Mono className="text-ink-muted">none</Mono>
                                  )
                                ) : (
                                  <EpicPicker task={task} onChanged={reload} />
                                ),
                              },
                            ]),
                        { label: "Coordinator", value: <AgentName name={task.coordinator} /> },
                        { label: "Budget", value: <LimitEditor task={task} kind="budget" onChanged={reload} /> },
                        { label: "Turns", value: <Mono>{task.turns}</Mono> },
                        { label: "Max turns", value: <LimitEditor task={task} kind="turns" onChanged={reload} /> },
                        { label: "Created", value: dateTime(task.createdAt) },
                        { label: "Last active", value: dateTime(task.lastActiveAt) },
                        ...(task.firstTitle ? [{ label: "First title", value: task.firstTitle }] : []),
                      ]}
                    />
                  </Panel>
                  <Panel title="Worktree">
                    {w ? (
                      <KeyValueList
                        items={[
                          { label: "Machine", value: <Mono>{machineName(w.worker)}</Mono> },
                          { label: "Path", value: <Mono className="break-all">{w.path}</Mono> },
                          { label: "Branch", value: <Mono className="break-all">{w.branch}</Mono> },
                          { label: "Base", value: <Mono>{w.base.slice(0, 10)}</Mono> },
                          { label: "Port", value: <Mono>{w.port}</Mono> },
                        ]}
                      />
                    ) : (
                      <p className="text-[12px] text-ink-muted">
                        {task.status === "done" ? "Removed when the task was done." : "Not set up yet."}
                      </p>
                    )}
                  </Panel>
                  <Panel title="Turns" aside={<Mono>{turns.length}</Mono>} flush className="lg:col-span-2">
                    <ResourceTable
                      columns={TURN_COLUMNS}
                      rows={[...turns].reverse()}
                      rowKey={(t) => t.id}
                      onOpen={(t) => openTurn(t.id)}
                      empty={<p className="px-4 py-3 text-[12px] text-ink-muted">No turns yet.</p>}
                    />
                  </Panel>
                  <div className="lg:col-span-2">
                    <EraseTask task={task.id} kind={task.kind} onErased={setErasure} />
                  </div>
                </div>
              </Scroll>
            )}
          </div>
        </div>
      </div>
      {redacting !== null && (
        <RedactText task={task.id} kind={task.kind} marked={redacting} onClose={() => setRedacting(null)} onRedacted={reload} />
      )}
    </div>
  );
}

// The task's repo, and moving its work to another one between turns: its
// worktree is made again there, and the coordinator is told.
function RepoEditor({ task, onChanged }: { task: TaskDetail; onChanged: () => void }) {
  const notify = useToast();
  const [editing, setEditing] = useState<string | null>(null);
  async function save() {
    try {
      await api("PUT", `/api/tasks/${encodeURIComponent(task.id)}/repo`, { repo: editing });
      notify("success", "The task moved repos", "Its worktree is made again there before the next turn.");
      setEditing(null);
      onChanged();
    } catch (failure) {
      notify("danger", "The task didn't move", (failure as Error).message);
    }
  }
  if (editing === null) {
    return (
      <span className="flex flex-wrap items-center gap-2">
        <Mono className="break-all">{task.repo}</Mono>
        {task.movingTo && (
          <span className="text-[11px] text-warning">
            moves to <span className="font-mono">{task.movingTo}</span> when this turn ends
          </span>
        )}
        <Button size="sm" variant="ghost" onClick={() => setEditing(task.repo)}>
          Change
        </Button>
      </span>
    );
  }
  return (
    <span className="flex flex-col gap-2">
      <Input mono autoFocus value={editing} onChange={(e) => setEditing(e.target.value)} onKeyDown={(e) => e.key === "Enter" && save()} />
      <span className="text-[11px] text-ink-muted">
        A git repository on this machine, or an address to copy it from: a git address or a link to a .zip or .tar.gz, which Agentry clones
        or downloads. The task moves when the turn in progress ends, and the agents work in it from the next turn on.
      </span>
      <span className="flex gap-2">
        <Button size="sm" variant="primary" disabled={!editing.trim() || editing.trim() === task.repo} onClick={save}>
          Move the task
        </Button>
        <Button size="sm" variant="ghost" onClick={() => setEditing(null)}>
          Cancel
        </Button>
      </span>
    </span>
  );
}

// One fact in the strip under the header.
function Fact({ label, className, children }: { label: string; className?: string; children: ReactNode }) {
  return (
    <div className={cn("flex min-w-0 flex-col justify-center gap-1 border-r border-divider px-6 py-2", className)}>
      <span className="text-[11px] text-ink-muted">{label}</span>
      <div className="flex min-h-5 items-center">{children}</div>
    </div>
  );
}

function Scroll({
  children,
  className,
  onFocus,
}: {
  children: ReactNode;
  className?: string;
  onFocus?: FocusEventHandler<HTMLDivElement>;
}) {
  return (
    <div onFocus={onFocus} className={cn("h-full scrollbar-thin overflow-y-auto", className)}>
      {children}
    </div>
  );
}

// MadeFromLink goes to the page the owning extension gives the stream a task was
// made from; a stream no extension here owns is its name alone. It asks every
// extension, shown or not, since their routes are always mounted.
function MadeFromLink({ stream }: { stream: string }) {
  const to = extensionLink(stream, EXTENSIONS);
  if (!to) {
    return <Mono>{stream}</Mono>;
  }
  return (
    <Link to={to} className="font-mono text-info hover:underline">
      {stream}
    </Link>
  );
}

// TaskLink goes to another task, such as the one another team does for this.
function TaskLink({ id, kind }: { id: string; kind?: string }) {
  return (
    <Link to={`/tasks/${id}`} className="font-mono text-info hover:underline">
      {shownId(id, kind)}
    </Link>
  );
}
