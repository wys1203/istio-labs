# Istio Ingressgateway Monitoring Lab — Design

| | |
|---|---|
| **Date** | 2026-05-04 |
| **Owner** | wys1203 |
| **Target stack** | k8s 1.24.17, Istio 1.13.5 |
| **Repo** | `github.com/wys1203/istio-labs` (to be created) |
| **Status** | Approved through brainstorming; awaiting user spec review before implementation plan |

---

## 1. Goal

Build a self-contained kind-based lab that lets the user reproduce, observe, and alert on three real-world pain points seen on a production Istio 1.13.5 ingressgateway (IGW):

1. **CPU throttling on IGW** even when CPU usage looks moderate.
2. **IGW ↔ upstream connection-state visibility** (open / fail / destroy / length).
3. **"IGW healthy but upstream unstable"** scenarios where the upstream is a WebSocket service: IGW pod CPU/memory low, but real traffic is broken.

The lab is the place to **design and tune dashboards and alerts** that can later be lifted back to production.

### Non-goals

- App-side / sidecar mesh observability. The lab installs Istio without sidecar injection.
- A faithful replica of the user's whole prod topology (gRPC, HTTP, multi-tenant, etc.). Lab covers WS + a baseline HTTP echo only; design generalises to multi-cluster on dashboards.
- Long-term retention, cross-cluster federation, or SLO/SLI math.

## 2. Decision summary

Captured from the brainstorming session:

| # | Decision | Choice |
|---|---|---|
| 1 | Lab usage mode | (C) Dashboard/alert iteration **+** chaos toolkit for on-demand reproduction |
| 2 | Kind cluster | Separate cluster `istio-lab`, not co-located with `keda-lab` |
| 3 | Monitoring stack | (A) `kube-prometheus-stack` + dashboards/alerts written from scratch (no Istio official dashboards imported) |
| 4 | WebSocket upstream | (B) Custom Go service `ws-chaos` with admin-driven chaos modes |
| 5 | Alert delivery | (B) Alertmanager + in-cluster `webhook-logger` sink (no Slack) |
| 6 | `proxyStatsMatcher` scope | IGW pod-level annotation (`proxy.istio.io/config`), **not** `meshConfig.defaultConfig` — matches user's prod constraint where mesh-level config is platform-team owned |
| 7 | WS HTTP version | DR `h2UpgradePolicy: DO_NOT_UPGRADE` set **only on the `ws-chaos` DestinationRule**, no impact on gRPC/HTTP services on the same IGW |

## 3. Architecture

```
┌─────────────────────────────────────────────────────────────────────┐
│  kind cluster: istio-lab  (kindest/node:v1.24.17, 1 cp + 2 worker)  │
│                                                                     │
│  ┌─ ns: istio-system ──────┐   ┌─ ns: monitoring ────────────────┐  │
│  │  istiod (1.13.5)        │   │  kube-prometheus-stack v45.31.1 │  │
│  │  istio-ingressgateway   │   │   Prometheus / Alertmanager     │  │
│  │   - cpu limit tight     │   │   Grafana / kube-state-metrics  │  │
│  │   - replicas=2 (locked) │   │   node-exporter                 │  │
│  │   - podAnnotations:     │   │  webhook-logger (alert sink)    │  │
│  │     proxy.istio.io/     │   │  ServiceMonitor → IGW :15090    │  │
│  │     config              │   │  PrometheusRule (IGW alerts)    │  │
│  └─────────────────────────┘   │  Grafana sidecar dashboards     │  │
│                                └─────────────────────────────────┘  │
│  ┌─ ns: apps ──────────────────────────────────────────────┐        │
│  │  ws-chaos (Go, custom)   ← chaos modes via /admin/mode  │        │
│  │  http-echo (mendhak/...) ← baseline HTTP                │        │
│  └─────────────────────────────────────────────────────────┘        │
│  ┌─ ns: load ──────────────────────────────────────────────┐        │
│  │  ws-prober  (Go, custom) ← black-box WS prober          │        │
│  │  fortio                  ← HTTP load                    │        │
│  └─────────────────────────────────────────────────────────┘        │
│                                                                     │
│  Gateway / VirtualService:                                          │
│    /http*  → http-echo                                              │
│    /ws*    → ws-chaos                                               │
└─────────────────────────────────────────────────────────────────────┘
```

Data flow:

```
                    ┌─→ ws-chaos /metrics ────────────┐
                    │                                  │
ws-prober → IGW :80 ┤                                  ├─→ Prometheus
                    │   ┌─→ IGW envoy :15090 stats ────┤
fortio ───→ IGW :80 ┘   │                              │
                        ↓                              │
                    cAdvisor / kube-state ─────────────┘
                                                       │
                                                       ↓
                                                    Grafana ← ConfigMap
                                                              dashboards
                                                       │
                                                       ↓
                                                Alertmanager → webhook-logger
                                                                 ↓
                                                            kubectl logs -f
```

## 4. Components

### 4.1 Kind cluster

`kind/istio-lab.yaml`:

- Name: `istio-lab` (distinct from existing `keda-lab`)
- Node image: `kindest/node:v1.24.17`
- Topology: 1 control-plane + 2 worker
- `extraPortMappings`: container `30080→host 8080`, `30443→host 8443` so the IGW NodePort is reachable from the host (optional; cluster-internal generators don't need this)
- Worker nodes labelled `role=app`

### 4.2 Istio 1.13.5

Bootstrap script `scripts/bootstrap-istio.sh` downloads `istioctl 1.13.5` to `bin/istio-1.13.5/bin/istioctl` (gitignored). All Make targets reference this binary; the user's system `istioctl 1.28.3` is not used.

`istio/operator.yaml` (IstioOperator):

```yaml
spec:
  profile: minimal
  components:
    pilot:
      enabled: true
    ingressGateways:
    - name: istio-ingressgateway
      enabled: true
      k8s:
        replicaCount: 2
        hpaSpec:
          minReplicas: 2
          maxReplicas: 2          # locked, do not let HPA mask throttling demos
        resources:
          requests: {cpu: 100m, memory: 128Mi}
          limits:   {cpu: 300m, memory: 256Mi}    # tight on purpose (open question, see §11)
        podDisruptionBudget:
          minAvailable: 1
        service:
          ports:
          - {name: http2,           port: 80,    targetPort: 8080,  nodePort: 30080}
          - {name: https,           port: 443,   targetPort: 8443,  nodePort: 30443}
          - {name: http-envoy-prom, port: 15090, targetPort: 15090}    # explicit, for ServiceMonitor
        podAnnotations:
          proxy.istio.io/config: |
            proxyStatsMatcher:
              inclusionRegexps:
              - "cluster\\..*\\.upstream_cx_.*"
              - "cluster\\..*\\.upstream_rq_.*"
              - "cluster\\..*\\.outlier_detection\\..*"
              - "cluster\\..*\\.health_check\\..*"
              - "cluster\\..*\\.membership_.*"
              - "cluster\\..*\\.circuit_breakers\\..*"
              inclusionPrefixes:
              - "listener"
              - "http"
              - "server"
```

No sidecar injection on `apps` / `load` namespaces.

`istio/gateway.yaml`:

- `Gateway` on hosts `*`, port 80 HTTP
- `VirtualService` routes:
  - prefix `/http` → `http-echo.apps.svc.cluster.local:80`
  - prefix `/ws`   → `ws-chaos.apps.svc.cluster.local:8080`
- `DestinationRule` for `ws-chaos`:
  ```yaml
  trafficPolicy:
    connectionPool:
      tcp:  {connectTimeout: 5s, maxConnections: 1024}
      http: {idleTimeout: 60s, h2UpgradePolicy: DO_NOT_UPGRADE}
    outlierDetection:
      consecutive5xxErrors: 5
      interval: 10s
      baseEjectionTime: 30s
      maxEjectionPercent: 50
  ```
  Outlier detection enabled so the corresponding alert / dashboard panel actually trigger under chaos.

### 4.3 Sample apps

#### `ws-chaos` (Go)

Single binary (~250 LOC), built via `make build`, loaded into kind via `kind load docker-image`. Dependencies: `gorilla/websocket`, `prometheus/client_golang`.

Endpoints:

| Method | Path | Purpose |
|---|---|---|
| GET | `/healthz` | k8s liveness |
| GET | `/metrics` | Prometheus exposition |
| GET | `/ws` | WebSocket endpoint (behaviour controlled by current mode) |
| GET | `/admin/mode` | current mode JSON |
| POST | `/admin/mode` | switch mode without restarting (existing connections keep their mode; new ones use the new mode) |

Modes:

| mode | Behaviour | Simulates |
|---|---|---|
| `normal` | echo + 30s heartbeat | baseline |
| `idle-hang` | upgrade succeeds, then no further messages or pongs | upstream stuck, connection alive but functionally dead |
| `slow-close` | sleep `delay` then close | upstream slow response |
| `random-rst` | `tcpConn.SetLinger(0)` + Close → TCP RST, ratio `ratio` | upstream resets |
| `drop-after` | proactively close after `seconds` | upstream idle timeout |
| `cpu-burn` | CPU loop after upgrade | upstream stuck busy |
| `reject-upgrade` | return 503 on `Upgrade: websocket` | upgrade failure |

App-side metrics:

- `wschaos_active_connections`
- `wschaos_messages_total{direction="rx"|"tx"}`
- `wschaos_close_total{reason="normal"|"rst"|"timeout"|"reject"}`

Service `:8080` (HTTP/WS) + `:8080/metrics`.

#### `http-echo`

Off-the-shelf image `mendhak/http-https-echo:31`. Pure baseline. No chaos.

#### `ws-prober` (Go)

Small binary (~100 LOC) running in `load` namespace. Behaviour:

- Maintain `N=10` concurrent WebSocket connections to `http://istio-ingressgateway.istio-system/ws`
- On each connection, send ping every 5s, expect echo
- Reconnect on close (with exponential backoff, capped)

Metrics:

- `wsprober_active_connections`
- `wsprober_attempts_total` / `wsprober_failures_total` (loss ratio basis)
- `wsprober_rtt_seconds_bucket` (histogram)
- `wsprober_close_total{reason}`
- `wsprober_tcp_errors_total`

Service `:9100/metrics`.

#### `fortio`

Off-the-shelf image. Used by `chaos-throttle-cpu` to push HTTP load against IGW.

### 4.4 Monitoring stack

#### kube-prometheus-stack

Helm chart `prometheus-community/kube-prometheus-stack` version **`45.31.1`** (last 45.x; preserves k8s 1.24 compatibility). `monitoring/values-kps.yaml` overrides:

- `grafana.sidecar.dashboards.enabled: true`, `label: grafana_dashboard`, `searchNamespace: ALL`
- `grafana.adminPassword: admin`
- `prometheus.prometheusSpec.ruleSelectorNilUsesHelmValues: false`
- `prometheus.prometheusSpec.serviceMonitorSelectorNilUsesHelmValues: false`
- `prometheus.prometheusSpec.retention: 6h`
- `alertmanager.config.route.receiver: webhook-logger` (see §6.5)
- `kubeProxy.enabled: false`, `kubeEtcd.enabled: false`, `kubeControllerManager.enabled: false`, `kubeScheduler.enabled: false` (kind-related noise)
- `defaultRules.disabled`: silence alerts that misfire on kind (e.g. `KubeControllerManagerDown`)

#### ServiceMonitors

| Target | Selector | Endpoint | Interval |
|---|---|---|---|
| IGW Envoy | ns `istio-system`, `app=istio-ingressgateway` | `http-envoy-prom :15090 /stats/prometheus` | 15s |
| ws-chaos | ns `apps`, `app=ws-chaos` | `:8080 /metrics` | 15s |
| ws-prober | ns `load`, `app=ws-prober` | `:9100 /metrics` | 15s |
| webhook-logger | ns `monitoring`, `app=webhook-logger` | `:9095 /metrics` | 30s |

IGW ServiceMonitor includes a `metricRelabelings` keep-only filter on `envoy_(cluster|listener|http|server)_.*` to prevent cardinality blow-up after the proxyStatsMatcher widening.

#### webhook-logger

Image: `ghcr.io/tomtom-international/alertmanager-webhook-logger:1.6.0` (pinned; MIT). Deployment exposes:

- `:8080/log`  — Alertmanager webhook receiver (configurable via `--listen-address=:8080` flag)
- `:9095/metrics` — Prometheus exposition (`alerts_received_total`)

Output via `kubectl logs -f deploy/webhook-logger | jq`. Self-monitored via `AlertmanagerWebhookSinkDown` rule (§6.4).

## 5. Dashboards

Three dashboards, delivered as ConfigMaps with label `grafana_dashboard=1`. All use template variables `pod` (IGW pod) and `cluster` (envoy upstream cluster).

### Dashboard 01 — `IGW Health` → pain point #1

| Row | Panels |
|---|---|
| Saturation | CFS Throttling Rate (`rate(container_cpu_cfs_throttled_periods_total)/rate(container_cpu_cfs_periods_total)`); Throttled Time; CPU Usage vs Limit; Memory vs Limit; Pod Restarts |
| Envoy Workers | Worker thread CPU; concurrency setting |
| Listener-level | Downstream cx active; cx rate; Active HTTP upgrades (`envoy_http_downstream_cx_upgrades_active`) |

CFS Throttling Rate is the headline panel with thresholds: green < 0.05, yellow 0.05–0.2, red > 0.2.

### Dashboard 02 — `IGW ↔ Upstream` → pain point #2

| Row | Panels |
|---|---|
| Connection Lifecycle | Active upstream cx; open rate; failed rate; **destroy by side (local vs remote, side-by-side)**; cx length p50/p95/p99 (`histogram_quantile(0.95, rate(envoy_cluster_upstream_cx_length_ms_bucket[5m]))`) |
| Request-level | RQ rate by code class; RQ active; RQ time p50/p95/p99; pending |
| Health & Outlier | Healthy member ratio (`envoy_cluster_membership_healthy / _total`); outlier ejections active; ejection events by reason; circuit breaker remaining |

Local-vs-remote destroy panel is the headline.

### Dashboard 03 — `IGW WebSocket` → pain point #3 (招牌)

Headline row uses three side-by-side columns:

```
┌── Envoy view ────┐  ┌── App view ─────┐  ┌── User view (prober) ──┐
│ cx_active        │  │ wschaos_active   │  │ wsprober_active        │
│ cx_destroy_*     │  │ wschaos_close_   │  │ wsprober_rtt_p95       │
│ cx_length p99    │  │   total{reason}  │  │ wsprober_loss_ratio    │
└──────────────────┘  └──────────────────┘  └────────────────────────┘
```

Subsequent rows:

| Row | Panels |
|---|---|
| HTTP Upgrade | Active upgrades; upgrade success rate; 101 rate; 4xx/5xx upgrade response rate |
| Byte-level | Upstream RX/TX bytes/s (`rate(envoy_cluster_upstream_cx_rx_bytes_total)`, tx); buffered bytes |
| Black-box SLI | Probe RTT quantiles; probe loss ratio; probe abnormal close rate; probe TCP errors |

The byte-level row + `cx_active` row in the headline are the **`IGWWSStaleConnections` signature** visualisation.

## 6. Alerts

Delivered as `PrometheusRule` resources. All alerts carry labels `pain_point=1|2|3` and `dashboard=01|02|03` for routing/correlation. Severity ∈ `{warning, critical}`.

### 6.1 Group `igw-saturation` (pain #1)

| Alert | Expression (simplified) | for | severity |
|---|---|---|---|
| `IGWCPUThrottlingWarn` | `rate(container_cpu_cfs_throttled_periods_total{pod=~"istio-ingressgateway.*"}[1m]) / rate(container_cpu_cfs_periods_total{...}[1m]) > 0.05` | 1m | warning |
| `IGWCPUThrottlingCritical` | same expr `> 0.2` | 1m | critical |
| `IGWNearCPULimit` | `rate(container_cpu_usage_seconds_total{...}[1m]) / on(pod) kube_pod_container_resource_limits{resource="cpu",...} > 0.8` | 5m | warning |
| `IGWMemoryNearLimit` | working set / limit > 0.85 | 5m | warning |
| `IGWPodRestarted` | `increase(kube_pod_container_status_restarts_total{...}[5m]) > 0` | 0m | warning |

### 6.2 Group `igw-upstream` (pain #2)

| Alert | Expression | for | severity |
|---|---|---|---|
| `IGWUpstreamConnectFail` | `rate(envoy_cluster_upstream_cx_connect_fail_total[1m]) > 1` | 1m | warning |
| `IGWUpstreamRemoteResetSurge` | remote destroy rate > 5× local destroy rate AND remote rate > 0.5/s | 2m | warning |
| `IGWUpstreamCxLengthDrop` | `histogram_quantile(0.5, …cx_length_ms_bucket[5m])` < 0.5× same expression with `offset 10m` | 2m | warning |
| `IGWUpstreamUnhealthy` | `membership_healthy / membership_total < 0.5` | 1m | critical |
| `IGWUpstreamOutlierEjection` | `outlier_detection_ejections_active > 0` | 1m | warning |
| `IGWUpstreamCircuitBreakerOpen` | `circuit_breakers_default_remaining_cx == 0` | 1m | critical |

### 6.3 Group `igw-websocket` (pain #3)

| Alert | Expression | for | severity |
|---|---|---|---|
| `IGWWSUpgradeFailRate` | 4xx/5xx rate / total rate on `ws-chaos` cluster > 0.1 | 2m | warning |
| **`IGWWSStaleConnections`** | `cx_active{...ws-chaos} > 5` AND `(rate(rx_bytes) + rate(tx_bytes)) < 100` | 2m | critical |
| `IGWWSProbeLossHigh` | `wsprober_failures / wsprober_attempts > 0.1` | 2m | critical |
| `IGWWSProbeRTTHigh` | `histogram_quantile(0.95, wsprober_rtt_seconds_bucket[5m]) > 0.5` | 2m | warning |
| `IGWWSAppCloseAbnormal` | `rate(wschaos_close_total{reason!="normal"}[2m]) > 0.5` | 2m | warning |

`IGWWSStaleConnections` is the lab's signature alert.

### 6.4 Group `igw-pipeline-health` (self-monitor)

| Alert | Expression | for | severity |
|---|---|---|---|
| `IGWScrapeDown` | `up{job="istio-ingressgateway"} == 0` | 2m | critical |
| `WSChaosScrapeDown` | `up{job="ws-chaos"} == 0` | 2m | warning |
| `WSProberScrapeDown` | `up{job="ws-prober"} == 0` | 2m | warning |
| `AlertmanagerWebhookSinkDown` | `up{job="webhook-logger"} == 0` | 2m | warning |

### 6.5 Alertmanager routing

```yaml
route:
  receiver: webhook-logger
  group_by: [alertname, pain_point]
  group_wait: 10s
  group_interval: 30s
  repeat_interval: 5m
  routes:
  - matchers: [severity="critical"]
    receiver: webhook-logger
    repeat_interval: 1m
receivers:
- name: webhook-logger
  webhook_configs:
  - url: http://webhook-logger.monitoring.svc.cluster.local:8080/log
    send_resolved: true
```

### 6.6 Verification matrix (`make verify`)

`scripts/verify.sh` is also the lab's acceptance test. Each step must fire its expected alert(s) within 5 minutes and resolve after `chaos-reset`:

| Step | Chaos action | Expected fired alerts | Expected dashboard signature |
|---|---|---|---|
| 1 | `chaos-throttle-cpu` + fortio | `IGWCPUThrottlingWarn`, `IGWCPUThrottlingCritical`, `IGWNearCPULimit` | 01 throttling row reds |
| 2 | `chaos-ws-rst` ratio=0.5 | `IGWUpstreamRemoteResetSurge` | 02 destroy_remote spike |
| 3 | `chaos-ws-drop` 30s | `IGWUpstreamCxLengthDrop` | 02 cx_length p50 drops |
| 4 | `chaos-kill-upstream` | `IGWUpstreamUnhealthy`, `IGWUpstreamConnectFail`, `IGWUpstreamOutlierEjection` | 02 membership_healthy=0 |
| 5 | `chaos-reject-upgrade` | `IGWWSUpgradeFailRate` | 03 upgrade row 5xx |
| 6 | `chaos-ws-idle` | **`IGWWSStaleConnections`**, `IGWWSProbeLossHigh` | 03 byte-level flatlines while cx_active stable |
| 7 | `chaos-ws-cpu` | `IGWWSProbeRTTHigh`, `IGWWSAppCloseAbnormal` | 03 RTT row spikes |
| 8 | `chaos-reset` | all firing alerts move to resolved | — |

## 7. Chaos toolkit

### 7.1 Make targets

| Target | Action | Pain point |
|---|---|---|
| `make chaos-throttle-cpu` | `kubectl patch` IGW limit to `100m` + start fortio at high QPS | #1 |
| `make chaos-ws-idle` | POST `/admin/mode` `{mode: idle-hang}` | #3 |
| `make chaos-ws-rst` | POST `/admin/mode` `{mode: random-rst, ratio: 0.5}` | #2 |
| `make chaos-ws-drop` | POST `/admin/mode` `{mode: drop-after, seconds: 30}` | #2 |
| `make chaos-ws-cpu` | POST `/admin/mode` `{mode: cpu-burn}` | #3 |
| `make chaos-reject-upgrade` | POST `/admin/mode` `{mode: reject-upgrade}` | upgrade fail |
| `make chaos-kill-upstream` | `kubectl scale --replicas=0 deploy/ws-chaos` | #2 |
| `make chaos-reset` | restore IGW limit, scale ws-chaos back, mode=normal, stop fortio | — |

Each script prints to stderr the alert(s) it should trigger and the dashboard panel(s) to inspect — making the script self-documenting.

### 7.2 Targets that act on cluster state

`chaos-throttle-cpu` and `chaos-kill-upstream` mutate k8s objects rather than ws-chaos admin endpoints. `chaos-reset` undoes all of them idempotently.

## 8. DX & Makefile

### 8.1 Top-level commands

```
make                     # help, parsed from `## ...` annotations on each target
make up                  # cluster + istio + apps + monitoring + load (full stack)
make down                # tear down kind cluster
make reset               # down + up (clean state)
make status              # print cluster health + dashboard URLs
make verify              # run scripts/verify.sh (the matrix)
make build               # build & kind-load ws-chaos / ws-prober images
make grafana             # port-forward 3000 + open browser
make prometheus          # port-forward 9090
make alertmanager        # port-forward 9093
make logs-alerts         # tail webhook-logger logs through jq
make chaos-help          # list chaos targets
```

Granular install targets (`install-cluster`, `install-istio`, `install-apps`, `install-monitoring`, `install-load`) are composed into `make up`.

### 8.2 Bootstrap

`scripts/bootstrap-istio.sh` downloads istioctl 1.13.5 to `bin/istio-1.13.5/bin/istioctl`. `bin/` is gitignored. No system-wide install required.

### 8.3 Repo layout

```
istio-labs/
├── CLAUDE.md                  # already present
├── README.md
├── Makefile
├── .gitignore
├── kind/
│   └── istio-lab.yaml
├── istio/
│   ├── operator.yaml
│   └── gateway.yaml
├── apps/
│   ├── ws-chaos/
│   │   ├── go.mod
│   │   ├── main.go
│   │   ├── Dockerfile
│   │   └── manifest.yaml
│   ├── http-echo/manifest.yaml
│   └── load/
│       ├── ws-prober/
│       │   ├── go.mod
│       │   ├── main.go
│       │   ├── Dockerfile
│       │   └── manifest.yaml
│       └── fortio/manifest.yaml
├── monitoring/
│   ├── values-kps.yaml
│   ├── servicemonitors.yaml
│   ├── webhook-logger.yaml
│   ├── alerts/
│   │   ├── igw-saturation.yaml
│   │   ├── igw-upstream.yaml
│   │   ├── igw-websocket.yaml
│   │   └── igw-pipeline-health.yaml
│   └── dashboards/
│       ├── 01-igw-health.json
│       ├── 02-igw-upstream.json
│       └── 03-igw-websocket.json
├── scripts/
│   ├── bootstrap-istio.sh
│   ├── up.sh / down.sh
│   ├── verify.sh
│   ├── chaos-*.sh   (8 files)
│   └── lib.sh
└── docs/
    └── superpowers/specs/
        └── 2026-05-04-istio-igw-monitoring-lab-design.md   # this file
```

### 8.4 README sections

1. Pre-requisites
2. Quick start (`make up && make verify && make grafana`)
3. Topology diagram
4. Pain points addressed (one section each, linking to dashboard + alert + chaos target)
5. Why pod-level proxyStatsMatcher (not meshConfig)
6. Verification matrix
7. Repo layout
8. Porting back to prod (lift-as-is vs retune list)
9. Troubleshooting
10. Lab-vs-prod gap callouts

## 9. GitHub repo & integration

1. `git init` in `istio-labs/`
2. Initial commit: this design doc + `CLAUDE.md` + `.gitignore`
3. `gh repo create wys1203/istio-labs --public --source=. --remote=origin --push`
4. Subsequent commits follow the implementation plan, one commit per phase
5. No CI in the initial cut (out of scope; can be added later via a follow-up)

`.gitignore` includes:
- `bin/` (downloaded istioctl)
- `*.local.yaml` (user-only override file)
- `grafana-data/` (if any)
- `coverage.out`, `*.test`
- macOS / editor cruft (`.DS_Store`, `.idea/`, `.vscode/`)

## 10. Known lab-vs-prod gaps (called out in README)

- Alert thresholds (5% / 20% throttling, 0.1 loss ratio, etc.) are lab-tuned for fast triggering. Each carries a `(lab)` annotation reminding to retune.
- IGW replicas locked at 2 with HPA disabled. Prod must keep HPA.
- proxyStatsMatcher widening can blow up cardinality on a busy mesh; the lab's regex set is a starting point, not a copy-paste destination.
- ws-prober at N=10 connections is a lab heuristic; prod needs sizing per SLO.
- DR `outlierDetection` enabled to demo the panel; prod values should follow your existing policy.

## 11. Open items

These were touched on during brainstorming but the user deferred final confirmation. Defaults below will be used unless overridden before plan execution:

1. **IGW resource limits.** Default `cpu: 300m / mem: 256Mi` with `replicas=2` locked. Tighter (e.g., `cpu: 150m`) makes throttling demos more dramatic; looser (e.g., `cpu: 1` / `mem: 1Gi`) more like prod baseline.
2. **GitHub repo visibility.** Default `--public`. If user prefers `--private`, single flag flip in `gh repo create`.

## 12. Out of scope (current lab)

- Tracing (Jaeger / Zipkin). Adds value but not required by the three pain points.
- Kiali. Mesh-graph view, but lab doesn't deploy sidecars so its utility is low.
- mTLS. IGW upstream is plaintext in the lab.
- Multi-cluster, multi-tenant, federated Prometheus.
- gRPC service example. The dashboards filter by cluster name and generalise; a gRPC example can be added later as `make install-grpc-echo`.
- Authn/authz at IGW.
- CI/GitHub Actions.

---

**Next step**: user reviews this spec. On approval, transition to `superpowers:writing-plans` to produce the phased implementation plan.
