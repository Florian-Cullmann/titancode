# TitanCode

TitanCode is a local observability workspace for AI-native software development. It
runs beside terminal-based coding agents and provides a calm browser view of the
repository, working tree and project structure.

## Current vertical slice

- single local Go process with embedded web UI
- continuous repository scanning
- language, module, file and line statistics
- Git branch, working tree status and diff statistics
- live browser updates over Server-Sent Events
- responsive overview inspired by the product's simplified dashboard direction

The current metrics are deliberately factual. Quality, coverage and architecture
scores will only appear when TitanCode can explain their source and link them to
concrete findings.

## Run

```bash
go run ./cmd/titancode -repo /path/to/repository
```

TitanCode listens on `127.0.0.1:7331` and opens the browser. To suppress that:

```bash
go run ./cmd/titancode -repo . -no-open
```

Build a standalone Linux binary:

```bash
go build -o bin/titancode ./cmd/titancode
```

## Near-term roadmap

1. change review with real unified and semantic diffs
2. symbol index and references through language servers
3. test discovery, execution and result history
4. diff coverage and test-to-code relationships
5. explicit architecture rules and dependency violations
6. quality trends against Git baselines

