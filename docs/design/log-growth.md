# Log growth: bounded startup and disk use

Status: the design, for task-23: measurements (section 1), the three
pieces and privacy (sections 2 to 5), projections and targets (6), the
recommendation (7) and the build plan (8). Research only: nothing in the
server changes with this document.

## In short

- The log grew 24,180 events, 27.8 MB, in under 3 days: 10 to 12 MB a
  busy day, 4 to 7 GB a year, and faster each month, because each
  `agent.learned` copies an agent's whole knowledge file. Knowledge is
  half the file.
- With the read model alone (`docs/design/read-model.md`), every startup
  replays everything: 7.8 s per GB, and the state holds 350 MB of heap per
  GB, since done tasks stay whole.
- No piece alone bounds startup (section 6): snapshots save a state that
  grows with history, slimming halves the bytes but not the events, and
  archiving can't reach knowledge.
- **Slimmer knowledge** (section 3): each `agent.learned` stores one hunk
  against the version before, with a full copy when the hunks outgrow the
  text: 4.7% of today's knowledge bytes. Old events replay as they are.
  Plans, commands and reports stay inline.
- **Snapshots** (section 2): `state.snapshot` beside the database, gob
  (JSON was measured too slow in build step 2), tagged with the binary's
  hash and anchored to a row's hash; any doubt means a full replay.
  Written every 10,000 events or 10 minutes, off the request path, and
  proven `Equal` to a full replay at every cut of the fixture logs.
- **Archiving** (section 4): 30 days after a task is done, a
  `task.archived` marker with its summary, and its rows moved into one
  gzipped blob (22%) in the content store, in one transaction; the delete
  trigger allows only rows a marker covers. Archived tasks open on demand,
  and reopen by putting their rows back.
- **Privacy** (section 5): possible. Erasing a task and redacting a secret
  are events too, and the triggers allow exactly the rows they name;
  backups stay out of reach.
- Together, for 20 open tasks and 30 busy days of done work, at 1, 10 and
  100 GB of history: startup 0.22 to 0.24 s on 8 cores (estimate; one
  core: 0.52 to 0.68 s), heap 118 to 174 MB, a live database of 0.20, 0.45
  and 3.0 GB.
- What still grows is the `agents` and `workers` streams, so the full
  replay after an upgrade is 1.9 s at 1 GB and 27 s at 100 GB (a deferred
  fix, section 7).
- Targets (section 6): startup with a snapshot under 0.5 s (with
  `GOMAXPROCS >= 2`; under 0.75 s on one core), the full
  replay under 2.5 s at 1 GB, heap under 135 MB and a live file under
  235 MB at that working set, an archived task open in under 100 ms.
- Order: slimmer knowledge first (it needs nothing from task-21, and every
  day without it adds bytes nothing can remove), then snapshots, archiving
  and privacy: [the build plan](#8-build-plan).

## 1. Measurements

### The data

A copy of the live database, made read-only with `VACUUM INTO` from a
`mode=ro` URI (it is not in git: it holds the user's data). The live file
was not written. Only counts, sizes and timings are quoted here. The log
starts on 16 October at 00:51 and ends on 18 October at 22:24, local time
(EDT): 69.5 hours.

| | |
|---|---|
| Events | 23,418, of which 33 are `proposal.rejected` |
| Streams | 27: 23 tasks, `agents`, `workers`, `connections`, `policy` |
| Database | 26.5 MB (6,478 pages of 4 KB, none free); 21.8 MB of it is event `data` |
| Rest of a row | 87 bytes on average (time, stream, actor, type, correlation) |
| Biggest task stream | `task-8`, 7,726 events, 4.0 MB |
| Busiest hour | 3,683 events, 4.1 MB |

Bytes by stream kind:

| Streams | Events | Data |
|---|---|---|
| `agents` | 516 | 12.67 MB |
| 23 task streams | 22,354 | 9.09 MB |
| `workers` | 537 | 42 KB |
| `connections`, `policy` | 11 | 1.4 KB |

### Growth per day

By local day. No day was quiet: the first was setting up, the last ends
at 22:24.

| Day | Hours with events | Events | Data | Data per active hour |
|---|---|---|---|---|
| 16 October | 5 | 968 | 0.37 MB | 0.07 MB |
| 17 October | 18 | 13,375 | 11.71 MB | 0.65 MB |
| 18 October | 5 | 9,075 | 9.73 MB | 1.95 MB |

Activity is bursty: 24 of the 28 hours with events have more than 100,
and hold 99% of the bytes. A busy day is 10 to 12 MB. If agents worked all
24 hours at the mean rate of an active hour (0.78 MB), a day would be
19 MB.

The rate grows even at the same activity. The mean `agent.learned` event
was 1.8 KB on 16 October, 20.6 KB on the 17th and 46.9 KB on the 18th: each
event copies a knowledge file that only gets longer. 18 October has half
the events of the 17th, and more knowledge bytes (6.56 MB against 5.98 MB).

### Bytes and counts by event type

Top ten by bytes (the top five are 96% of all data):

| Type | Events | Data | Share | Mean | Biggest |
|---|---|---|---|---|---|
| `agent.learned` | 446 | 12.57 MB | 57.6% | 28 KB | 59 KB |
| `action.requested` | 6,156 | 3.72 MB | 17.0% | 604 B | 32 KB |
| `turn.ended` | 510 | 2.00 MB | 9.2% | 3.9 KB | 8.6 KB |
| `plan.updated` | 951 | 1.78 MB | 8.1% | 1.9 KB | 14 KB |
| `task.assigned` | 239 | 0.89 MB | 4.1% | 3.7 KB | 6.3 KB |
| `action.approved` | 6,147 | 0.42 MB | 1.9% | 67 B | 68 B |
| `agent.changed` | 44 | 67 KB | | 1.5 KB | 2.4 KB |
| `turn.started` | 534 | 50 KB | | 93 B | 116 B |
| `task.created` | 23 | 39 KB | | 1.7 KB | 4.0 KB |
| `task.needs_you` | 43 | 38 KB | | 885 B | 7.6 KB |

Top ten by count: `action.requested` 6,156, `action.approved` 6,147,
`action.result` 6,064, `plan.updated` 951, `turn.started` 534,
`session.started` 532, `prompt.acknowledged` 531, `turn.ended` 510,
`agent.learned` 446, `worker.connected` 255. The three action events are
78% of the count and 19% of the bytes.

### Startup replay

`internal/current` does not exist yet (task-21 builds it), so this times
what its `Load` will do with today's code: open the store, read every task
stream with `TaskIDs` and `Store.Stream`, plus the `agents`, `workers` and
`connections` streams, and fold each with its `FromEvents`. Each run opens
a fresh store. Min of 3, on an idle 32-core Linux machine:

| Step | Time |
|---|---|
| Open, read and decode every stream | 156 ms |
| Fold every stream | 50 ms |
| Whole startup | 207 ms, 0.89 s per 100k events |

The state this builds holds **9.2 MB of heap** (9,242 KB), about 400
bytes per event.
That is seven times the 1.3 MB in `read-model.md`, which measured the state
while the decoded events stayed alive, so the texts the state shares with
them were not counted. The state keeps every action's command, every
turn's plan and report, and the knowledge files.

### Snapshot cost

A throwaway snapshot of the whole folded state: every task, the agents
roster and the workers. `tasks.Task` has two unexported fields,
`collected` and `storyNews`, so each task is encoded as a mirror struct
holding the `*Task` and those two, read and set back through reflection.
Min of 3:

| Format | Size | Encode | Decode | gzip size | gzip write | gunzip |
|---|---|---|---|---|---|---|
| JSON | 10.25 MB | 17.6 ms | 30.1 ms | 2.45 MB | +68 ms | +36 ms |
| gob | 9.06 MB | 9.6 ms | 4.9 ms | 2.39 MB | +71 ms | +31 ms |

With `collected` and `storyNews` set back, both round trips are
`reflect.DeepEqual` to the replay for every task and the agents roster.
The workers are not: `Worker.credential` (the hash of its credential) is
unexported and is lost, and so are the unexported `invites` of
`workers.Roster` and `connections.Roster`. A real snapshot must carry all
of them, so each package needs its own exported snapshot form.
`agents.Agent` has one more, `untouched` (`internal/agents/agent.go`), and
`connections.Connection` has its own `credential`
(`internal/connections/connections.go`). The agents' round trip came out
equal only because no agent in the copy is still untouched (below); with
one, it would not have.

Where the snapshot's bytes are (JSON):

| Part | Size |
|---|---|
| Actions of every task | 5.04 MB, about 818 B per action |
| Turns of every task (plans, reports) | 3.04 MB, about 5.7 KB per turn |
| The rest of the tasks (trail, files, descriptions) | 1.98 MB |
| Agents roster, knowledge included | 0.20 MB |
| Workers | 0.7 KB |

Per task:

| | Tasks | Mean | Largest |
|---|---|---|---|
| Open | 6 | 271 KB | 500 KB |
| Done | 17 | 496 KB | 4.04 MB (`task-8`) |
| Any task, without its actions | 23 | 218 KB | 2.13 MB |

So the snapshot is dominated by history, not open work: done tasks are 82%
of it, and a task's size grows with its actions and turns, open or not. A
snapshot of every task is about the size of the task streams' data. It
saves the replay time, but not memory or disk, unless done tasks leave the
state (archiving).

### Archiving potential

Which tasks are done, from the folded state, and when, from their last
`task.done` event, counted back from the log's last event. The data is
under 3 days old, so nothing is 3 or 7 days done yet.

| Done for at least | Tasks | Events | Data |
|---|---|---|---|
| any time | 17 | 18,100 (77.3%) | 7.74 MB (35.5%) |
| 1 day | 12 | 7,189 (30.7%) | 2.83 MB (13.0%) |
| 3 days | 0 | 0 | 0 |
| 7 days | 0 | 0 | 0 |

Done tasks hold most events but a third of the bytes, because the biggest
bytes, knowledge, are in the `agents` stream, which belongs to no task.
Archiving tasks can't shrink knowledge; only slimmer events can.

A one-row summary per archived task, sized as the task list's JSON
(`summaryOf`), is 624 bytes on average and 829 at most. For 17 tasks that
is 11 KB in place of 7.7 MB.

### Slimmer events

Each big text, decoded from its event: `knowledge`, `command`, `report`,
`plan` and `instructions`. Exact repeats are texts whose SHA-256 was seen
before. Line diffs are an ed-style diff from the previous version in the
same series (an agent's knowledge, one turn's plan): every added line plus
12 bytes per changed hunk, with the first version whole. Commands, reports
and instructions are not versions of anything, so a diff is the whole
text.

| Type | Texts | Bytes | Exact repeats | As line diffs |
|---|---|---|---|---|
| `agent.learned` | 446 | 12.39 MB | 0.47 MB (3.8%) | **0.36 MB (2.9%)** |
| `action.requested` | 6,156 | 3.34 MB | 26 KB (0.8%) | n/a |
| `turn.ended` | 510 | 1.95 MB | 0 | n/a |
| `plan.updated` | 951 | 1.74 MB | 0.76 MB (43.3%) | **1.02 MB (58.4%)** |
| `task.assigned` | 239 | 0.86 MB | 8 KB (0.9%) | n/a |

Knowledge changes by a few lines at a time: diffs keep 3% of its bytes.
Plans are often written again unchanged (43% are repeats), and diffs keep
58%. The other texts are unique, so a hash store gains nothing on them
but moving them out of the rows.

Texts over a threshold moved to the content-addressed store, the event
keeping `{"sha256": ..., "size": N}` (91 bytes), each unique text stored
once:

| Threshold | Data left in the log | Blobs | Blob bytes | On disk, 4 KB blocks | gzipped one by one |
|---|---|---|---|---|---|
| 1 KB | 2.97 MB (13.6%) | 2,108 | 17.49 MB | 21.97 MB | 7.68 MB |
| 4 KB | 5.98 MB (27.4%) | 867 | 14.64 MB | 16.89 MB | 6.30 MB |

Blob bytes by type at 1 KB: knowledge 11.91 MB, commands 2.07 MB, reports
1.94 MB, instructions 0.86 MB, plans 0.71 MB.

A hash store alone moves bytes; it hardly removes any. At 1 KB the log
file falls to 6.1 MB but 17.5 MB of blobs (22 MB in 4 KB blocks) come on
top: more disk than today, unless blobs are compressed (7.7 MB gzipped).
Only the repeats are saved.

### The resulting live database

The copy's rows written into new database files, each with a change
applied, then `VACUUM`:

| | Log file | Blobs beside it |
|---|---|---|
| As it is | 26.53 MB | |
| Texts over 4 KB moved | 10.14 MB | 14.64 MB |
| Texts over 1 KB moved | 6.11 MB | 17.49 MB |
| Knowledge and plans as line diffs, the rest over 1 KB moved | 7.22 MB | 4.87 MB |
| Done tasks archived (one 600-byte summary each) | 15.35 MB | |
| Archived, texts over 1 KB moved | 1.45 MB | the live part of 17.49 MB |
| Archived, diffs, the rest over 1 KB moved | 1.86 MB | the live part of 4.87 MB |

Diffs are what shrink the total: 26.5 MB becomes 12.1 MB (7.2 MB log plus
4.9 MB of blobs) with every event kept. Archiving then takes the live log
under 2 MB.

### VACUUM

On a second scratch copy, never the live file: drop the delete trigger,
delete every event of the streams archiving would move, check the free
pages, `VACUUM`, check the file.

| Archived | Streams | Events left | File after the delete | Free pages | After `VACUUM` |
|---|---|---|---|---|---|
| Done at all | 17 | 5,318 | 26.53 MB | 2,657 | **15.34 MB** |
| Done for 1 day | 12 | 16,229 | 26.53 MB | 1,002 | **22.26 MB** |

Deleting frees pages inside the file but the file does not shrink; only
`VACUUM` gives the space back. A `VACUUM` of the whole 26.5 MB copy takes
0.13 s (min of 3), about 5 s per GB, and needs free disk as big as the
database while it runs. The database has `auto_vacuum` off (0), so
`PRAGMA incremental_vacuum` is not available without first rebuilding
with `auto_vacuum = INCREMENTAL`.

### Measured for the design

From the later copy (24,180 events, a 27.8 MB file, 22.9 MB of data),
with the cases sections 2 to 5 needed added to the same benchmark:

| | |
|---|---|
| Agents in the roster | 17, none still `untouched` |
| Knowledge files now | 9.9 KB mean, 59 KB largest |
| Snapshot, done tasks as summaries (gob) | 1.47 MB (6 open tasks, 18 summaries), decoded in 1.4 ms; 0.43 MB gzipped |
| Writing the full 9.4 MB gob snapshot atomically (temporary file, fsync, rename, fsync the folder) | 3.2 ms |
| Hashing the 26 MB `agentry` binary (`sha256sum`, the whole process) | 70 ms |
| Every text over 1 KB gzipped inside its row, as base64 | data 62.6% of what it is |

Knowledge versions stored as diffs, with a full copy now and then (line
diffs as above; "most read" is a full copy plus the diffs after it, the
bytes needed to rebuild one version):

| Knowledge stored as | Bytes | Of 13.18 MB | Longest chain | Most read |
|---|---|---|---|---|
| Whole, as today | 13.18 MB | 100% | 0 | 59 KB |
| A full copy every 8 versions | 1.97 MB | 15.0% | 7 | 65 KB |
| A full copy every 32 versions | 0.77 MB | 5.8% | 31 | 85 KB |
| A full copy every 128 versions | 0.42 MB | 3.2% | 127 | 168 KB |
| A full copy when the diffs since the last outgrow the text | 0.62 MB | 4.7% | 100 | 118 KB |
| One hunk per version (the common head and tail kept, the middle stored), no full copies | 0.35 MB | 2.7% | all | |

One hunk does as well as a line diff (2.7% against 2.8%): an agent changes
its file in one place per turn, mostly at the end. Plans gain nothing from
either: one hunk keeps 57.8% of their 1.78 MB, and the cadence of full
copies changes nothing, because a turn's plan has at most 10 versions and
most plan events are a turn's first.

Rebuilt database files, as in the table above:

| | Log file | Beside it |
|---|---|---|
| As it is | 27.78 MB | |
| Knowledge as one hunk per version | 14.46 MB | |
| Knowledge as one hunk, done tasks archived | 2.64 MB | 2.28 MB of archive blobs |

The archive blobs: each of the 18 done tasks' rows, rejections included,
as JSON lines, gzipped one task to a blob: 10.19 MB becomes 2.28 MB
(22.3%). The largest, `task-8`, is 4.84 MB raw and 1.08 MB gzipped, and
gunzips in 17 ms.

### Projection inputs

The numbers the 1, 10 and 100 GB projections will use, all from the
measurements above:

- **Events per GB of database file:** about 880,000 (23,418 events in
  26.5 MB, 1,133 bytes per event in the file, 931 of them `data`).
- **Startup replay:** 8.9 µs per event, so about 7.8 s per GB of file.
  Of that, reading and decoding is 75% (scales with bytes), folding 25%
  (scales with events).
- **Heap held by the state:** about 400 bytes per event, so about 350 MB
  per GB of file, if done tasks stay in the state.
- **Snapshot:** gob 0.34 bytes per byte of file now (9.06 MB for 26.5 MB),
  decoded at about 0.54 ms per MB, plus 3.4 ms per MB to gunzip.
- **Per open task in the state:** 271 KB on average, 500 KB at most, and
  growing with its history: about 818 bytes per action and 5.7 KB per
  turn. Per done task: 496 KB on average.
- **Archive summary:** about 625 bytes per archived task.
- **Growth:** 10 to 12 MB on a busy day today, up to 19 MB if agents worked
  around the clock, and rising with knowledge size: about 4 GB a year at a
  busy day every day, 7 GB at all hours, before knowledge growth.
- **Share by kind:** knowledge 57.6% of bytes, other task texts 38%;
  done tasks 77.3% of events and 35.5% of bytes (with all tasks counted as
  they are now).
- **Slimming factors on the whole log**, for section 3's pick (knowledge
  alone, one hunk, a full copy when the hunks outgrow the text): knowledge
  keeps 4.7% of its bytes, and the log file falls from 27.78 MB to
  14.46 MB (0.52 of today's); archiving done tasks after that leaves
  2.64 MB (0.095), plus 2.28 MB of archive blobs.
- **VACUUM:** about 5 s per GB, with free disk equal to the file.

### Rerun the measurements

```sh
# a read-only copy of the live database (never write to the live one)
mkdir -p .agentry/files/bench
sqlite3 'file:'"$HOME"'/.agentry/agentry.db?mode=ro' \
  "VACUUM INTO '$PWD/.agentry/files/bench/agentry.db'"

# totals, days and types
sqlite3 -column 'file:.agentry/files/bench/agentry.db?mode=ro' "
  SELECT COUNT(*), SUM(type = 'proposal.rejected'), COUNT(DISTINCT stream),
    SUM(length(CAST(data AS BLOB))) FROM events;
  SELECT substr(datetime(at, 'localtime'), 1, 10) AS day, COUNT(*),
    SUM(length(CAST(data AS BLOB))),
    COUNT(DISTINCT substr(datetime(at, 'localtime'), 1, 13)) FROM events GROUP BY day;
  SELECT type, COUNT(*), SUM(length(CAST(data AS BLOB))) AS bytes,
    MAX(length(CAST(data AS BLOB))) FROM events GROUP BY type ORDER BY bytes DESC LIMIT 10;"

# replay, snapshot, archiving and slimmer events
AGENTRY_BENCH_DB=$PWD/.agentry/files/bench/agentry.db \
  go test ./internal/web -run TestLogGrowthBench -v -count=1

# VACUUM after archiving, on a scratch copy (1 for "done for a day", 0 for "done")
cp .agentry/files/bench/agentry.db .agentry/files/bench/vacuum.db
sqlite3 .agentry/files/bench/vacuum.db "
  DROP TRIGGER events_no_delete;
  CREATE TEMP TABLE archived AS SELECT stream FROM events GROUP BY stream
    HAVING MAX(CASE WHEN type = 'task.done' THEN position END)
         > COALESCE(MAX(CASE WHEN type = 'task.reopened' THEN position END), 0)
       AND julianday((SELECT MAX(at) FROM events))
         - julianday(MAX(CASE WHEN type = 'task.done' THEN at END)) >= 0;
  DELETE FROM events WHERE stream IN (SELECT stream FROM archived);
  SELECT COUNT(*) FROM events; PRAGMA freelist_count;"
ls -l .agentry/files/bench/vacuum.db
sqlite3 .agentry/files/bench/vacuum.db VACUUM
ls -l .agentry/files/bench/vacuum.db
```

The benchmark is `internal/web/loggrowth_bench_test.go`; it is skipped
unless `AGENTRY_BENCH_DB` is set, copies the file again so the run never
writes to it, and takes about 5 s. The rebuilt databases go to the test's
temporary folder.

The figures above are from a copy taken at 22:24. A rerun from a fresh
copy 8 minutes later had 762 more events (24,180) and a 27.8 MB file, and
every share above moved by under 3 points: knowledge diffs kept 2.8%, done
tasks held 35.6% of the bytes, and the archived, diffed live log was
1.7 MB.

The cases for sections 2 to 5 are `benchDesign` in the same file; they
ran on the later copy, and their output is kept beside it in
`.agentry/files/bench/log-growth-run-2.txt`.

## 2. Snapshots of the read model

`read-model.md` left snapshots out while a replay took 0.2 s, and said
they could come later under the same `current.State` API if startup ever
passed a few seconds. At 7.8 s per GB of file (section 1), a year of busy
days (about 4 GB) would take about 30 s to start, so they are needed. This
section adds them without changing that API.

One promise changes in wording, not in substance. `internal/tasks/task.go`
says "The state is never stored, so it can never disagree with what
happened", and `read-model.md` section 3 counts that against snapshots.
With them the state is stored, but only as a cache that is thrown away
whenever it might disagree: the log stays the only truth, and a full
replay stays the definition of the state.

### Where it lives

| | A file beside the database | A table in the database |
|---|---|---|
| Atomic | temporary file, fsync, rename, fsync the folder | a transaction |
| Writing it | 3.2 ms for 9.4 MB, outside SQLite | the whole snapshot through the WAL and again at checkpoint, on the one write connection, so proposals wait |
| The log's file | unchanged | a table outside the append-only triggers, rewritten beside the truth; the file churns by a snapshot per write |
| Backups and `VACUUM INTO` copies | not carried | carried, so a restore brings its own snapshot |
| Starting over | delete the file | a `DELETE` |

**Pick: a file**, `state.snapshot` in the data folder beside `agentry.db`,
mode 0600, since it holds the same texts as the log. Not being carried by
a backup is no loss: every snapshot is checked against the log before use
(the anchor, below), and a missing one costs one replay.

### Format

The state has unexported fields an encoder can't reach:
`tasks.Task.collected` and `storyNews` (`internal/tasks/task.go`),
`agents.Agent.untouched` (`internal/agents/agent.go`),
`workers.Worker.credential` and `workers.Roster.invites`
(`internal/workers/workers.go`), and `connections.Connection.credential`
and `connections.Roster.invites` (`internal/connections/connections.go`).
Each one changes behaviour if lost: `untouched` decides whether a new
default prompt replaces an agent's (`internal/agents/defaults.go`), and a
lost `credential` locks a worker out.

| | Per-package saved forms | One encoder using reflection and `unsafe` (as the benchmark does) | Export the fields |
|---|---|---|---|
| What | a `Saved` type and a `Save`, `Restore` pair in each package | reads and sets unexported fields from outside | rename them |
| A field added later | a reflection test fails until the form has it | carried, by accident | carried |
| Cost | one struct and two short functions per package | `unsafe` in production code; a rename breaks it silently | anyone could set a credential or `untouched` |

**Pick: saved forms**, each beside its type, so a field and its saved
twin change in the same file:

```go
// Saved is a task as a snapshot keeps it: every field, exported.
type Saved struct {
	Task      Task            // its exported fields; the encoder skips the rest
	Collected map[string]bool // collected
	StoryNews bool            // storyNews
}

func (t *Task) Save() Saved
func Restore(s Saved) *Task
```

`agents.Saved` adds `Untouched` to each agent; `workers.Saved` and
`connections.Saved` add each `Credential` and the invites (code, expiry,
used). A test in each package walks the type with reflection and fails if
any field, exported or not, has no counterpart, then round-trips a value
with every field set (task-21's `fill` helper in
`internal/tasks/clone_test.go` does the setting) and requires it
`reflect.DeepEqual` to the original.

Encoding, on the later copy (section 1):

| | JSON | gob |
|---|---|---|
| Every task whole, as today | 10.6 MB, encoded in 20 ms, decoded in 28 ms | 9.4 MB, 7.6 ms, 4.3 ms |
| Done tasks as summaries (section 4) | about 1.7 MB and a few ms, scaled from gob | 1.47 MB, decoded in 1.4 ms |
| `nil` and empty | kept apart (`null`, `[]`) | both decode as `nil` |
| Read by a person | `jq` | no |

gzip would save 8 MB on the whole state and about 1 MB on the summarised
one, and costs 31 to 34 ms on every read and about 70 ms on every write.

**First pick, before measuring: JSON, not compressed.** Its decode is
under 30 ms even with every done task in it, and it keeps `nil` and empty
apart, which `reflect.DeepEqual`, and so the equivalence test, does not
forgive. Section 6 projects 0.36 s of decode on one core for a month of
done work, so the plan was one entry to a line, decoded in parallel: 0.22
to 0.24 s on 8 cores (estimate; one core: 0.52 to 0.68 s) of startup
(section 7). Build step 2's measurement, below, turned JSON down: the body
that was built is gob chunks, decoded in parallel the same way (the file
is described after it). **The one rule for leaving JSON.** Build step 2 times the
one-entry-per-line JSON decode of a body sized for 100 GB of history, as
Target 1's own measurement is: the working set plus 90,000 archived
summaries, about 186 MB by formula (e) (128.5 MB + 57 MB + 0.2 MB). It
times it on 1, 2 and 8 goroutines and reports all three; the rule reads
the 2-goroutine time, the weakest case Target 1 promises. If that takes
more than 0.34 s (0.5 s minus the fixed 0.16 s: 70 ms of hashing and 90 ms
of tail), the fallback is chosen there, before step 4 is built: gob over
saved forms that record `nil` versus empty explicitly (a presence flag per
slice or map), so equivalence test 1 still passes on a gob round trip. The
projection passes with margin: 186 MB × 2.8 ms/MB is 0.52 s on one core,
0.26 s on two, plus 11 ms for the largest task decoded alone, so startup
is about 0.16 + 0.26 + 0.011 ≈ 0.43 s. If gob also misses 0.5 s with two
or more cores, the build stops and asks the user; 0.75 s on one core stays
the limit.

**Measured in build step 2: gob.** `TestStartupBench` on a 1 GB synthetic
log grown from the 19 October copy: a 201.7 MB body (128.5 MB of working-set
tasks, 90,000 summaries at about 810 B each) decodes as JSON lines in 0.81 /
0.43 / 0.14 s on 1 / 2 / 8 cores, and as gob (127.7 MB, 64 streams) in
0.14 / 0.10 / 0.08 s. JSON misses 0.34 s on two cores, and so would a 186 MB
body (about 0.40 s), so step 4 writes gob over saved forms with presence
flags.

**Built in step 4a: the presence flags by reflection.** Gob writes an
empty slice or map, and a pointer to a zero value, as nothing, and reads
nothing back as `nil`; it also reads a `nil` map inside a slice back as an
empty one. Rather than a hand-kept flag beside each slice and map in each
saved form, `internal/exactgob` writes the value with gob, then its shape:
one bit for every slice, map and pointer in it, set when it isn't `nil`,
in a fixed order (fields in order, elements in order, map entries by key).
Reading sets each one back as its bit says. That is a presence flag per
slice, map and pointer, made by the same walk on both sides, so a field
added later has its flag without anyone adding it, and the saved forms
stay as they were. Each package's saved-form test round-trips through both
JSON and `exactgob`, filled by `deeptest.Fill` and replayed with `nil` and
empty lists side by side.

The file is one header line, then the body:

```text
{"format":1,"build":"<sha256 of the binary>","position":24180,"anchor":"<sha256 of row 24180>","size":10589375,"sha256":"<sha256 of the body>"}
<chunk><chunk>...
```

Each chunk is its length (a uvarint), then an `exactgob` value, so the
chunks are split without decoding and decode on their own, in parallel
(`current.inParallel`, one goroutine per core). The first chunk is
everything but the tasks: the positions, `created` and the task count,
each owned prefix's order, the broken streams' names, the archived set, the
rosters' saved forms, the settings and the skipped tally. Each later chunk
is a run of 16 tasks' saved forms with their streams. The anchor is
`store.Hash`: the sha256 of the row's columns as stored, as a JSON array.

The header is checked before the body is read, so a snapshot from other
code is turned down without decoding megabytes.

The body holds everything `current.Equal` compares, so a loaded state can
be equal to a replay: every task, the summaries, the rosters, and two
more. `order` is `State.taskIDs` as it is, duplicates and broken tasks
included, since the state keeps every `task.created` row in its
`created` list (`State.TaskIDs`). `broken` is the names of the streams whose apply
panicked, without their state.

A broken stream is **rebuilt at load by a full replay of that stream**
(its accepted events up to the snapshot's position, folded as `Load` folds
them), so it breaks again by the same panic and says why. The other choice, writing
no snapshot while any stream is broken, was dropped: a stream stays broken
until a release fixes its apply, and that may be many restarts away, so
startup would go back to replaying the whole log exactly when something is
already wrong. The rebuild costs one stream's replay. A broken roster then
reads as empty, not as it was before the event that broke it; that is what
a restart does today, and `Equal` only asks that both sides fail.

**Built in step 4a: owned streams are rebuilt too.** A feature's own
streams (`features.Streams`, the ideas) keep `any` state with no saved
form. Rather than add an optional `Save` and `Restore` to
`features.Streams`, which every feature would have to write and test,
Resume rebuilds each owned stream the same way as a broken one, from its
events up to the snapshot's position; the snapshot keeps only each prefix's
order. They are small: on the 19 October copy, 81 of 149,839 rows in 42
idea streams.

### The version tag

| | A constant bumped by hand | A hash of the apply code's source | The build: a hash of the running binary |
|---|---|---|---|
| A change nobody marks | a stale snapshot loads silently; a golden test catches only what its fixture reaches | apply leans on helpers in other packages (the `events` accessors, `tasks` helpers), so which files count is itself a list to keep | can't happen |
| Snapshots kept across an upgrade | yes, when nothing changed | yes, when no listed file changed | no: each new binary replays once |
| Cost | a test and a habit | a `go generate` step | hashing the binary at startup: 70 ms for 26 MB with `sha256sum`, while the database opens |

The VCS revision from `debug.ReadBuildInfo` is not enough: two builds of
the same uncommitted tree share it.

**Pick: the binary's hash.** It costs one full replay after each upgrade
or rebuild, and archiving (section 4) keeps that replay small, because the
live log then holds only open and recent work. There is no bump to forget,
so no test has to catch one; a test checks that a snapshot with another
`build` is ignored. `format` changes only if the header itself does.

### When it is written

- **Trigger:** a goroutine in `current`, woken as events are applied.
  It writes when 10,000 events have been applied since the last snapshot,
  or when 10 minutes have passed with at least one event, at a clean
  shutdown, and after each archiving round (section 4), which shrinks the
  state.
- **Never on the request path, and never under `Store.mu`.** It takes the
  state's read lock only to copy the map of task pointers, the summaries,
  the rosters, the position, the `taskIDs` slice and the names of the
  broken streams (a new slice and a new list, since `Apply` appends to
  `taskIDs` in place). Copy on write (`read-model.md`, "Locking") means nothing
  it then encodes can change, so it encodes and writes with no lock held.
  One write at a time; a failed write is logged and leaves the old file.
- **Cost now:** about 25 ms of CPU (encode 20 ms, write 3.2 ms) per
  10 MB, about every 3 busy hours at today's rate. With done tasks as
  summaries, a few ms on the copy, and about 0.3 s at section 6's working
  set, a month of done work.
- **The tail it can leave:** at most 10,000 events or 10 minutes, so at
  most about 90 ms of replay (8.9 µs per event, section 1).

### Atomic writes

1. `os.CreateTemp` in the data folder (`state.snapshot.tmp-*`), the
   header and body written, `Sync`, `Close`.
2. `os.Rename` over `state.snapshot`.
3. Open the folder and `Sync` it, so the rename itself survives a power
   cut (Linux and macOS; Windows can't sync a folder, and its rename
   replaces in place).
4. At startup, delete any leftover `state.snapshot.tmp-*`.

The header's `size` and `sha256` catch a torn file anyway, if a disk lies
about fsync.

### Loading it, and every fallback

At `Open`: read the header; check `format` and `build`; read row
`position` and compare its hash with `anchor`; read the body and check
`size` and `sha256`; decode; load the state from it; rebuild each stream
named in `broken` (above); then apply every event after `position` in
order (by primary key, so the cost is the tail, not the log), and call
`Loaded` with the last position. The tail is applied in place, with no
clone per event: nothing has read the decoded state yet. A tail event that
folds its stream again (`task.unarchived`, `event.redacted`, section 4) is
given its stream's events below its own position, never the stream's
current rows, which hold the rest of the tail too.

| Case | Found by | Then |
|---|---|---|
| Missing | no file | full replay |
| Corrupt or torn | a header that doesn't parse, `size` or `sha256` wrong, a decode error | full replay; the file is kept once as `state.snapshot.bad` |
| From other code | `build` differs | full replay |
| Ahead of the log, as after restoring an older database | no row at `position` | full replay |
| From another log at the same position, as after a restore that has since grown past it | the row's hash differs from `anchor` | full replay |
| Its anchor row archived since (section 4) | no row at `position` | full replay; rare, because a snapshot is written after every archiving round |
| The tail's apply panics | `guard` in `current` | throw the loaded state away and replay fully; if that panics too, the stream is broken, as today. A stream the log itself breaks after the snapshot lands here too, as Resume can't tell it from one the snapshot breaks: one whole replay, then the next snapshot holds the stream as broken and loads as usual |
| Older than the log | `position` below the last | the normal case: apply the tail |

Every fallback logs why, and a full replay at startup writes a fresh
snapshot straight after, in the background.

**Built in step 4a.** The store has an optional `store.Resumer`, which
`current.State` is: `Open` calls `Resume(store.Past)` first, in its one
transaction, and `Past` reads the log as `Open` loads it (`Last`, `Hash`,
`Stream` up to a position, `ApplyAfter`). Resume makes every check above,
rebuilds the broken and owned streams, applies the tail in place (a task
changes without a copy per event, as nothing has read it yet) and calls
`Loaded`; a snapshot that doesn't fit empties the state, and `Open` loads
it whole. Step 4b calls `current.RemoveLeftovers(path)` and
`current.Restore(path, build, catalogue, owned...)` before `store.Open`,
and `current.WriteSnapshot(path, state, log, build)` to write, which is
atomic (below). `current.EqualThroughSnapshots` is equivalence test 1's
judge: `storetest.Open` runs it at cleanup on every store a test opens (at
every position of a log of 40 events or fewer, otherwise at 40 seeded ones
and the last), and `internal/current` on its fixture logs at every
position. The full replay is
`read-model.md`'s `Open`, which groups the whole table by stream: it
scans every row, so its cost is the live log's size, and archiving is what
keeps that bounded.

**Built in step 4b.** `current.SnapshotWriter` is the trigger above: a
goroutine woken by `State.Apply` and `Refold` (a channel of one, sent on
without waiting), which writes once 10,000 events have been applied since
the last snapshot, once 10 minutes pass with at least one, at once after
a start that replayed the whole log (`State.Resumed` is 0), and at
`Close`, unless the snapshot on disk already holds the state. It copies
the state under its read lock and encodes and writes with no lock held; a
failed write is logged and leaves the old file, and the next trigger tries
again. Its clock, thresholds and file writer are fields a test sets.
`cmd/agentry/serve.go` keeps `state.snapshot` in the data folder: it calls
`RemoveLeftovers` and `Restore` before `store.Open`, starts the writer
once the log is open and closes it, which writes the last snapshot,
before the log closes. The build is the sha256 of the running binary,
read at startup; a binary that runs but can't be read logs a warning and
gets a build that differs on every start (`buildOrUnhashed`), so no
snapshot is used and every start replays the whole log. The write after
each archiving round waits for the archiver (step 7). Tests: `TestTheWriterWritesAfterEnoughEventsOrTimeWithAnEvent`
(Target 3's rule, with a fake clock), `TestAProposalCommitsWhileTheWriterIsMidWrite`
(Target 6's "never under `Store.mu`"), `TestCloseWritesASnapshotTheNextStartGoesOnFrom`,
`TestAFailedWriteIsLoggedAndTheWriterGoesOn`, `TestAStartAfterAFallbackWritesAFreshSnapshot`,
`TestAResumedStartWritesNothingNew`, and `TestServeKeepsTheSnapshotAcrossARestart`
and `TestAnUnhashedBuildNeverUsesASnapshot` in `cmd/agentry`; equivalence
test 2 is `TestARealLogThroughASnapshotEqualsAReplay` on the copy and
`TestAFixtureLogThroughASnapshotEqualsAReplay` on fixture logs, and
`TestStartupBench` times Targets 1, 3 and 6.

### The equivalence test

`current.Equal` (task-21's, `reflect.DeepEqual` with unexported fields
included) against a full replay is the judge, as in `read-model.md`.

1. **Every cut of the fixture logs.** For each log the suite builds
   (every scenario's store, `read-model.md` test 2), task-21's
   `everyEvent()` log, the seeded random sequences (test 4), and a log
   with a broken task stream (as task-21's
   `TestAPanicBreaksOnlyItsStream` builds: a `worktree.moved` before the
   task has a worktree) and events on that stream after the break: at every
   position, build the state of the events up to it, write a snapshot,
   load it back as `Open` would, apply the rest, and require it `Equal` to
   a full replay. Logs of a few thousand events make that a few seconds.
2. **The real copy**, when `AGENTRY_BENCH_DB` is set: cuts every 500
   positions, about 50 of them, plus the first event of each type.
3. **Every fallback** in the table: a truncated file, one byte of the body
   flipped, another `build`, a position past the end, a missing anchor
   row, and a copy of the log with one row changed under the anchor. Each
   must load `Equal` to a full replay and log its reason.
4. **Sabotage checks.** Leaving `StoryNews` out of `tasks.Saved` (with a
   fixture where a story finishes while the coordinator decides,
   `internal/tasks/apply.go` line 145), leaving out `Worker.credential`,
   or decoding the body with plain gob of the structs, must each fail
   test 1. That case is plain gob, where `nil` and empty merge; it does not
   apply to the fallback's saved forms, which record `nil` versus empty
   with a presence flag per slice or map and must pass test 1 on a gob
   round trip.
5. **The forms are complete:** the reflection test in each package
   (above).

## 3. Slimmer events

What is big (section 1): knowledge 57.6% of the bytes, commands 17%,
reports 9%, plans 8%, instructions 4%. Knowledge is the odd one out
twice over. It is the only big text in no task (the `agents` stream), so
archiving can't move it. And it is the only one copied again and again:
each `agent.learned` holds the whole file
(`agents.Agent.Learned`, `internal/agents/agent.go`, written by
`hooks.recordWhatWasLearned`, `internal/hooks/handler.go`), of which 2.7%
is new. The task texts are mostly unique (under 1% exact repeats, plans
43%) and sit in task streams, which archiving moves out and gzips to 22%
(section 4).

| | (a) Hash references into the content store | (b) Diffs of versions | (c) A bound on the knowledge file | (d) Compression inside the rows |
|---|---|---|---|---|
| Knowledge, 13.18 MB | 12.6 MB of unique blobs: moved, hardly saved | 0.62 MB with a full copy when the diffs outgrow the text (4.7%); 0.35 MB as one hunk with no full copies | still a whole copy per change; caps only the largest (59 KB) | part of the 62.6% below |
| Task texts | moved about one for one; in 4 KB blocks, more disk (23 MB for 18.4 MB of text over 1 KB) | plans 58%; commands, reports and instructions nothing | none | part of the 62.6% below |
| The log file | 6.3 MB plus 18.4 MB of blobs (texts over 1 KB moved) | 14.46 MB (knowledge only), from 27.78 MB | | data 62.6% of today's |
| Reading | a file opened per text: 2,173 of them | replay applies each patch to the version before | none | a gunzip per text, at every replay |
| New code | references, fetching, collection rules | one patch shape and its apply; the knowledge history rebuilt | a check when learning | encoding per field; rows no longer readable in `sqlite3` |

**Pick: (b), for knowledge only, as one hunk.** A changed knowledge file
is stored as what it keeps of the version before and what it puts in
between:

```json
{"name": "engineer", "head": 9120, "tail": 0, "middle": "<the new lines>", "sha256": "<of the whole new text>"}
```

The new text is `old[:head] + middle + old[len(old)-tail:]`. One hunk
measured as small as a line diff (2.7% against 2.8%), needs no diff
library (Go's standard library has none) and no LCS, and applies with one
slice. The writer sends a full copy, the old shape `{"name", "knowledge"}`,
for an agent's first version and whenever the middles since its last full
copy add up to more than the text. Measured with line diffs, that keeps
0.62 MB of 13.18 MB (4.7%) and bounds a rebuild to the text plus as much
again (118 KB at most); one hunk is within a few percent of the line
diffs, so the same holds.

- **Who writes it:** `hooks.recordWhatWasLearned` already has the old
  knowledge (from the roster) and the new; it works out `head` and `tail`
  and proposes.
- **The check:** the rules, under the store's lock, apply the patch to
  the roster's current knowledge and refuse it if the result's hash is not
  `sha256`. A human editing the file on the page between the hook's read
  and its proposal makes the hook's patch fail, and the hook sends a full
  copy instead.
- **Apply:** the fold checks only the bounds (`head + tail` within the
  text, every field present), never the hash: replay is deterministic, so
  what the rules accepted fits again, and hashing every version made a fold
  of the `agents` stream take 75 ms instead of 0.12 ms. A patch out of
  bounds can then only be a bug. It panics, and the `agents` stream is
  broken as any panicking apply is (`guard` in `current`); the real-copy
  replay test catches it before a release.
- **Plans, commands, reports and instructions stay inline.** They are task
  texts: archiving takes them out of the live log and gzips them, which
  beats anything here. Plans would keep 58% as diffs, the rest 100%.

Why not the others: (a) moves bytes rather than removing them, gives
almost every event a reference that collection must honour, and opens a
file for every turn and action read; the content store is used for one
thing in this design, the archive (section 4). (d) keeps 62.6%, worse than
(b) on knowledge by a factor of ten, and makes the rows opaque. (c) is a
question about briefs, not disk (below).

### Old events keep replaying

No event is ever rewritten for this. The shape says which kind an
`agent.learned` is: with `knowledge`, a full copy (every event written so
far); with `middle`, a patch. `agents.FromEvents` and the roster's `apply`
handle both, and so does `web.knowledgeHistory`
(`internal/web/agents_api.go`), which today lists each event's
`knowledge` and would rebuild each version walking forward from the
stream's start (461 `agent.learned` events today). The catalogue entry
(`internal/events/catalogue.go`) needs only `name` already, so the patch
shape passes the shape check without a new type. No version number is
needed while the shapes differ by field.

### Blobs are never collected while an event names them

Nothing deletes from the content store today: `sharedfiles.Disk`
(`internal/sharedfiles/blobs.go`) has `Put`, `Has` and `Open`, no delete.
The design keeps it that way as a rule: a blob goes only through the
privacy path (section 5), which first removes or rewrites, under the
store's lock, every event that names it. If collection is ever added, it
marks every hash named by a live event, by an archive marker and by the
events inside each archive blob (`file.added` carries hashes,
`internal/tasks/files.go`), and sweeps only files older than a day, so a
blob put for an event not yet committed survives.

The archive (section 4) needs the blob on disk before the event that
names it commits. `Disk.Put` renames into place but does not fsync; the
archiver needs a put that syncs the file and its folder first.

### What bounds the agents stream

It belongs to no task, so archiving can't touch it, and a snapshot only
stops it being replayed at each start. Its growth is knowledge, and (b)
takes that from 13.18 MB in 2.9 days, and rising with each file's length,
to 0.62 MB: about 0.2 MB a day, 80 MB a year, growing with how much the
agents write, not with how long their files are. The full-replay fallback
reads it once: about 0.6 s for a year's worth at 7.8 s per GB.

A bound on the file itself, (c), is a separate choice: the largest is
59 KB, about 15,000 tokens in every brief that agent gets. With diffs it
no longer matters for disk, so it is left to the user (open question):
for example, telling the agent in its brief when its file passes 32 KB.

### The saving on the real data

On the later copy:

| | Live log file | Archive blobs |
|---|---|---|
| As it is | 27.78 MB | |
| Knowledge as one hunk per version | 14.46 MB (52%) | |
| And done tasks archived (section 4) | 2.64 MB (9.5%) | 2.28 MB |

A busy day falls from 10 to 12 MB to about 4.5 to 5 MB (the 42% that is
not knowledge), and that part leaves the live log once its tasks are
archived, at 22% of its size.

## 4. Archiving finished tasks

| | (a) A `task.archived` event, and the task's rows moved out | (b) The log rotated into time segments | (c) One file: a summary event, the rows stay |
|---|---|---|---|
| The live log | open and recent work, plus one marker per archived task | the current segment; but an open task's early events sit in old segments | everything, for ever |
| Startup without a snapshot | replays the live log | replays every segment that holds an open task's events, so in practice all | must find the markers first, then skip those streams |
| Memory | a summary per archived task | every task, unless summarised as well | a summary per archived task |
| Disk | archived rows gzipped to 22% | old segments could be compressed, but none can be dropped while it holds any open task's event | nothing saved |
| Append-only | one delete, allowed only by the marker, in the trigger itself | no deletes; positions split across files | unchanged |
| An archived task's history | one blob | a query across segments | its rows, by the index |
| Erasing a task (section 5) | delete one blob | rewrite a segment | rewrite rows |

**Pick: (a).** (c) is its first half: the marker and the summary alone
already bound memory and the work at startup; the move adds the bound on
disk. (b) bounds each file but not what startup reads, since open work
spans segments.

Where the moved rows go:

| | A second SQLite file, `archive.db` | One gzipped blob per task, in the content store |
|---|---|---|
| Size | the rows one for one (10.19 MB for the 18 done tasks) | 2.28 MB (22.3%) |
| Reading one task | by index | gunzip one blob: 17 ms for the largest |
| Checked by | counting rows | its hash, which is its name |
| Erasing a task | a delete there too, with its own triggers | delete one file |
| Kept with | a second database to migrate, back up and vacuum | the shared files, already backed up with the data folder |

**Pick: a blob per task**, in the same store as the shared files
(`sharedfiles.Disk`, `<data folder>/files`, `cmd/agentry/serve.go`): the
task's rows up to the marker, rejections included, as JSON lines
(position, time, actor, type, correlation, data), gzipped.

### How it fits "the log is the truth"

The archive is part of the log. The marker, appended to the task's own
stream, says exactly where its rows went:

```json
{"through": 21877, "count": 7726, "archive": "<sha256 of the blob>", "created": 112, "summary": {"title": "...", "status": "done", "...": "..."}}
```

Rows keep their positions inside the blob. The schema's `AUTOINCREMENT`
(`internal/store/store.go`) means SQLite never hands out a deleted
position again, so a position names one event for ever. A full read of a
stream is the blob's rows up to `through` plus the live rows after; no
event is lost, only moved.

The delete trigger stays in the database, and allows exactly one thing:
deleting rows that a marker covers, unless the task has been unarchived
since.

```sql
CREATE TRIGGER events_no_delete BEFORE DELETE ON events
WHEN NOT EXISTS (
	SELECT 1 FROM events m
	WHERE m.stream = OLD.stream AND m.type = 'task.archived'
	  AND OLD.position <= json_extract(m.data, '$.through')
	  AND NOT EXISTS (SELECT 1 FROM events u WHERE u.stream = m.stream
	                  AND u.type = 'task.unarchived' AND u.position > m.position))
BEGIN SELECT RAISE(ABORT, 'events are append-only'); END;
```

So "append-only" still does not depend on the code remembering: a stray
`DELETE` is refused by SQLite as today. Changing the trigger is the
schema's first migration: `Open` drops and recreates it in one
transaction and records `PRAGMA user_version = 1`. An older Agentry
opening the database keeps the new trigger (`CREATE TRIGGER IF NOT
EXISTS`), ignores the unknown marker in its replay, and shows archived
tasks as gone, since their `task.created` rows are. It also counts only
`task.created` rows for the next id, so it could give a new task an
archived task's id: going back to an older release after archiving is not
supported, and the release notes say so.

Every reader that listed tasks by their `task.created` rows now has to
see archived tasks too, or the next id collides with one. `Store.made`
(`internal/store/store.go`), which `Store.Create` calls to name the next
task, is that reader. Today it counts `task.created` rows; it must count
what this lists, `task.created` rows and `task.archived` markers
together, each at the `task.created` position it stands for (the
marker's `created`), oldest first:

```sql
SELECT stream, position FROM events
WHERE type = 'task.created' AND position <= ?applied
UNION
SELECT stream, json_extract(data, '$.created') FROM events
WHERE type = 'task.archived' AND json_extract(data, '$.created') <= ?applied
ORDER BY 2
```

The marker half is gated on the `task.created` position it stands for,
not on its own: the archive transaction deletes that row outright, so
between its commit and `applied` moving past the marker, a gate on the
marker's position would hide the task from both halves, and a
`createTask` in that window would get a spurious 409 (the rules refuse
it, so ids never collide, but the user sees the error).

`UNION` drops a row that is the same in both halves, so a task appears
once whether it is archived, unarchived (its `task.created` row is back,
at the same position the marker names) or archived twice (two markers,
one `created`). The rules (`internal/rules/rules.go`) refuse a
`task.created` on a stream that holds a summary, as on one that holds a
task.

**Built in step 5.** `internal/store/store.go` keeps the schema's
`migrations` as a list: entry i takes a file at `user_version` i to i+1,
so the version is the list's length and nothing is bumped by hand. `Open`
runs the ones a file hasn't had and records the version in one
transaction; a new file starts at 0 and runs them all, and a file from a
newer build isn't opened. Migration 1 is the gated trigger above, plus a
partial index of the markers, `events_archived` on `(stream, position)
WHERE type = 'task.archived'`: the trigger runs once per deleted row, and
without the index each run scanned the stream, so deleting 8,000 rows took
4.6 s under `Store.mu`; with it, 29 ms.
`auto_vacuum = INCREMENTAL` is a pragma of the writing connection, so a
new file is incremental from the start; an existing one keeps its mode
until the one full `VACUUM` (below), which `Open` does not run. `made`
is the `UNION` above. `task.archived` (`through`, `count`, `archive`,
`created`, `summary`) and `task.unarchived` (`marker`, `archive`) are in
the catalogue, written by Agentry alone; `task.unarchived` refolds its
stream. The rules (`internal/rules/archive_rules.go`) let an archived task
take only `task.unarchived`, archive only a done task with a marker whose
hash and positions make sense, and refuse `task.unarchived` on a whole
task. The state folds the markers since step 6 (below, "Done tasks leave
the state"). Nothing writes a marker outside tests before step 7. Tests:
`TestAStrayDeleteIsRefused`, `TestAMarkerAllowsDeletingOnlyTheRowsItCovers`,
`TestUnarchivingEndsWhatTheMarkerAllows`, `TestAVersionZeroLogMigratesOnce`,
`TestANewLogStartsAtTheLatestSchema`, `TestALogFromANewerBuildIsNotOpened`
and `TestCreateCountsArchivedTasksOnce` in `internal/store`;
`TestAnArchivedTaskTakesOnlyItsUnarchiving`, `TestOnlyADoneTaskIsArchived`
and `TestOnlyTheStoreWritesTheMarkers` in `internal/rules`.

### The move, step by step

An archiver goroutine (`internal/archive`) runs at startup and then
hourly, a task at a time. It changes the log only through one store
method, `Store.Archive`, which acts under `Store.mu` like `Propose`, so
no proposal, reopening or other archiving runs between its check and its
commit:

1. **Choose:** tasks done for at least the age (below), not archived yet,
   and not an epic with a story still in the state (below).
2. **Write the blob:** read the stream with `Log`, rejections included,
   up to `through`, its last position now. Encode, gzip (Go's gzip writes
   no time into its header, so the same rows always make the same bytes),
   put it in the store with the file and folder synced. No lock is held.
3. **Marker and delete, in one transaction under `Store.mu`:** the rules
   check the `task.archived` proposal: the task done for the age, the
   stream's last position in the database still `through` (anything since,
   and the round leaves it for the next), and the blob present
   (`Blobs.Has`). Then one transaction inserts the marker and runs
   `DELETE FROM events WHERE stream = ? AND position <= ?`. The trigger
   sees the marker, uncommitted, inside the same transaction, so it allows
   the delete. If SQLite's `changes()` is not `count`, the transaction
   rolls back and nothing happened. After the commit, the model applies
   the marker (the task becomes its summary) and `applied` moves, as for
   any proposal.
4. **Give the space back:** `PRAGMA incremental_vacuum` and a WAL
   checkpoint (below), and a new snapshot after the round.

A crash can only leave a blob that no event names (after step 2): the
next round makes the same bytes, and `Put` finds them there. A marker
never stands beside its rows, and rows never go without their marker.

### The age

A server option, `archive after`, 30 days by default; 0 turns archiving
off. It counts from the `task.done` event. `tasks.Task` has `IsDone` but
not when (`internal/tasks/apply.go`, the `task.done` case), so it gains
`DoneAt`, cleared on reopening. Nothing in the copy is even 3 days done
yet (section 1), so with the default the first task is archived a month
after it is done, and the Done column keeps a month of whole tasks.

### Opening an archived task's history

`GET /api/tasks/{id}` (`web.showTask`), and through it the MCP
`show_task` tool (`internal/mcp/tools.go`, which calls that endpoint):
when the state holds only a summary, the handler opens the marker's blob
(`Blobs.Open`), gunzips it (17 ms for the largest task), adds any live rows
up to the marker (only a crash leaves those), folds them with
`tasks.FromEvents`, and returns the same detail with `archived: true` and
when it was archived. A small cache of the last few tasks opened, keyed by
stream and marker, keeps the Events tab and repeated MCP calls from
gunzipping again. Nothing goes into `current.State`; the folded task is
dropped after the reply. Every other reader of a task's events goes
through one store function that does the same merge, so none reads the
live rows alone by mistake.

The page marks the task as archived; Reopen stays where it is and does the
next step first.

### Reopening an archived task

The rules refuse every event on an archived task except
`task.unarchived`. Reopening one is four steps:

1. **Read the blob:** open and gunzip it, with no lock held.
2. **Rows back and `task.unarchived`, in one transaction under
   `Store.mu`:** a store method, `Store.Unarchive`, acts like
   `Store.Archive`. The rules check the proposal (the stream's last event
   is still the marker that names this blob). Then one transaction
   inserts the blob's rows into `events` with their own positions, which
   are free (`AUTOINCREMENT` never reused them), checks that it inserted
   `count` of them, and inserts `task.unarchived`, naming the blob. If the
   count is wrong, it rolls back and nothing happened. Since the archiver
   takes the same lock, it can never delete rows that were just restored:
   by the time it can look, `task.unarchived` is there and the trigger
   refuses the delete.
3. **The state folds the stream again.** After the commit, still under
   `Store.mu`, the store reads the stream's accepted events with a
   position below the `task.unarchived` (the restored rows and the
   marker) and hands them to the model with `Refold` ("What task-21
should leave room for", item 9). It never
   reads the stream's current rows: at `Open` those hold the rest of the
   tail too. One task's replay, about 40 ms for the largest
   (`read-model.md`, task-8). Then `applied` moves, as for any proposal.
4. **`task.reopened`**, as today, as its own proposal.

How the fold reads the markers. `tasks.FromEvents` today returns nil for
`[marker]`, since there is no `task.created`, and `current.Load` would
drop the task. It changes in three ways:

- **`task.archived`:** the task folded so far is set aside and the fold
  goes on with none. A new `tasks.Archived(accepted)` returns the summary
  of the last marker that no `task.unarchived` follows, and
  `current.Load` (and `Refold`) keep that summary in `archived`, ordered
  by the marker's `created`, whenever `FromEvents` returns nil. So a
  stream that is only `[marker]` becomes its summary, not nothing.
- **`task.unarchived`:** the fold takes back the task it set aside at
  the marker this event reverses, then applies the event. That task was
  rebuilt from the restored events before the marker in the same slice,
  so nothing is read twice. If none was set aside (the rows were not in
  the slice), the fold panics and the stream is broken, as for any event
  that can't apply.
- **`current.Load`'s `created`** takes a task's position from its
  `task.created` row when the slice has one, and from the marker's
  `created` only when it does not, so an unarchived task is listed once.

Readers that follow the log by position (`After`, the coordinator's
`Reader`, `/api/changes`) never see the restored rows, whose positions
they passed long ago, only `task.unarchived`. That is right: they are not
new events. A task archived a second time gets a new blob with every row;
the first blob stays, named by the first marker, which is now among the
rows in the second.

### Epics and their stories

What crosses tasks (`internal/tasks/apply.go`): an epic's `Stories` and
`StoriesDone`, and each story's summary copied into the epic's own
`task.story_done` events; a story's `Epic` and `After`. The epic's page
reads its stories' states (`read-model.md` section 2).

- **A story is archived on its own age.** The epic's page lists it from
  its summary, which keeps its title, status and done summary.
- **An epic is archived only when it is done for the age and none of its
  stories is still in the state** (each is archived, or has been erased).
  The user may close an epic with stories still open
  (`closedByYouIfAnEpic`, `internal/rules/task_rules.go`), so an epic can
  wait longer than its own age: until its last story is done and archived.
- **Before or after:** each is its own stream with its own marker, so the
  order is free. An open story whose `After` names an archived story reads
  "done" from the summary.
- **Reopening a story of an archived epic** unarchives the epic first, in
  the same request: a reopened story's news goes to the epic's stream
  (`task.story_done`), which an archived epic would refuse.
- Across teams: a task waiting on another team's (`WaitingOn`) is open, so
  never archived; a done request whose asker is archived still posts
  `task.request_answered`, and that asker is unarchived first, as above.

### Done tasks leave the state, not just the disk

`read-model.md` put a task at about 60 KB of heap ("Risks and open
points"). Measured, the state is 9.5 MB for 24 tasks, about 400 KB each,
because it keeps every action's command and every turn's plan and report
(section 1). Kept for ever that is about 350 MB of heap per GB of file.

An archived task is in the state as a `tasks.Summary`, the fields
`web.summaryOf` (`internal/web/views.go`) reads today: id, title, repo,
status, kind, epic and story counts, labels, rank, budget and tokens,
created and last active, plus the done summary. About 630 bytes as JSON.
`current.State` keeps them in `archived map[string]tasks.Summary`;
`Task(id)` returns nil and `ErrArchived` for one; a new `Summaries()`
serves the list. Task ids stay in creation order and are never reused:
the marker's `created` is the `task.created` position the state orders by,
and the next id counts archived tasks too, through `Store.made`, which
`Store.Create` calls (section 4, "How it fits").

**Built in step 6.** `tasks.Replay` sets the task aside at a marker and
takes it back at `task.unarchived` (panicking, so the stream breaks, when
nothing was set aside), and `tasks.Archived` reads the last marker's
summary, its id always the stream's. `current.State` folds a marker into
`archived` on `Load`, `Apply` and `Refold`; a marker that is the first
event of its stream the state sees stands for the `task.created` that
went with the rows: it counts for `NextTask` and goes into `created` at
its `created` position, in order even in a snapshot's tail. `Summaries()`
lists whole tasks' `SummaryOf` and archived summaries in that order, and
`/api/tasks` reads it, so an archived task is listed as done with its
summary. `Equal` compares each summary with `tasks.Archived` of a replay,
and the snapshot carries the map; a snapshot from before this is another
build's, and one of the old shape under the same tag fails to decode, so
either way the log replays. The readers of item 6 already skipped an
archived task; their tests now archive through a real marker
(`storetest.Archive`). Measured on a thousand copies of the every-event
task, the state holds 818 bytes per archived task
(`TestTheSummaryHeapBenchRunsOnAFixture`; `TestStartupBench` logs the
same on the synthetic log). Tests: `TestAMarkerAloneFoldsToItsSummary`,
`TestASnapshotTakesAnUnarchivingInItsTail`,
`TestASnapshotFromBeforeSummariesReplays`,
`TestAnArchivedTaskIsCountedButNotHandedOut`,
`TestUnarchivingTakesBackTheWholeTask` and `TestEqualSeesAWrongSummary`
in `internal/current`; `TestAnArchivedTaskIsListedAsItsSummary` in
`internal/web`.

At scale: a task is about 1.2 MB of file today, so 1 GB of history is
about 900 tasks, and 100 GB about 90,000: some 60 MB of summaries in
memory and in the list's JSON. The list will then have to page archived
tasks (open question).

### VACUUM

A delete frees pages inside the file but does not shrink it; `VACUUM`
gives the space back, at about 5 s per GB, with an exclusive lock (no
proposals meanwhile) and free disk as big as what it copies (section 1).

| | Never | A full `VACUUM` now and then | `auto_vacuum = INCREMENTAL` |
|---|---|---|---|
| File size | stays at its high-water mark; new events reuse the freed pages, so it stops growing once archiving keeps pace | shrinks to the live data | shrinks after each round |
| Cost | none | about 5 s per GB of live data, everything waiting | `PRAGMA incremental_vacuum` after each round, in proportion to the pages freed |
| To switch | | | one full `VACUUM` after setting the pragma: SQLite changes the mode of an existing database only that way |

**Pick: incremental.** The release that adds archiving sets the pragma,
runs the first archiving round, and only then runs the one `VACUUM`:
`VACUUM` copies only the live rows, so it is cheapest once the done tasks
are gone. If the disk lacks room for the copy, it logs that and tries
again at the next start. After each round: `PRAGMA incremental_vacuum`,
then `PRAGMA wal_checkpoint(TRUNCATE)`, so the WAL does not keep the old
pages either. Step 5 sets the pragma on every open; the `VACUUM` is step
7's.

## 5. Deleting data for privacy

Two cases: removing a whole task (work for someone who asks for it gone,
or a mistake), and removing one secret pasted into a chat, a command or a
knowledge file.

A text can be in more places than its event: the live rows; the archive
blobs; the snapshot; the database's free pages and its WAL; copies in
other streams (a story's summary in its epic's `task.story_done`, a
request's in its asker's `task.request_answered`, a coordinator's words in
`task.assigned`, in `internal/tasks/apply.go`); the task's shared files;
its worktree and branch, and any remote they were pushed to; the
engine's own session files outside Agentry; and backups.

| | Redaction events and an allowed rewrite | Crypto-shredding | Deleting blobs |
|---|---|---|---|
| How | an event names the rows; the triggers allow rewriting or deleting exactly those | every text encrypted with a key per task (or per text) in a key table; erasing deletes the key | texts live in the content store; erasing deletes the blob, and events keep a hash that leads nowhere |
| Reaches | anything in the database and the archive | anything encrypted, and backups too if the keys are kept apart from them | only texts in blobs: the archives, not the short commands where secrets usually are |
| Cost | the gated triggers, a rewrite path, `secure_delete` | encrypting and decrypting on every read and replay; gzip and repeats gain nothing on encrypted text; a key file whose loss loses the log | nothing more than the store |

**Pick: redaction events with an allowed rewrite** for rows, and deleting
blobs for archives. Crypto-shredding is the only way to reach backups,
but for one user's own machine its key handling costs more than it buys,
and a lost key file would lose the log.

So yes, deletion is possible under append-only, in the same way as
archiving: the database allows exactly what an event in the log names,
and the log records that it happened (who, when, why, which positions),
never what was removed.

### Removing a whole task

1. Stop its turn if one is running.
2. Propose `task.erased` (who, why, `through`, the archive blobs). It is
   the stream's last accepted event: the rules refuse anything after it.
   The state keeps a stub, so the id is never reused and the list can say
   "task-12 was erased on 3 November".
3. Delete every row of the stream up to `through`. The delete trigger
   gets a second allowed case, a `task.erased` marker, which nothing
   reverses. Run it with `PRAGMA secure_delete = ON`, so the freed pages
   are zeroed, then checkpoint the WAL with `TRUNCATE` and run
   `incremental_vacuum`.
4. Delete its archive blobs, and the blobs of its shared files that no
   other task's event names.
5. Redact the copies in other streams: the epic's `task.story_done`, the
   asker's `task.request_answered` (below).
6. Write a new snapshot at once, and delete `state.snapshot.bad` if there
   is one.
7. Offer to remove its worktree and branch; Agentry can't reach a remote
   it was pushed to.

### Removing a secret

The user marks the text on the page: a command, a message, a report. The
server finds the rows of the task (or the whole log, for a secret pasted
widely) whose `data` contains it, and shows the user the count first.

1. Propose `event.redacted` on the same stream, with the positions, the
   fields and the length removed. Never the text, and never its hash: a
   short secret's hash can be guessed.
2. Rewrite each named row in one transaction, the secret replaced by
   `[redacted]` and the rest of the text kept. The update trigger allows
   an `UPDATE` only of `data`, only of a row that an `event.redacted` later
   in the same stream names:

   ```sql
   CREATE TRIGGER events_no_update BEFORE UPDATE ON events
   WHEN NEW.position IS NOT OLD.position OR NEW.at IS NOT OLD.at
     OR NEW.stream IS NOT OLD.stream OR NEW.type IS NOT OLD.type
     OR NEW.actor_kind IS NOT OLD.actor_kind OR NEW.actor_id IS NOT OLD.actor_id
     OR NEW.correlation IS NOT OLD.correlation
     OR NOT EXISTS (SELECT 1 FROM events r, json_each(r.data, '$.positions') p
                    WHERE r.stream = OLD.stream AND r.type = 'event.redacted'
                      AND r.position > OLD.position AND p.value = OLD.position)
   BEGIN SELECT RAISE(ABORT, 'events are append-only'); END;
   ```

   With `secure_delete` on, then a WAL checkpoint with `TRUNCATE`, since
   the old page is in the WAL too.
3. Steps 1 and 2 are one transaction under `Store.mu`, by a store method
   `Store.Redact` that acts like `Store.Archive`. After the commit the
   store folds the stream again as for `task.unarchived` (section 4): it
   hands `Refold` the stream's accepted events with a position below the
   `event.redacted`, which are the rewritten rows, never the stream's
   current rows. A new snapshot is written at once.
4. **A secret in a knowledge file** is in every version after it was
   written. The path first proposes a full copy of the current file
   without it, then rewrites each earlier version that holds it as a full
   copy of its redacted text, from the first such version up to that new
   one. A patch's `head` and `tail` would be wrong against a redacted
   base; full copies leave nothing to mismatch, and versions before the
   secret are untouched.
5. **A secret in an archived task:** unarchive it, redact, archive it
   again (a new blob), then delete the old blob. The `event.redacted` names
   the deleted blob, so a reader that meets the old marker knows the blob
   is gone on purpose.

### Snapshots, archives, backups and worktrees

- **Snapshots** are rewritten at once after any erasure or redaction.
  The old file's blocks may stay on the disk until reused; a filesystem
  can't promise more.
- **Archives** are handled above: an erased task's blobs are deleted, a
  redacted one is re-archived.
- **Backups** of `agentry.db` and the data folder are out of Agentry's
  reach. The page says so after an erasure, with the time, so the user
  knows which backups still hold it.
- **Worktrees, branches and remotes:** the text may be in files an agent
  wrote, or in commits. Agentry offers to remove the worktree and the
  local branch; pushed commits are the user's to rewrite.
- **The engine's own records**, such as a session's transcript, live
  outside Agentry; the page names them, and does not delete them.

## 6. Projections

### Assumptions

- **History** is counted in GB of database file as today's code writes
  it: 880,000 events per GB (section 1). At 4 to 7 GB a year of busy use,
  1 GB is 7 to 13 weeks, 10 GB is 1.4 to 2.5 years and 100 GB is 14 to 25
  years. Knowledge files keep lengthening, so today's format fills a GB
  faster each month: these are the longest those sizes last.
- **The working set, W,** is what archiving leaves whole in the state: 20
  open tasks and 30 busy days of done tasks (the default age, section 4).
  A busy day is 11 MB and 9,700 events, 95.5% of them on task streams, and
  the copy's open tasks have about 700 events each. So W is
  30 × 9,700 × 0.955 + 20 × 700, about **292,000 events**, and
  30 × 11 MB × 0.50 + 20 × 0.3 MB, about **0.17 GB** of file.
- **What a GB holds:** knowledge 0.50 GB (27.78 MB fell to 14.46 MB with
  0.62 MB kept, section 3), which one hunk per version keeps at 4.7%, so
  0.024 GB; task streams and the rest 0.50 GB. Never archived: the
  `agents` stream's 19,400 events, the `workers` stream's 20,200 events
  and 3.4 MB, and about 900 markers (0.7 MB): 40,500 events and 0.004 GB.
- **Rates** (section 1): reading and decoding 5.85 s per GB of file read
  (75% of 7.8 s), folding 2.2 µs per event (25% of 8.9 µs); 400 B of heap
  and 440 B of JSON snapshot per event in the state (10.59 MB for 24,180
  events); JSON decode 2.8 ms per MB (29.9 ms for 10.59 MB), encode 1.8 ms
  per MB, atomic write 0.34 ms per MB; 630 B per summary and 900 tasks per
  GB, so 0.57 MB of summaries per GB; archive blobs 0.193 bytes per byte of
  file archived (2.28 MB for the 11.82 MB the rows took); and, with a
  snapshot, a fixed 0.16 s: 70 ms hashing the binary and at most 90 ms of
  tail.

### Formulas

H is history in GB.

- **(a) Nothing done:** start = 7.8 s × H; heap = 400 B × 880,000 × H = 352 MB × H; live = H.
- **(b) Snapshots only:** start = 0.16 s + 440 B × 880,000 × H × 2.8 ms/MB = 0.16 s + 1.08 s × H; after an upgrade, (a)'s start; snapshot = 0.39 GB × H; heap and live as (a).
- **(c) Slimmer knowledge only:** live = (0.024 + 0.50) × H; start = live × 5.85 s + 880,000 × H × 2.2 µs = 5.0 s × H; heap as (a).
- **(d) Archiving only:** live = (0.50 + 0.004) × H + 0.17 GB; start = live × 5.85 s + (40,500 × H + 292,000) × 2.2 µs; heap = 292,000 × 400 B + 0.57 MB × H + 0.2 MB of roster; archive = (0.50 × H − 0.17 GB) × 0.193.
- **(e) All three:** live = (0.024 + 0.004) × H + 0.17 GB; snapshot = 292,000 × 440 B + 0.57 MB × H + 0.2 MB; start = 0.16 s + snapshot × 2.8 ms/MB; after an upgrade = live × 5.85 s + (40,500 × H + 292,000) × 2.2 µs; heap and archive as (d).
- **(e) on 8 cores:** start = 0.16 s + snapshot × 2.8 ms/MB / 8 + 11 ms, the largest task (4 MB of JSON) decoded alone. An estimate: parallel decoding is not measured.

### The table

Each cell is 1 GB / 10 GB / 100 GB of history, at the working set W.

| | Startup | After an upgrade or a bad snapshot | Heap held by the state | Live database | Archive blobs | Snapshot |
|---|---|---|---|---|---|---|
| (a) Nothing done (the read model alone) | 7.8 / 78 / 780 s | the same | 0.35 / 3.5 / 35 GB | 1 / 10 / 100 GB | | |
| (b) Snapshots only | 1.2 / 11 / 109 s | 7.8 / 78 / 780 s | 0.35 / 3.5 / 35 GB | 1 / 10 / 100 GB | | 0.39 / 3.9 / 39 GB |
| (c) Slimmer knowledge only | 5.0 / 50 / 500 s | the same | 0.35 / 3.5 / 35 GB | 0.52 / 5.2 / 52 GB | | |
| (d) Archiving only | 4.7 / 32 / 306 s | the same | 118 / 123 / 174 MB | 0.67 / 5.2 / 51 GB | 0.06 / 0.93 / 9.6 GB | |
| (e) All three | 0.52 / 0.54 / 0.68 s | 1.9 / 4.2 / 27 s | 118 / 123 / 174 MB | 0.20 / 0.45 / 3.0 GB | 0.06 / 0.93 / 9.6 GB | 129 / 134 / 185 MB |
| (e), the body decoded on 8 cores (estimate; one core is row (e)) | **0.22 / 0.22 / 0.24 s** | 1.9 / 4.2 / 27 s | as (e) | as (e) | as (e) | as (e) |

What it says:

- **No piece alone bounds startup.** Snapshots save a state that holds
  every task (0.39 GB of snapshot per GB); slimming halves the bytes but
  not the events; archiving leaves knowledge in the live log in full.
  Together they leave startup to the working set: 0.16 s fixed, the
  working set's decode, and 0.57 MB of summaries per GB, the one part that
  still grows (0.16 s more at 100 GB on one core, 20 ms on eight, an
  estimate).
- **One core of JSON misses 0.5 s.** The working set alone is 128 MB of
  snapshot and 0.36 s to decode. Decoding the tasks in parallel brings it
  to about 60 ms, so startup is 0.22 to 0.24 s on 8 cores (estimate; one
  core: 0.52 to 0.68 s). Parallel decoding is not measured: build step 2
  measures it, and [section 2's rule for leaving JSON](#2-snapshots-of-the-read-model)
  decides whether gob replaces it. Section 7 picks the parallel decode.
- **Every upgrade replays the live log in full**, since the binary's hash
  changes. In (e) that is the working set (1.6 s) plus the streams nothing
  archives: `agents` (slimmed knowledge, 24 MB per GB) and `workers`
  (20,200 events per GB). It passes 5 s at about 13 GB, 2 to 3.5 years of
  busy use. Section 7 defers the fix.
- **Heap** in (d) and (e) is the working set, 117 MB of it; summaries add
  0.57 MB per GB.
- **Disk** in (e) grows 28 MB per GB of history in the live file, plus
  0.1 GB of archive blobs per GB: 100 GB of history becomes 3.0 GB live
  and 9.6 GB archived, 12.6% of it.
- **A snapshot write** at W is 128 MB × (1.8 + 0.34) ms/MB, about 0.28 s
  of CPU, at most once per 10 minutes of activity; 0.4 s at 100 GB.

### If the working set doubles

40 open tasks and 60 busy days, or 30 days at twice today's rate: about
584,000 events and 0.34 GB.

| | Startup | After an upgrade | Heap | Live database | Snapshot |
|---|---|---|---|---|---|
| (d) Archiving only | 6.3 / 34 / 307 s | the same | 234 / 239 / 290 MB | 0.84 / 5.4 / 51 GB | |
| (e) All three, one core | 0.88 / 0.90 / 1.04 s | 3.5 / 5.8 / 29 s | 234 / 239 / 290 MB | 0.37 / 0.62 / 3.1 GB | 258 / 263 / 314 MB |
| (e), 8 cores (estimate) | 0.26 / 0.26 / 0.28 s | 3.5 / 5.8 / 29 s | as above | as above | as above |

What depends on the working set doubles, and nothing that depends on
history moves; (a) to (c) don't depend on it at all. The snapshot write
doubles too, to 0.55 s. The archive age is the knob: 7 days in place of 30
cuts the working set, and its heap and decode, by nearly four.

### Targets

Numbers the build must meet, measured at the working set W unless a row
says otherwise. Each is checked by a benchmark that is skipped unless
`AGENTRY_BENCH_DB` names a read-only copy of a real database (section 1);
the benchmark copies it again into its temporary folder and never writes
to it. `TestStartupBench` (build step 2) grows the copy into a synthetic
log of `AGENTRY_BENCH_GB` GB of history (1 by default) with the working set
W, or twice W with `AGENTRY_BENCH_WORKING=2`.

| # | Target | Projected | How it is measured |
|---|---|---|---|
| 1 | Startup with a valid snapshot **≤ 0.5 s** with `GOMAXPROCS >= 2`, and **≤ 0.75 s** on one core, for any history up to 100 GB | 0.22 to 0.24 s on 8 cores (estimate; one core: 0.52 to 0.68 s) | `TestStartupBench` at 1 and 10 GB: `current.Open` to `Loaded`, min of 3; the 100 GB term by adding 90,000 summaries |
| 2 | Startup after an upgrade or a bad snapshot (full replay of the live log) **≤ 2.5 s** at 1 GB and **≤ 5 s** at 10 GB, with its reason logged | 1.9 s, 4.2 s | the same, with the snapshot deleted |
| 3 | The snapshot is never more than **10,000 events or 10 minutes** behind; its tail replays in **≤ 0.1 s** | 90 ms | a test with a fake clock; the benchmark with a 10,000-event tail |
| 4 | Live database **≤ 1 MB per open task + 6 MB per busy day of done work kept + 35 MB per GB of history**: ≤ 235 MB at 1 GB | 198 MB | the benchmark archives its synthetic log at the default age, runs `incremental_vacuum`, and reads the file's size |
| 5 | Heap held by the state **≤ 450 B per event in the state + 1 KB per archived task**: ≤ 135 MB at 1 GB | 118 MB | `runtime.ReadMemStats` after `runtime.GC`, the state alive and the events dropped, as section 1 measured |
| 6 | A snapshot write **≤ 0.5 s of CPU**, never on the request path and never under `Store.mu` | 0.28 s | the benchmark; a test that commits a proposal while the writer is held mid-write |
| 7 | An archived task's history opens in **≤ 100 ms** for the largest task, **≤ 10 ms** from the cache | about 60 ms (17 ms gunzip, 40 ms fold) | `GET /api/tasks/task-8` on the archived copy, a benchmark in `internal/web` |
| 8 | An archive blob **≤ 25%** of its rows' JSON | 22.3% | the archiver's benchmark on the copy |
| 9 | Knowledge stored **≤ 8%** of its full copies; rebuilding any version reads **≤ 2.5 times** its text | 4.7%; 118 KB for the 59 KB file. Built: 7.47% and 2.02 times with a full copy past 1x the text; at about 61 KB per file one hunk's middle is wider than the 446 versions measured here showed, and no threshold met 6% (1.25x: 6.89%/2.25; 1.5x: 6.47%/2.47; 2x: 6.14%/3.00; never: 4.99%/31.4) | build step 1's test on the copy |
| 10 | `Store.mu` held **≤ 200 ms** per task archived or unarchived, the largest included | not measured; roughly: unarchive holds it for about 40 ms of `Refold` plus the insert of about 4.8 MB of rows, archive for a delete through `events_by_stream`; step 7 measures it | a benchmark of `Store.Archive` and `Store.Unarchive` on `task-8` |

Targets 1, 2, 4 and 5 grow only with the working set and, slowly, with
history (summaries, the `agents` and `workers` streams); the rest don't
depend on history at all.

## 7. Recommendation

Build all three, in this order: slimmer knowledge, snapshots, archiving.
Then privacy, on the archive's triggers, when a user needs it.

**Slimmer knowledge first.** It is the one saving that can't be had
later. Knowledge is half of every GB, it sits in the `agents` stream that
archiving can't reach, and an append-only log keeps every full copy
written before the change for ever. Today that is about 6 MB a day, more
each day as the files lengthen. It is also the smallest piece (one patch
shape, the hook, a rules check, the history page) and needs nothing from
task-21.

**Snapshots second.** Alone they help little at scale, (b): 1.1 s and
0.39 GB of snapshot per GB. But they are what makes startup independent
of history once archiving bounds the state, and they need only task-21's
state. The projections add one thing to section 2: the body's `tasks`
and `archived` arrays are written one entry to a line (still one JSON
value, since JSON allows a newline between elements; at build step 2 it
gave way to gob chunks, see section 2), and the loader
splits on newlines and decodes the entries on up to 8 goroutines: 0.22
to 0.24 s on 8 cores (estimate; one core: 0.52 to 0.68 s). JSON keeps
`nil` and empty apart for the equivalence test. It gave way to gob at
build step 2, by
[section 2's rule for leaving JSON](#2-snapshots-of-the-read-model).

**Archiving third.** The biggest piece: a schema migration, store methods
that delete and restore rows, summaries in the state, the archived view
and reopening. It is what bounds heap, disk and the full replay. It comes
after snapshots because each round ends with a snapshot of the smaller
state, and the snapshot's anchor check is what notices archived rows.

**Privacy last.** Erasing a task reuses the archive's gated delete
trigger and `Refold`; redaction adds the update trigger. Neither changes
size or startup.

| | Saves | Costs | Decision |
|---|---|---|---|
| Knowledge as one hunk | 95% of knowledge, half of each GB | one patch shape; no task-21 dependency | build, step 1 |
| Snapshots, JSON decoded in parallel | startup down to the working set | saved forms, a writer, a loader, the fallbacks | build, steps 3 and 4 |
| Archiving behind a gated delete | heap, disk and the full replay down to the working set | a migration, two store methods, summaries, reopening | build, steps 5 to 9 |
| Erasing and redacting | privacy | two gated triggers, a rewrite path | build, step 10 |
| gob snapshots over saved forms with a presence flag per slice or map | decodes 6 times faster than JSON | plain gob merges `nil` and empty, so the forms must record which for `Equal` to judge a round trip | the fallback: chosen at build step 2 by [section 2's rule for leaving JSON](#2-snapshots-of-the-read-model) (2 goroutines on a 100 GB-sized body, 0.34 s) |
| Checkpoints of `agents` and `workers` | the replay after an upgrade at 100 GB, from 27 s to about 2 s | a checkpoint event, and archiving two more streams before it | deferred: when Target 2 fails, at about 13 GB |
| Paging archived tasks in the list | the list's JSON (57 MB at 100 GB) | API and page paging | deferred: at about 10,000 archived tasks, 11 GB |
| Blob collection | nothing yet: only a crash leaves a blob unnamed | marking and sweeping | deferred |
| A bound on a knowledge file's length | brief size, not disk | a check when learning | the user's call (open question) |
| Plans as diffs | 42% of plans, 3% of the data | a second patch shape | dropped: archiving gzips plans to 22% |
| Hash references for commands, reports and instructions | nothing: bytes move, they don't go | references, collection rules, a file per read | dropped |
| Compression inside the rows | 37% of the data | opaque rows, a gunzip per text at every replay | dropped |
| Log segments | each file's size | startup still reads every segment with open work | dropped |
| Crypto-shredding | reaches backups | keys, decryption on every read, a key file whose loss loses the log | dropped |

## 8. Build plan

Ten steps, each one reviewable change that leaves the server working and
carries its own tests. Step 1 needs nothing from task-21; steps 2 to 10
need task-21's read model (`internal/current`, the store's model) merged,
with the items in "What task-21 should leave room for" below. Benchmarks
run with `AGENTRY_BENCH_DB` pointing at a `VACUUM INTO` copy (section 1,
"Rerun the measurements"), never the live file. The Backlog story copied
from this plan must link [docs/design/log-growth.md](docs/design/log-growth.md),
since the steps cite section 2's equivalence tests and the task-21 items.

1. **Knowledge as one hunk per version.** No task-21 dependency. It edits
   the same roster `apply` in `internal/agents/agent.go` and
   `internal/rules/rules.go` that task-21 rewrites; it doesn't depend on
   task-21, but whichever lands second rebases.
   - Files: `internal/agents` (a `Patch` with `head`, `tail`, `middle`,
     `sha256`; `Diff(old, new)` and `Apply(old)`; the roster's apply and
     `FromEvents` take both shapes), `internal/hooks/handler.go`
     (`recordWhatWasLearned` proposes a patch, and a full copy for an
     agent's first version, when the middles since its last full copy
     outgrow the text, or when the patch is refused), `internal/rules`
     (refuse a patch whose result's hash isn't `sha256`),
     `internal/web/agents_api.go` (`knowledgeHistory` rebuilds each
     version walking forward).
   - Proves: `Apply(old, Diff(old, new)) == new` on 10,000 random edits;
     a stale patch is refused and the hook sends a full copy; today's
     full-copy events replay unchanged; the history page lists the same
     versions either way; on the copy, every knowledge version rewritten
     as patches rebuilds byte for byte.
   - Target 9.
2. **The startup benchmark.** Depends on task-21.
   - Files: `internal/storetest/synthetic.go` (grows the copy into a
     synthetic log: its task streams repeated under new ids and later
     times, its knowledge extended, to `AGENTRY_BENCH_GB` of history and
     the working set W or 2W), `internal/current/startup_bench_test.go`.
   - Proves: the synthetic log has the copy's mix (events per GB and bytes
     by type within 5%); prints today's startup and heap, case (a).
   - Also times the one-entry-per-line JSON decode of a 100 GB-sized
     body (the working set plus 90,000 archived summaries, about 186 MB)
     on 1, 2 and 8 goroutines, using section 1's throwaway mirror structs,
     and applies
     [section 2's rule for leaving JSON](#2-snapshots-of-the-read-model):
     over 0.34 s on 2 goroutines means gob, chosen here before step 4 is
     built (projected: about 0.43 s of startup, so JSON passes; measured:
     JSON 0.43 s on 2 goroutines, gob 0.10 s, so gob; see section 2).
   - Measures Targets 1 to 5 from here on; none passes yet.
3. **Saved forms.** Depends on task-21; skip whatever task-21 already
   built (its item 1).
   - Files: `internal/tasks` (`Saved`, `Save`, `Restore`), the same in
     `internal/agents`, `internal/workers`, `internal/connections`.
   - Proves: a reflection test per package fails if any field, exported or
     not, has no counterpart; a value with every field set (`fill`) round
     trips `reflect.DeepEqual`.
4. **Snapshots.** Depends on task-21 (items 2 and 3) and step 3.
   - Files: `internal/current/snapshot.go` (a JSON header line, then the
     body as length-prefixed gob chunks written by `internal/exactgob`, the
     tasks 16 to a chunk; parallel decode), `internal/current/resume.go`
     (`Restore` with every fallback, `Resume`, the atomic write; the
     writer goroutine), `internal/store` (`Resumer`, which `Open` hands a
     `Past` to check the snapshot's anchor and apply the events after it;
     `Hash`, a read of one row's hash for the anchor),
     `cmd/agentry/serve.go` (`state.snapshot` in the data folder, leftover
     temporary files deleted, a write at clean shutdown).
   - Proves: section 2's equivalence tests 1 to 5 (every cut of every
     fixture log, the real copy, every fallback, the sabotages, the forms);
     a proposal commits while the writer is held mid-write.
   - Targets 1 (on a log that is only the working set), 3 and 6.
5. **Schema version 1.** Depends on task-21.
   - Files: `internal/store/store.go` (the migration in one transaction:
     the gated delete trigger, `auto_vacuum = INCREMENTAL`,
     `user_version = 1`, `made` counts markers too), `internal/events/catalogue.go` (`task.archived`,
     `task.unarchived`), `internal/rules` (on an archived task, only
     `task.unarchived`; no `task.created` on a stream with a summary).
   - Proves: a stray `DELETE` is refused; a delete a marker covers is
     allowed in the marker's transaction, and refused once
     `task.unarchived` follows; the migration runs once; a version 0 file
     opens and migrates; `made` counts a task once when archived,
     unarchived and archived twice.
6. **Summaries in the state.** Depends on task-21 (items 4 to 8 and 10).
   - Files: `internal/tasks` (`Summary` from `web.summaryOf`, `DoneAt`,
     `Archived`, `FromEvents` on the markers), `internal/current` (the
     `archived` map, `ErrArchived`, `Summaries()`, `Load` and `Refold`
     keep summaries in `created` order, `Equal` compares them, the
     snapshot carries them), `internal/web` (the list reads summaries),
     and the readers of task-21's item 6 (`watchdog`, `hooks/orphans.go`,
     `coordinator/loop.go`, the agents, questions and workers APIs).
   - Proves: fixture streams of a marker alone fold to their summary,
     `Equal` to a replay and through a snapshot; the next id counts
     archived tasks; each reader skips an archived task without reading
     its stream.
   - Target 5's per-summary term.
7. **The archiver.** Depends on steps 4, 5 and 6.
   - Files: `internal/sharedfiles/blobs.go` (a put that syncs the file and
     its folder), `internal/store` (`Store.Archive`: under `Store.mu`, the
     rules' check, the marker and the delete in one transaction, rolled
     back unless `changes()` is `count`), `internal/archive` (new: at
     start and hourly, chooses by `DoneAt` and the age, epics after their
     stories, gzipped JSON-lines blobs, `incremental_vacuum` and
     `wal_checkpoint(TRUNCATE)`, a snapshot after each round),
     `cmd/agentry/serve.go` (`archive after`, 30 days by default, 0 for
     off; the one full `VACUUM` after the first round, retried at the next
     start if the disk lacks room).
   - Proves: after archiving, a full replay of blob plus live rows is
     `Equal` to the state before with the task as its summary; a proposal
     between the blob and the commit leaves the task for the next round; a
     crash after the blob makes the same bytes next time; a forced count
     mismatch (a test trigger) rolls back; an epic with an open story is
     not chosen; a snapshot whose anchor was archived falls back.
   - Targets 1, 2, 4 and 5 at 1 and 10 GB; Targets 8 and 10.
8. **Opening an archived task.** Depends on step 7.
   - Files: `internal/store` (one function for a stream's whole history:
     the blob's rows up to `through`, then the live rows; every reader of
     a task's events uses it), `internal/web` (`showTask` returns the
     detail with `archived: true` and when; a cache of the last few, keyed
     by stream and marker), `internal/mcp` (a test that `show_task` gets
     it), `ui/` (the task page marks it archived).
   - Proves: an archived task's detail equals its detail before archiving,
     but for `archived`; a second open is a cache hit.
   - Target 7.
9. **Unarchiving and reopening.** Depends on steps 7 and 8.
   - Files: `internal/store` (`Store.Unarchive`: under `Store.mu`, the rows
     back with their positions and `task.unarchived` in one transaction,
     the count checked, then `Refold` with the events below it),
     `internal/current` (`Refold`), `internal/tasks` (the fold of
     `task.unarchived`), `internal/web` (Reopen unarchives first; so does a
     story of an archived epic, and a `task.request_answered` to an
     archived asker), `internal/rules`.
   - Proves: archive then reopen gives the state of a log never archived,
     plus the new events; a restart with `task.unarchived` in the
     snapshot's tail; archiving twice and reopening; the archiver can't
     delete restored rows.
   - Targets 1, 2 and 10 still hold.
10. **Erasing a task and redacting a secret.** Depends on steps 5 and 9.
    - Files: `internal/store` (`Store.Erase`, `Store.Redact`, the update
      trigger and the delete trigger's second case, `secure_delete`),
      `internal/rules`, `internal/tasks` and `internal/agents`
      (`task.erased` stubs, `event.redacted` through `Refold`, knowledge
      rewritten as full copies), `internal/sharedfiles` (deleting a blob,
      on this path only), `internal/web` and `ui/` (marking a text, the
      count first, the note on backups).
    - Proves: after erasing, a planted string is in no row, WAL, blob or
      snapshot (searched in the files' bytes after the checkpoint); a
      redacted row keeps every other field; the update trigger refuses
      anything else; every knowledge version after the secret rebuilds
      without it; a redacted archived task is archived again and its old
      blob is gone.

Done when Targets 1 to 10 hold on the copy at 1 GB of synthetic history,
and Targets 1, 2, 4 and 5 at 10 GB.

## What task-21 should leave room for

Small things the read-model build can do now, so the snapshot, archive
and privacy work add to it instead of reworking it:

1. **Saved forms beside `Clone`.** `tasks.Saved` with `(*Task).Save()`
   and `tasks.Restore`, for the unexported `collected` and `storyNews`,
   and the same in `agents` (`untouched`), `workers` and `connections`
   (`credential`, `invites`): the same field list `(*Task).Clone`
   (`internal/tasks/clone.go`) walks, checked by the same kind of
   reflection test (`clone_test.go`).
2. **An in-place apply that starts from a given state.** Export the
   method `tasks.FromEvents` loops over (`(*Task).apply`, as `Apply`) and
   the rosters' `apply(r *Roster, e)`, so a loaded snapshot takes its tail
   without a clone per event.
3. **Let the model start part way.** A `store.Model` that can say where
   it already is (say `Since() int64`, 0 for empty), so `Open` replays
   `After(since)` instead of grouping every stream.
4. **Keep each stream's last applied position** in `current.State`, next
   to `position`. task-21 has only the log's `position` today. The
   archiver reads it to choose `through` without a query; the check that
   the task has not moved since stays in `Store.Archive`, against the
   database, under `Store.mu`.
5. **Order tasks by their `task.created` position, held per task**, not
   only in the `created` list `Loaded` sorts, since an archived task
   keeps that position in its marker and has no `task.created` row left.
6. **Count archived tasks in `Store.made`**, which `Store.Create` calls
   to name the next task, and keep them in `State.TaskIDs` (section 4).
   The readers, and what each should see for an archived task:
   - `Store.Create`, from `web/api.go` `createTask` and
     `coordinator/loop.go` `newTask`: the next id, `made + 1`, so
     archived tasks must be counted;
   - `web/api.go` `listTasks`: the task's summary;
   - `watchdog/watchdog.go` `Check`, `hooks/orphans.go`
     `HoldOrphanedAsks`, `coordinator/loop.go` `portsInUse`,
     `web/agents_api.go` (the rename and `needing`),
     `web/questions_api.go` `listQuestions` and `web/workers_api.go`
     `tasksByMachine`: nothing, since an archived task is done; each
     skips it, as it skips a done task, without reading its stream;
   - `current.Equal`: the summary on both sides, the state's against
     `tasks.Archived` of a replay.
   So task-21 should not count `len(Tasks())` for an id, and those readers
   should skip a task the state can't hand out instead of failing.
7. **Done tasks behind a summary type.** Move `web.summaryOf` into
   `tasks.Summary` and have the list read summaries, so an archived task
   can be a summary alone.
8. **Getters that can say "archived".** Room for `Task(id)` to return
   nil with an `ErrArchived`, and for readers to handle a task with no
   full state.
9. **One way to fold a stream again**, for `task.unarchived` and
   `event.redacted`: a fourth `store.Model` method,
   `Refold(stream string, before []events.Event, e events.Event)`, beside
   `Load`, `Loaded` and `Apply`. `Apply(e)` alone can't read the store, so
   the store, under `Store.mu`, reads the stream's accepted events with a
   position below `e` and calls `Refold` in place of `Apply`, live and in
   `Open`'s tail. `State.Refold` folds `before` plus `e` as `Load` does and
   moves `position`.
10. **`Task.DoneAt`**, set by `task.done` and cleared by `task.reopened`.
11. **No version constant.** The snapshot will be tagged with the
    binary's hash, so nothing needs bumping by hand.
