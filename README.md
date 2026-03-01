# FComT Agent (Go)

FComT Agent is a free, local-first network monitoring client built around distributed agents, active synthetic testing, and continuous end-to-end measurements.

The goal is practical ISP accountability:

- run on local computers (home, branch, HQ, cloud VM),
- collect defensible SLA evidence (latency, jitter, loss, uptime, route quality),
- keep working even without cloud connectivity,
- optionally sync results to a shared server/API for team dashboards and escalation workflows.

## Disclaimer

This project is an independent, open implementation focused on ISP accountability and local-first monitoring.

## Why this exists

Most ISP disputes fail because customers lack high-resolution, timestamped, longitudinal evidence from the user edge. FComT Agent is built to produce that evidence continuously, with exports and reports suitable for:

- ISP ticket escalations
- Internal ops reviews
- Contract/SLA validation
- General curiousity

## Product shape

- Agent runtime: Go daemon (`FComT-agent`)
- Local UI: web app served locally (or separate desktop shell)
- Optional backend: API for multi-agent fleet management and cross-site analytics

The agent is the primary product. Server components are optional.

## Primary functionality

This section captures the primary capabilities to implement in the Go agent.

### 1) Distributed synthetic monitoring sessions

The agent supports two operating profiles so monitoring works with or without a server.

- `local_only` (default, no server required):
  - tests against well-known internet destinations and public test endpoints (Google, AWS, Cloudflare, and optional public speed-test hosts),
  - keeps all telemetry and analysis local,
  - default interval: every `60s` (configurable; recommended minimum `30s`).
- `server_assisted` (optional):
  - enables agent-to-agent synthetic sessions for site-to-site monitoring,
  - supports higher granularity probing (default `500ms`) and centralized reporting,
  - synchronizes telemetry to a coordinator for fleet-wide views.
- Probe transport: UDP (default), optional TCP fallback
- Session model (server-assisted): one side acts as `client` (initiates), one as `server` (responds)
- Purpose: simulate user-path quality without inspecting user payloads

Per-test/session metrics:

- latency (RTT, one-way estimate where clock sync allows),
- jitter (inter-packet delay variation),
- packet loss,
- packet duplication,
- packet reordering,
- availability/session timeout state,
- DSCP propagation check (optional).

### 2) Agent connection modes

Support role/mode behavior similar to common distributed monitoring deployments:

- `client_only`: always initiates outbound sessions; no inbound listening requirement.
- `private_server`: accepts inbound probes on private/LAN/VPN paths.
- `private_internet_server`: accepts inbound probes from internet-facing peers (NAT/port-forward aware).

Default listen port:

- `23999/udp` for synthetic probes
- `23999/tcp` for optional throughput tests/control channels

### 3) End-to-end path and destination monitoring

The agent must support both:

- agent-to-destination checks (internet/public targets such as Google/AWS/DNS/CDN/SaaS endpoints) in local-only mode,
- agent-to-agent sessions (site-to-site quality) in server-assisted mode.

This enables LAN-to-WAN and WAN-to-service visibility.

### 4) Visual traceroute telemetry

Agent performs frequent traceroute sampling and stores hop-level history.

- periodic probes (configurable; high-frequency mode around 1s cadence),
- ICMP/UDP traceroute support,
- hop latency/loss snapshots,
- route-change detection and timeline,
- reverse-path tests where feasible.

Output is designed for "what changed when performance broke" analysis.

### 5) SNMP device monitoring (read-only)

Agent polls local network devices for context during incidents.

- SNMP versions: v1, v2c, v3
- Poll intervals: `30s` and `60s` presets (configurable)
- Common counters:
  - interface errors/discards/CRC
  - utilization/congestion
  - CPU/memory
  - interface status/duplex/speed
- Device support target:
  - routers, switches, firewalls, Wi-Fi APs, SD-WAN edges

This helps distinguish ISP-path failures from local LAN/device issues.

### 6) On-demand and scheduled speed tests

Agent supports controlled throughput tests between two endpoints.

- run manually (diagnostic burst),
- run on schedule (baseline),
- bidirectional execution (up/down),
- max/min threshold validation,
- queueing when endpoint is busy.

Speed tests are explicitly rate-limited to avoid accidental network impact.

### 7) Thresholds, eventing, and smart alerts

Agent maintains configurable thresholds and emits normalized events.

Baseline preset profiles:

- high sensitivity (clean links/datacenter),
- medium sensitivity (stable broadband/MPLS),
- low sensitivity (unstable links/satellite/DSL).

Alert behavior:

- severity levels (`ok/info/warn/error/critical`),
- notification delay windows to suppress short blips,
- rate limits and summary batching,
- mute windows (maintenance/reboots),
- webhook/email integrations (locally or via optional server).

### 8) Long-term evidence and SLA scoring

Core accountability features:

- SLA profile definition per ISP/link:
  - max latency/jitter/loss,
  - minimum availability,
  - allowed maintenance windows,
  - measurement period (monthly/quarterly).
- breach detection:
  - rolling and billing-cycle views,
  - duration and severity of violations,
  - impacted destinations/sites.
- evidence bundle generation:
  - signed JSON/CSV exports,
  - chart snapshots,
  - traceroute change logs,
  - incident timeline with UTC timestamps.

### 9) Local-first storage and offline buffering

The agent must remain useful with no cloud dependency.

- local write-ahead queue for outbound sync,
- local time-series and event store,
- resilient backfill after reconnect,
- bounded retention policies by disk budget,
- deterministic replay for audits.

Suggested local storage:

- SQLite (metadata, config, events),
- compressed parquet/TSDB segments for metrics.

### 10) Optional server callouts (not required for core use)

When configured, agent can push data to one or more remotes:

- self-hosted FComT coordinator,
- managed community endpoint (future),
- customer SIEM/observability pipeline.

Callout model:

- outbound-only from agent by default,
- HTTPS with mutual auth/token auth,
- batch + stream modes,
- redaction controls before upload,
- unlocks higher-frequency profiles (for example `500ms`) and centralized reporting.

If no remote is configured, all features still work locally.

### 11) Privacy and non-intrusive operation

Design principles:

- no packet capture,
- no deep packet inspection,
- no user content collection,
- synthetic telemetry only,
- least-privilege runtime where possible.

This mirrors the "active synthetic, not passive traffic inspection" monitoring philosophy.

### 12) Automatic updates and remote config

Agent supports:

- signed update channels (stable/beta),
- policy-based rollout windows,
- pull-based config refresh,
- safe rollback on failed startup health checks.

All update operations must be auditable.

## Functional requirements (agent)

### FR-1 Session engine

- Maintain concurrent destination probes in local-only mode and concurrent pair sessions in server-assisted mode.
- Default probe cadence: `60s` in local-only mode and `500ms` in server-assisted mode.
- Compute rolling metrics at 1m, 5m, 1h, and 24h windows (plus 10s windows when server-assisted high-frequency mode is enabled).

### FR-2 Role negotiation

- Select client/server role per pair using explicit policy + fallback election.
- Support NAT-awareness hints and reachability tests.

### FR-3 Incident pipeline

- Convert threshold crossings into deduplicated incidents.
- Maintain incident state machine (`open`, `degraded`, `resolved`).

### FR-4 Local API for UI

- Expose read API on localhost for dashboards.
- Expose control API for config, tests, exports.

### FR-5 Evidence export

- One-click export for a date range and target link.
- Include raw samples, aggregates, config snapshot, and integrity hash manifest.

### FR-6 Backpressure safety

- Never allow upload failures to block local metric ingestion.
- Drop policy must be explicit and logged if disk limits are exceeded.

## Non-functional requirements

- Resource target (idle): <2% CPU, <200MB RAM on typical desktop/server.
- Probe precision: scheduling jitter <50ms at 500ms cadence (server-assisted), and stable minute-level execution in local-only mode.
- Durability: no data loss across process restarts for committed local samples.
- Security: signed binaries, signed updates, TLS everywhere, secret rotation.
- Portability: Linux, Windows, macOS, Docker.

## Suggested project layout

```text
FComT/
  cmd/
    FComT-agent/
  internal/
    config/
    session/
    probe/
    metrics/
    traceroute/
    snmp/
    speedtest/
    thresholds/
    incidents/
    export/
    storage/
    sync/
    api/
  web/
    ui/               # local web UI
  deployments/
    docker/
  docs/
```

## Example config (v1)

```yaml
agent:
  id: "site-hq-01"
  mode: "private_internet_server" # client_only | private_server | private_internet_server
  listen_udp: 23999
  listen_tcp: 23999

monitoring:
  profile: "local_only" # local_only | server_assisted
  local_probe_interval_s: 60
  server_probe_interval_ms: 500
  session_timeout_s: 30
  destinations:
    - name: "google-dns"
      host: "8.8.8.8"
    - name: "cloudflare-dns"
      host: "1.1.1.1"
    - name: "aws-us-east-1"
      host: "ec2.us-east-1.amazonaws.com"
    - name: "public-speedtest"
      host: "speedtest.tele2.net"

traceroute:
  enabled: true
  interval_s: 1
  max_hops: 32

snmp:
  enabled: true
  default_poll_s: 30
  devices:
    - name: "edge-fw"
      host: "192.168.1.1"
      version: "v3"

sla:
  profile: "isp-primary"
  latency_ms_max: 40
  jitter_ms_max: 10
  packet_loss_pct_max: 0.5
  availability_pct_min: 99.9

sync:
  remotes: []
  buffer_max_mb: 512
```

## SLA accountability workflow

1. Deploy agent at each relevant edge (home, branch, HQ, cloud).
2. Configure local destinations (Google/AWS/public speed-test targets), optional server peers, and ISP SLA objectives.
3. Run continuous synthetic tests (default 60s local-only cadence, optional 500ms with server-assisted mode) and periodic traceroutes.
4. Correlate link degradation with hop changes and device telemetry.
5. Generate monthly breach reports and attach evidence bundle to ISP tickets.

## MVP scope (first release)

- Go daemon with:
  - local-only synthetic probes at 60s default cadence against well-known public internet targets,
  - optional server-assisted 500ms UDP synthetic sessions for high granularity,
  - client/server session modes when server-assisted is enabled,
  - latency/jitter/loss/availability metrics,
  - traceroute sampling,
  - threshold incidents,
  - local REST API,
  - local web dashboard,
  - CSV/JSON evidence exports.
- Optional webhook sink for remote storage.

## Later phases

- SNMP auto-discovery and OID profile packs,
- signed PDF report generation,
- multi-tenant server coordinator,
- public benchmark destinations directory,
- anomaly detection and forecasting,
- mobile companion app.

## Local visualizer and example scenarios

The agent now includes:

- a built-in local visualizer at `/ui/`
- built-in example scenarios you can load from the UI or API

Example scenario API:

- list scenarios: `GET /v1/examples`
- seed scenario into in-memory data: `POST /v1/examples/seed?name=<scenario_name>`
- fetch graph-ready timeseries: `GET /v1/metrics/timeseries?target=<target>&window=168h`

Included scenario files:

- `internal/examples/testdata/local_only_minute_sla_breach.json`
- `internal/examples/testdata/server_assisted_high_granularity.json`
- `internal/examples/testdata/local_only_weekly_timeseries.json`
