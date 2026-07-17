# weir

An MCP server that gives AI agents controlled access to the local system.

Agents need to run commands and search filesystems, but unrestricted shell
access is a bad idea. Weir sits in between: you whitelist exactly which
commands an agent can run, and the built-in find tool exposes only safe,
read-only filesystem queries. Everything else is denied.

## Usage

```
weir --run-allow cmd1,cmd2,... [--find-deny /path1,/path2,...] [--timeout 60] [--max-lines 1000]
```

**--run-allow** (required): comma-separated list of command basenames to whitelist.
Commands not in this list are rejected.

Entries can optionally define allowed subcommands using colon-separated
syntax:

```
weir --run-allow "swamp,rg,deno:test;fmt;lint;check,git:status;log;diff;checkout;commit;push;fetch;remote"
```

- `swamp` — all subcommands allowed (no restriction)
- `deno:test;fmt;lint;check` — only `deno test`, `deno fmt`, `deno lint`,
  and `deno check` are allowed; `deno eval`, `deno run`, etc. are rejected
- `git:status;log;diff;...` — only the listed git subcommands are allowed

When subcommands are specified, the first argument to the command is checked
against the list. If it doesn't match, the request is denied with an error
naming the allowed subcommands.

**--find-deny**: additional paths to block from `find` searches. Appends to
the hardcoded deny list (`/`, `/proc`, `/sys`, `/dev`).

**--timeout**: default command timeout in seconds. 0 means unlimited.
Defaults to 60.

**--max-lines**: default maximum output lines returned from commands. 0 means
unlimited. Defaults to 1000. Can be overridden per-request via the `max_lines`
field in a tool call.

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

Weir's whitelist is intentionally small. If you find yourself wanting to add
many commands, that's a signal the work should live in structured tooling
instead — something with validation, state tracking, and versioned output
rather than raw shell access.

For us, that's [swamp](https://github.com/swamp-labs/swamp). Commands that
agents need regularly become swamp extensions: typed, schema-validated, with
checks that gate execution. Weir provides the escape hatch (`rg` for search,
`swamp` for everything else); the extensions provide the real surface area.

The constraint is the point. An ever-growing whitelist is a sign the
architecture is missing a layer.

## Protocol

JSON-RPC 2.0 over stdin/stdout, per the [MCP spec](https://modelcontextprotocol.io).
