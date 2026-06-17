# OTLPgen

A single-binary observability data generator. It emits realistic **logs, metrics, and
traces** over **OTLP/HTTP JSON** (`/v1/logs`, `/v1/metrics`, `/v1/traces`) — the standard
OpenTelemetry wire format — so it works with **any OTLP-capable backend**: Grafana, Elastic,
Datadog, Honeycomb, New Relic, Splunk, Dynatrace, or a plain OpenTelemetry Collector.

It simulates a multi-service platform (a payment platform by default) with correlated
signals: distributed traces with parent/child spans, gauge metrics with realistic jitter,
and weighted log lines including Kubernetes failure events. Use it to populate dashboards,
demo APM/service maps, or load-test an ingest pipeline.

- **One binary.** No runtime, no `pip install`. The default config is embedded.
- **One override file.** A single YAML file, deep-merged over the embedded default.
- **Templated identity.** Change `vars.platform` and every service name, tag, pod name,
  trace, and log line re-skins itself.

## Install

Pre-built binaries live in [`dist/`](dist/). Grab the one for your platform with `curl` —
this auto-detects OS/arch, downloads it as `otlpgen`, and makes it executable:

```bash
OS=$(uname -s | tr '[:upper:]' '[:lower:]')        # darwin | linux
ARCH=$(uname -m); [ "$ARCH" = "x86_64" ] && ARCH=amd64; [ "$ARCH" = "aarch64" ] && ARCH=arm64
curl -fsSL -o otlpgen \
  "https://raw.githubusercontent.com/justynroberts/OTLPgen/main/dist/otlpgen-$OS-$ARCH"
chmod +x otlpgen
```

Or pick a specific build directly:

| Platform | Command |
|---|---|
| macOS (Apple Silicon) | `curl -fsSL -o otlpgen https://raw.githubusercontent.com/justynroberts/OTLPgen/main/dist/otlpgen-darwin-arm64 && chmod +x otlpgen` |
| macOS (Intel) | `curl -fsSL -o otlpgen https://raw.githubusercontent.com/justynroberts/OTLPgen/main/dist/otlpgen-darwin-amd64 && chmod +x otlpgen` |
| Linux (x86-64) | `curl -fsSL -o otlpgen https://raw.githubusercontent.com/justynroberts/OTLPgen/main/dist/otlpgen-linux-amd64 && chmod +x otlpgen` |
| Linux (arm64) | `curl -fsSL -o otlpgen https://raw.githubusercontent.com/justynroberts/OTLPgen/main/dist/otlpgen-linux-arm64 && chmod +x otlpgen` |
| Windows (x86-64) | `curl -fsSL -o otlpgen.exe https://raw.githubusercontent.com/justynroberts/OTLPgen/main/dist/otlpgen-windows-amd64.exe` |

> On macOS, Gatekeeper may quarantine a downloaded binary. If it refuses to run, clear it with
> `xattr -d com.apple.quarantine ./otlpgen`. Prefer building from source? See [Building](#building).

## Quick start

Once you have the `otlpgen` binary, you need just two things: an OTLP endpoint and a token.

```bash
# 1. Point at your backend (otlpgen reads these two env vars only)
export OTEL_EXPORTER_OTLP_ENDPOINT="https://your-otlp-endpoint:443"
export OTEL_API_KEY="your-token"

# 2. Send one batch of everything and exit
./otlpgen --one-shot

# 3. Or run continuously for 10 minutes
./otlpgen --duration 10
```

The default config sends an Elastic-style `Authorization: ApiKey <OTEL_API_KEY>` header. For a
backend that needs a different header (Grafana, Honeycomb, New Relic, …), pass an override file
with `--config` — see [Configuring for your vendor](#configuring-for-your-vendor).

## Usage

```
otlpgen [flags]

--config PATH     YAML override file, deep-merged over the embedded default
--signals LIST    Comma-separated: logs,metrics,traces (default: all)
--duration N      Run for N minutes (default: continuous)
--one-shot        Send one batch and exit
--chaos LEVEL     Inject errors/spikes: mild | heavy | extreme
--print-config    Print the resolved + templated config and exit
--verbose         Verbose logging
--version         Print version
```

```bash
otlpgen --signals logs                  # logs only
otlpgen --signals metrics,traces        # APM only
otlpgen --chaos heavy --duration 5      # error storm for 5 minutes
otlpgen --config myplatform.yaml        # custom platform
otlpgen --print-config                  # inspect the effective config
```

## Configuring for your vendor

OTLPgen sends standard OTLP/HTTP JSON. You only need to set two things: the **base URL**
and the **auth header(s)**. The tool appends `/v1/logs`, `/v1/metrics`, `/v1/traces` to the
base URL, so point the URL at the OTLP *root*, not at a signal path.

There are two ways to configure auth:

**A. Environment variables** (quickest — works with the default `ApiKey` header):

```bash
export OTEL_EXPORTER_OTLP_ENDPOINT="https://host:443"
export OTEL_API_KEY="token"
```

> **Note:** otlpgen reads only these two variables. It does **not** read the OpenTelemetry
> SDK variables `OTEL_EXPORTER_OTLP_HEADERS` or `OTEL_EXPORTER_OTLP_PROTOCOL` — a header set
> there is silently ignored and the wire format is always OTLP/HTTP **JSON**. If your vendor
> needs a header other than `Authorization: ApiKey <key>` (e.g. Grafana's `Basic`, Honeycomb's
> `x-honeycomb-team`), use an override file (option B) — not those env vars.

**B. An override file** (when the vendor needs a different header name):

```yaml
# vendor.yaml
otlp:
  url: "${OTEL_EXPORTER_OTLP_ENDPOINT}"
  headers:
    x-vendor-token: "${MY_TOKEN}"   # ${ENV} is resolved at runtime
```

```bash
otlpgen --config vendor.yaml
```

### Per-vendor settings

| Vendor | Base URL (`OTEL_EXPORTER_OTLP_ENDPOINT`) | Auth header |
|---|---|---|
| **OTel Collector** (self-hosted) | `http://collector-host:4318` | usually none |
| **Grafana Cloud** | `https://otlp-gateway-<zone>.grafana.net/otlp` | `Authorization: Basic <base64(instanceID:token)>` |
| **Grafana Alloy / Agent** | `http://alloy-host:4318` | as configured on Alloy |
| **Elastic** (APM / Cloud) | `https://<deployment>.apm.<region>.cloud.es.io:443` | `Authorization: ApiKey <key>` |
| **Datadog** (via Agent OTLP intake) | `http://datadog-agent:4318` | none (Agent holds the API key) |
| **Honeycomb** | `https://api.honeycomb.io` | `x-honeycomb-team: <key>` |
| **New Relic** | `https://otlp.nr-data.net` | `api-key: <license-key>` |
| **Splunk Observability** | `https://ingest.<realm>.signalfx.com` | `X-SF-Token: <token>` |
| **Dynatrace** | `https://<env>.live.dynatrace.com/api/v2/otlp` | `Authorization: Api-Token <token>` |

> Endpoints and header names change over time and by region/plan — check your vendor's
> "OpenTelemetry / OTLP" docs page for the exact values for your account.

### Vendor examples

**Grafana Cloud** — Basic auth from your stack's OTLP credentials:

```yaml
# grafana.yaml
otlp:
  url: "https://otlp-gateway-prod-eu-west-0.grafana.net/otlp"
  headers:
    Authorization: "Basic ${GRAFANA_OTLP_TOKEN}"   # base64("<instanceID>:<apiToken>")
```

**Honeycomb** — team key, plus a dataset header for metrics:

```yaml
# honeycomb.yaml
otlp:
  url: "https://api.honeycomb.io"
  headers:
    x-honeycomb-team: "${HONEYCOMB_API_KEY}"
    x-honeycomb-dataset: "otlpgen"
```

**New Relic** — license key:

```yaml
# newrelic.yaml
otlp:
  url: "https://otlp.nr-data.net"
  headers:
    api-key: "${NEW_RELIC_LICENSE_KEY}"
```

**Self-hosted OTel Collector** — no auth, plain HTTP:

```yaml
# collector.yaml
otlp:
  url: "http://localhost:4318"
  verify_ssl: false
  headers: {}
```

The default (embedded) config already targets an `ApiKey` header (Elastic-style), so for
Elastic you only need the two environment variables from [Quick start](#quick-start).

## Customising the simulated platform

Everything about the simulated platform lives in the config. The embedded default is in
[`config/services.yaml`](config/services.yaml); override any part of it with `--config`.

### Templated service names

Identity strings are driven by `vars` and referenced with `{{var}}`. Vars can reference
other vars. The smallest useful override is a single line:

```yaml
# minimal.yaml — rename the entire platform
vars:
  platform: "checkout"
```

This propagates everywhere: `service.name`, `service:` tags, Kubernetes deployment/pod
names, trace names, and the text of log lines all become `checkout-*`.

Override individual service names too:

```yaml
vars:
  platform: "acme"
  api: "acme-storefront"      # instead of the default {{platform}}-api
  processor: "acme-orders"
```

See [`examples/`](examples/) for a minimal rename and a full reskin (`acme-shop.yaml`).

### How merging works

The override file is **deep-merged** over the embedded default:

- **Maps** merge key-by-key — so you can tweak one service without redefining the others.
- **Lists and scalars** replace — provide a full `tags:` or `templates:` list to change it.

```yaml
services:
  api:
    rate_per_minute: 80     # change just this; everything else inherited
  reconciliation:
    enabled: false          # disable one service
```

Run `otlpgen --print-config` at any time to see the fully resolved, templated config.

### Config structure

- `vars` — templating variables (`{{platform}}`, `{{cluster}}`, custom names, …).
- `otlp` — `url`, `environment`, `verify_ssl`, and `headers` (auth).
- `services.<id>` — one block per service. The map key (`id`) is a stable internal handle
  used by trace span references; `name:` is the emitted `service.name`.
  - `rate_per_minute`, `tags`, `hostnames`
  - `log_patterns` — weighted log lines with `{placeholder}` attribute pools per level
  - `metrics` — gauge measurements with `min`/`max`/`baseline` (jittered ±15%, clamped)
- `traces` — weighted distributed-trace templates. Each span references a service by `id`
  and optionally a `parent` id for parent/child wiring.

## Building

Requires Go 1.24+.

```bash
make build          # build ./otlpgen for the current platform
make dist           # cross-compile binaries for all platforms into dist/
make run            # build + one-shot
VERSION=v0.2.0 make dist
```

Cross-compilation targets: macOS (arm64/amd64), Linux (amd64/arm64), Windows (amd64).

## Notes

- All numeric IDs and values are randomized within configured ranges; nothing is real data.
- `--chaos` amplifies error-level log weights, failure-trace weights, and error/latency/
  resource metric baselines in memory — useful for triggering alerts on demand.
- Any **2xx** response counts as a successful send. Backends differ: 200 (the OTLP spec),
  202, or 204 (Grafana's gateway acks logs with No Content) are all treated as delivered.
