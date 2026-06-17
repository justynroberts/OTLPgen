# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

OTLPgen is a single-binary observability data generator written in Go. It synthesizes
realistic **logs, metrics, and traces** for a simulated multi-service platform and emits
them over **OTLP/HTTP JSON** (`/v1/logs`, `/v1/metrics`, `/v1/traces`) to any OTLP-capable
backend. There is no external runtime; the default platform definition is embedded in the
binary at build time.

## Build / run

Requires Go 1.24+. There is no test suite.

```bash
make build          # build ./otlpgen for the current platform
make run            # build + run --one-shot (one batch of everything, then exit)
make print-config   # build + print the fully resolved+templated config
make dist           # cross-compile binaries for all 5 platforms into dist/
make tidy           # go mod tidy
VERSION=v0.2.0 make dist   # version is stamped via -ldflags -X main.version

# Run directly during dev:
go build -o otlpgen . && ./otlpgen --one-shot --verbose
./otlpgen --print-config            # inspect the effective config without sending
./otlpgen --signals logs --one-shot # one signal only
./otlpgen --chaos heavy --duration 5
```

`dist/` binaries are committed (they are release artifacts); the local `./otlpgen` build is
gitignored. The version is injected at link time, so `--version` only reflects a real value
on a `make`-built binary.

To actually send data you must set an endpoint, or the binary exits with an error:
`export OTEL_EXPORTER_OTLP_ENDPOINT=...` and `export OTEL_API_KEY=...` (the embedded config's
default header is `ApiKey`, Elastic-style).

## Architecture

Four `package main` files, no sub-packages:

- **`config.go`** — the config pipeline and all typed structs. The flow in `LoadConfig` is the
  key thing to understand: **embedded YAML → deep-merge override → resolve `{{vars}}` → decode
  to typed `Config`**. The config is parsed twice: first into `map[string]any` so it can be
  merged and var-substituted generically, then re-marshaled and decoded into the typed
  `Config`. `--print-config` prints those re-marshaled bytes. `${ENV}` references in the OTLP
  URL and headers are resolved separately, at the end of `LoadConfig`, at runtime.
- **`generate.go`** — the `Generator`. Turns the typed config into OTLP/JSON payloads
  (`buildOTLPLogs`, `generateMetrics`, `generateTrace`). Uses weighted random selection
  (`weightedPick`) for log patterns and trace templates, and `±15%`/`±30%` jitter for metric
  and span-duration values. Also holds `applyChaos`, which mutates weights/baselines **in
  memory** before generation.
- **`otlp.go`** — the `Client`. Marshals a payload to JSON and POSTs to `baseURL+endpoint`;
  success is any **2xx** (backends return 200/202/204 — e.g. Grafana acks logs with 204).
  `verify_ssl: false` disables TLS verification. Only `OTEL_EXPORTER_OTLP_ENDPOINT` and
  `OTEL_API_KEY` are read from the environment — the OTel SDK's `OTEL_EXPORTER_OTLP_HEADERS`
  / `_PROTOCOL` are **not**; non-default auth needs a `--config` override.
- **`main.go`** — CLI flags, logging helpers, `Stats` accounting, and `Generator.Run` (the
  per-second emit loop; `--one-shot` emits one larger batch and exits, otherwise it loops
  every second until `--duration` elapses or SIGINT/SIGTERM).

### Config model and the two name systems

The embedded default lives in **`config/services.yaml`** (`//go:embed`). When changing default
platform behaviour, edit that file, not Go code.

A `services` entry has two distinct identifiers, and conflating them causes broken traces:
- the **map key** (`id`) is a stable internal handle — trace `SpanDef.service` and `parent`
  reference services by this id;
- **`name:`** is the emitted `service.name`. If omitted, it falls back to the id.

Traces are wired by id: each `SpanDef` names a `service` id and optionally a `parent` id; the
generator resolves parents to span IDs within the same trace, so a `parent` must reference
another span's `service` id in the same template.

### Templating

Two independent substitution systems, do not confuse them:
- **`{{var}}`** — resolved at config-load time from the `vars` map, applied to *every string*
  in the config tree (`substituteTree`). Vars may reference other vars (`resolveVars` iterates
  to a fixed point). This is what makes changing `vars.platform` re-skin the whole platform.
- **`${ENV}`** — resolved from OS environment variables, but **only** for `otlp.url` and
  `otlp.headers` values (`resolveEnv`). Used for secrets/endpoints.

There is also a third, separate mechanism: log `templates` use `{placeholder}` (single brace),
filled from the pattern's `attributes` pools at generation time in `generateLogEntry`.

### Merge semantics (override files)

`deepMerge`: **maps merge key-by-key; lists and scalars replace wholesale.** So to tweak one
field of one service you supply just that field, but to change a `tags:` or `templates:` list
you must provide the entire replacement list. `enabled: false` on a service drops it (services
are opt-out via a `*bool` so "unset" means enabled).

### Chaos

`--chaos {mild|heavy|extreme}` looks up a `[errWeight, traceWeight, metricSpike]` multiplier
triple in `chaosPresets` and mutates the in-memory config before emission: it scales
error-level log-pattern weights, scales weights of traces whose name contains
failure/slow/error, and spikes baselines of metrics whose name matches
error/latency/cpu/memory/queue/mismatch/failure (clamped to `max` and to 10× baseline).

## Notes

- OTLP/JSON encodes int64 as strings — `attrInt` and all `*UnixNano` fields are emitted as
  string values deliberately; keep that when adding attributes.
- `examples/` holds override files: `minimal.yaml` (one-line rename) and `acme-shop.yaml`
  (full reskin). They double as documentation of the override format.
</content>
</invoke>
