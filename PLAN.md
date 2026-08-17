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
