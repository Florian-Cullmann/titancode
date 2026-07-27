# TitanCode

**Local observability for AI-native software development.**

TitanCode runs next to terminal-based coding agents and turns a repository into a
calm, continuously updated browser workspace. It helps you understand the codebase,
review the current working tree, and keep track of project structure without
replacing your CLI workflow.

> [!IMPORTANT]
> TitanCode is an early development preview. The current release provides the
> repository overview and live Git integration; deeper code intelligence, tests,
> coverage, and architecture analysis are on the roadmap.

## Why TitanCode?

AI coding agents are excellent at changing code, but terminal workflows make it
easy to lose sight of the system as a whole. TitanCode is designed to answer:

- What is in this repository?
- What changed, and how large is the change?
- Which parts of the project deserve attention?
- How does the codebase evolve while an agent is working?

The goal is not another full IDE. TitanCode is a local observation and review
layer that stays useful alongside any CLI-based agent.

## Features

- local web dashboard served by a single background process
- continuous repository scanning with live browser updates
- Git branch, working-tree status, and diff statistics
- unified review for working-tree and staged changes
- per-file stage and unstage actions
- multi-suite test discovery for nested and mixed-language repositories
- Go, Python unittest, PHPUnit, and JavaScript/TypeScript test execution
- independent suite results, runtimes, cancellation, and failure output
- live freshness tracking when framework-relevant files change after a test run
- persistent per-suite history with failure details and runtime slowdown detection
- per-suite execution modes: manual, debounced after inactivity, or on new staged content
- repository areas grouped by file and code size
- language distribution and source-file statistics
- `.gitignore`-aware scanning
- automatic exclusion of virtual environments, dependencies, and build caches
- responsive interface with no external runtime services
- factual metrics only—no unexplained or fabricated quality scores

## Installation

### Requirements

- Linux (currently the primary supported platform)
- [Go 1.26 or newer](https://go.dev/doc/install)
- Git

### Build from source

Clone the repository and build the standalone binary:

```bash
git clone https://github.com/Florian-Cullmann/titancode.git
cd titancode
go build -o bin/titancode ./cmd/titancode
```

Optionally install it somewhere on your `PATH`:

```bash
install -Dm755 bin/titancode "$HOME/.local/bin/titancode"
```

Ensure `$HOME/.local/bin` is part of your `PATH`, then verify the installation:

```bash
titancode -h
```

### Run without installing

From the cloned repository:

```bash
go run ./cmd/titancode -repo /path/to/your/project
```

## Usage

Observe a repository:

```bash
titancode -repo /path/to/your/project
```

TitanCode starts a local server at
[http://127.0.0.1:7331](http://127.0.0.1:7331) and opens the dashboard in your
default browser.

Useful options:

```text
-repo PATH     Repository to observe (default: current directory)
-addr ADDRESS  HTTP listen address (default: 127.0.0.1:7331)
-no-open       Do not open the browser automatically
```

Examples:

```bash
# Observe the current directory
titancode -repo .

# Keep the browser closed on startup
titancode -repo ~/coding/my-project -no-open

# Use a different local port
titancode -repo ~/coding/my-project -addr 127.0.0.1:7332
```

## What gets scanned?

In Git repositories, TitanCode analyzes tracked files and untracked files that are
not excluded by standard Git ignore rules. This includes repository-level and
nested `.gitignore` files, `.git/info/exclude`, and global Git ignore rules.

Common non-project directories are excluded automatically, including:

- `.git`, `.venv`, `venv`, and Python caches
- `node_modules`, `vendor`, and `target`
- `dist`, `build`, and `coverage`
- framework caches such as `.next`, `.svelte-kit`, and `.turbo`

Repositories without Git are supported as well and use TitanCode's built-in
exclusion list.

## Architecture

TitanCode currently ships as one Go binary:

```text
Repository
    │
    ├── Git and filesystem scanner
    │       └── project snapshot
    │
    ├── local HTTP server
    │       ├── JSON API
    │       └── Server-Sent Events
    │
    └── embedded browser interface
```

All analysis happens locally. The current version does not upload repository
contents or require a cloud service.

## Development

Run the application against this repository:

```bash
go run ./cmd/titancode -repo .
```

Run the test suite and static checks:

```bash
go test ./...
go vet ./...
```

Build the production binary:

```bash
go build -o bin/titancode ./cmd/titancode
```

## Roadmap

- unified and semantic diff review
- symbol index, definitions, references, and call graphs
- support for additional test frameworks
- coverage for changed lines and test-to-code relationships
- complexity, duplication, size, and readability trends
- explicit architecture rules and dependency violations
- regression detection against Git baselines
- optional browser-based agent orchestration
- packaged Linux background service and Windows support

## Contributing

TitanCode is at an early stage, so issues and focused pull requests are welcome.
Before starting a large change, please open an issue to discuss the intended
behavior and architecture.

When submitting code:

1. keep the local-first model intact;
2. add tests for behavior changes;
3. run `go test ./...` and `go vet ./...`;
4. explain user-visible behavior and trade-offs in the pull request.

## Security

TitanCode binds to `127.0.0.1` by default. Avoid exposing it on a public interface:
the dashboard provides information about the observed repository and is not yet
designed as a remotely accessible multi-user service.

Please report security-sensitive findings privately to the repository owner
instead of opening a public issue.

## License

No open-source license has been selected yet. Until a license is added, all rights
remain with the copyright holder.
