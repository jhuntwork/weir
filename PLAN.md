# Weir direction

## Purpose

Weir is not a sandbox or a security boundary. It guides an agent toward durable,
queryable, reusable operations expressed through Swamp.

The direct `run` and `find` tools are deliberately small escape hatches:

- `swamp` is the default operation surface.
- A small set of commands may support operating or developing Swamp extensions
  (for example, selected `deno` validation subcommands).
- A few universally useful inspection tools (for example, `rg`) may remain
  direct.
- A denied command is product feedback: search model extensions by service,
  inspect candidate types and methods before pulling, then check installed
  types. Extend the closest fitting type; create a new extension only when no
  type covers the operation.
- Direct tools should be AI-native: explicit typed operations, bounded and
  legible results, and a clear path from a failed attempt to reusable tooling.

The allowlist is therefore a routing mechanism, not a convenience catalog.

## First slice: denial feedback and telemetry

### Decisions

1. Record complete command-attempt events, including the raw command, parsed
   executable/arguments, cwd, environment, request limits, filters, and the
   denial reason. Do not optimize for sensitive-value redaction yet.
2. Give denied direct commands a concise, Swamp-first response that searches
   model extensions by service, requires inspecting candidate metadata before
   pulling, then checks installed types. Extension creation is the last resort
   after no candidate or type exposes the needed operation.
3. Emit durable JSONL events only. Reporting and aggregation are later work.
4. Restricted commands use a canonical form: `command subcommand [args...]`.
   Flags before the subcommand are rejected rather than parsed heuristically.
   This makes the escape hatch predictable and telemetry semantically useful.

### Initial direct-command posture

- `swamp`: primary interface.
- `rg`: small general-purpose inspection primitive.
- `deno`: only extension-development validation subcommands (`test`, `fmt`,
  `lint`, `check`) for now; revisit if they prove too broad.
- `git`: read-only inspection (`status`, `log`, `diff`) for now. Swamp Git
  extensions, such as `@twonines/git-workspace`, own meaningful Git workflows.

## Second slice: reliable pipe semantics

Preserve the mental model `command | filter | ... | response` while making it
safe and predictable for an agent session:

- Apply `max_lines` and `max_bytes` only to the final response after filters.
- Keep draining a completed `head` stage so a verbose child is never left
  blocked on stdout.
- Use a hard stream-byte budget for every pipeline edge. Buffered filters must
  fail explicitly when they cannot compute a complete result within it; they
  must not return plausible partial `sort`, `tail`, or `uniq` output.
- Bound stderr and make its truncation visible.
- Bound `find` result collection, support cancellation, and report truncation.

These are reliability and context-management guarantees, not an attempt to
turn Weir into a security sandbox.

### Out of scope for this slice

- A security sandbox or comprehensive command-policy language.
- Telemetry reporting/aggregation UI.
- Replacing or tightening `deno test` beyond the current narrow allowlist.
- Removing the remaining direct Git inspection operations.
- Resource limits, pagination, and other runtime reliability work.

## Third slice: inspectable local job lifecycle

### Scope and guarantee

A job is a Weir-owned local process tree started explicitly through a dedicated
job interface. It is not a `background: true` flag on `run`, and it is not a
claim to supervise arbitrary detached processes, remote work, or durable
provider-side effects.

The end-to-end probe on 2026-08-17 established that Weir's Unix process group
contains the ordinary local chain `Weir → swamp CLI → Swamp model → descendant`.
On cancellation Weir can truthfully guarantee that this local process tree is
terminated once its direct child has exited. It cannot claim that a remote
mutation was rolled back or that an extension which detaches/daemonizes a child
remains controllable.

V1 jobs are server-lifetime: Weir owns their child process groups and cancels
all non-terminal jobs during orderly server shutdown. They are not recovered as
live work across a Weir restart. Persisted terminal records and bounded logs
remain inspectable until TTL cleanup; a restart records any previously-running
job as interrupted rather than guessing that it completed.

### Required model

Every job has an opaque ID and these states:

```text
queued → running → succeeded | failed | cancelled | interrupted | expired
                  ↘ cancellation_requested ↗
```

`cancellation_requested` is observable, never terminal. `cancelled` is written
only after Weir has killed the job's process group and reaped its direct child.
`interrupted` means the Weir server stopped before it could establish a terminal
outcome. `expired` is a retained terminal summary after bounded log cleanup, not
a silent deletion of history.

Persist for each job: creation/start/terminal timestamps, elapsed duration,
command identity and parsed argv, cwd, configured environment names (with a
redaction policy before persistence), request/RPC attribution available to the
server, process PID/PGID while running, exit code or terminal error, output
truncation state, and explicit effect scope `local_process_tree`.

Stdout and stderr are captured independently in bounded append-only logs. A
job's status/result declares whether either log was truncated. Terminal job
records have a configurable TTL and are removed only by explicit bounded cleanup
that preserves an expiry summary long enough for callers to distinguish
"expired" from "unknown ID".

### Public surface

Expose separate typed tools, not a run flag:

- `job_start`: validate the same restricted command grammar and limits as
  foreground `run`, create a queued job, and return its ID and initial state.
- `job_get`: return one inspectable status/result record.
- `job_logs`: return a bounded stdout/stderr tail with truncation metadata.
- `job_list`: return a bounded filtered list of jobs.
- `job_cancel`: request cancellation, kill the owned process group, wait for
  reaping, and return the observed state.
- `job_cleanup`: explicitly apply TTL cleanup to terminal records.

The first implementation should accept only commands already permitted by
Weir. It must preserve the Swamp-first routing posture and must not add a
shell, a generic detached-process facility, or implicit Git/network semantics.

### Acceptance evidence

Before exposing the surface, test queueing/concurrency limits, status and log
bounds, timeout and explicit cancellation, process-group descendant cleanup,
server-shutdown interruption, terminal TTL cleanup, and the distinction between
local cancellation and unknown remote effects. Exercise a real Swamp model
chain in integration coverage in addition to unit-owned child processes.
