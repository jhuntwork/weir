# weir

An MCP routing interface for AI agents that keeps direct tools intentionally
small and steers reusable operations toward [Swamp](https://swamp-club.com/).

Weir is not a sandbox or security boundary. Its `run` and `find` tools are
bounded, legible escape hatches: `swamp` is the primary operation surface;
`rg`, limited extension-development validation, and read-only Git inspection
remain direct where useful. A denied command is product feedback: search for a
Swamp capability, inspect whether it fits, then extend or build durable tooling
only when needed.

## Usage

```
weir --run-allow cmd1,cmd2,... [--find-deny /path1,/path2,...] [--timeout 60] [--max-lines 1000]
```

**--run-allow** (required): comma-separated list of command basenames to
allow. Commands not in this list are routed to Swamp-first guidance.

Entries can optionally define allowed subcommands using colon-separated syntax.
The current recommended direct surface is:

```
weir --run-allow "swamp,rg,deno:test;fmt;lint;check,git:status;log;diff"
```

- `swamp` — primary typed operation surface; all Swamp subcommands are allowed
- `rg` — small, general-purpose read-only exploration primitive
- `deno:test;fmt;lint;check` — extension-development validation only; `deno
  eval`, `deno run`, and other execution forms are denied
- `git:status;log;diff` — read-only inspection only

When subcommands are specified, the first argument to the command is checked
against the list. Use the canonical form `command subcommand [args...]`; flags
before the subcommand are intentionally unsupported. If it does not match, the
request is denied with the allowed subcommands and a Swamp-first handoff.

### Denied-operation handoff

For a denied operation such as `git commit`, Weir preserves the attempted
operation in its telemetry and directs the agent to:

1. Search model extensions by service, not operation:
   `swamp extension search "git" --content-type models --json`
2. Prefer `@swamp/*` candidates, but inspect their types and methods before
   pulling: `swamp extension info <package> --json`
3. Pull only a candidate with the needed type and method; otherwise check local
   types: `swamp model type search "git" --json`
4. Extend the closest fitting model type. Create a new Swamp model extension
   only when no type covers the operation.

**--find-deny**: additional paths to block from `find` searches. Appends to
the hardcoded deny list (`/`, `/proc`, `/sys`, `/dev`).

**--timeout**: default command timeout in seconds. 0 means unlimited.
Defaults to 60.

**--max-lines**: default maximum output lines returned from commands. 0 means
unlimited. Can be overridden per request via `max_lines`. Applied after all
filters.

**--max-bytes**: default maximum output bytes returned from commands. 0 means
unlimited. Can be overridden per request via `max_bytes`. Applied after all
filters.

**--max-stream-bytes**: hard byte budget for each command/filter pipeline
stage. Defaults to 32 MiB. Buffered filters fail explicitly if they cannot
compute a complete result inside this budget.

**--max-find-results**: maximum `find` matches collected and returned. Defaults
to 1000; truncated searches say so in their result.

## Tools

### run

Executes a whitelisted command. Supports per-command environment variables,
per-request timeout overrides, and output truncation (max 1000 lines by
default).

Output can be post-processed through a chain of filters applied left-to-right:

| Filter   | Implementation | Description                                  |
|----------|----------------|----------------------------------------------|
| `grep`   | built-in       | keep lines matching a regex                  |
| `grep_v` | built-in       | exclude lines matching a regex               |
| `head`   | built-in       | keep the first N lines                       |
| `tail`   | built-in       | keep the last N lines                        |
| `sort`   | built-in       | sort lines (lexicographic, numeric, reverse) |
| `uniq`   | built-in       | deduplicate adjacent lines, optional counts  |
| `wc`     | built-in       | count lines, words, or chars                 |
| `jq`     | system `jq`    | apply a jq expression to JSON                |

`grep`, `grep_v`, `head`, `tail`, `sort`, `uniq`, and `wc` are implemented
internally with no external dependencies. `jq` shells out to the system's
`jq` binary (must be in PATH).

**Filter expressions:**

- `sort`: empty for lexicographic, `"reverse"`, `"numeric"`, or `"numeric,reverse"`
- `uniq`: empty for plain dedup, `"count"` to prefix each line with its run count
- `wc`: `"lines"` (default), `"words"`, or `"chars"`

### find

A built-in, read-only subset of POSIX `find`. Walks a directory tree and
matches entries by name glob, type (file/dir/symlink), depth, modification
time, and size. Supports exclude patterns matched against basenames.

No `-exec`, no `-delete`, no shell evaluation, no permission changes —
this is a pure query tool. It exists so agents can explore the filesystem
without needing shell access or a real `find` in the whitelist.

Hardcoded deny paths (`/`, `/proc`, `/sys`, `/dev`) cannot be overridden.
Additional paths can be denied via `--find-deny`.

## Build

```
go build -o weir .
```

## Philosophy

Weir's direct surface is intentionally small. If work repeatedly needs a raw
command, that is evidence the operation should live in structured, reusable
Swamp tooling instead: typed models and methods with validation, state tracking,
and versioned output.

A denied command is therefore a routing signal, not an invitation to grow the
allowlist. Weir keeps `rg` for exploration and `swamp` for durable operations;
Swamp extensions provide the real capability surface. An ever-growing direct
allowlist is a sign the architecture is missing that layer.

## Protocol

JSON-RPC 2.0 over stdin/stdout, per the [MCP spec](https://modelcontextprotocol.io).
