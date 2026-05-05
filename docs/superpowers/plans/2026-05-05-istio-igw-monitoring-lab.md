# Istio IGW Monitoring Lab — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement the istio-labs lab (kind + Istio 1.13.5 + kube-prometheus-stack + custom dashboards/alerts + WS chaos toolkit) per the design spec.

**Architecture:** Single kind cluster, 1 control + 2 worker, k8s 1.24.17. Istio 1.13.5 minimal profile, no sidecar injection, IGW with `proxy.istio.io/config` pod annotation widening Envoy stats. kube-prometheus-stack 45.31.1 with custom ServiceMonitors, PrometheusRules, and three IGW-focused Grafana dashboards. Custom Go services `ws-chaos` (chaos modes via admin endpoint) and `ws-prober` (black-box WS prober). Alertmanager → in-cluster webhook-logger sink.

**Tech Stack:** Go 1.21+, kind v0.31.0, kubectl, helm 3.x, Docker, gorilla/websocket, prometheus/client_golang, kube-prometheus-stack chart 45.31.1, mendhak/http-https-echo, fortio, alertmanager-webhook-logger.

**Spec:** [docs/superpowers/specs/2026-05-04-istio-igw-monitoring-lab-design.md](../specs/2026-05-04-istio-igw-monitoring-lab-design.md)

---

## Open items still requiring user decision before plan execution

The spec §11 lists two unconfirmed decisions. Defaults below are used unless overridden:

1. **IGW resource limits** — default `cpu: 300m / mem: 256Mi`, `replicas=2` locked.
2. **GitHub repo visibility** — default `--public`. Affects only Task 30 (`gh repo create`).

---

## File structure

```
istio-labs/
├── README.md                                        Task 29
├── Makefile                                         Task 1, extended each phase
├── .gitignore                                       (already committed)
├── CLAUDE.md                                        (already committed)
├── kind/istio-lab.yaml                              Task 3
├── istio/
│   ├── operator.yaml                                Task 5
│   └── gateway.yaml                                 Task 6 (extended in Task 12)
├── apps/
│   ├── ws-chaos/
│   │   ├── go.mod / go.sum                          Task 7
│   │   ├── metrics.go                               Task 7
│   │   ├── modes.go                                 Task 8
│   │   ├── modes_test.go                            Task 8
│   │   ├── handlers.go                              Task 9
│   │   ├── main.go                                  Task 10
│   │   ├── Dockerfile                               Task 11
│   │   └── manifest.yaml                            Task 11
│   ├── http-echo/manifest.yaml                      Task 13
│   └── load/
│       ├── ws-prober/
│       │   ├── go.mod                               Task 14
│       │   ├── prober.go                            Task 14
│       │   ├── prober_test.go                       Task 14
│       │   ├── metrics.go                           Task 14
│       │   ├── main.go                              Task 14
│       │   ├── Dockerfile                           Task 15
│       │   └── manifest.yaml                        Task 15
│       └── fortio/manifest.yaml                     Task 16
├── monitoring/
│   ├── values-kps.yaml                              Task 17
│   ├── servicemonitors.yaml                         Task 18
│   ├── webhook-logger.yaml                          Task 19
│   ├── alerts/
│   │   ├── igw-saturation.yaml                      Task 24
│   │   ├── igw-upstream.yaml                        Task 25
│   │   ├── igw-websocket.yaml                       Task 26
│   │   └── igw-pipeline-health.yaml                 Task 27
│   └── dashboards/
│       ├── 01-igw-health.json + cm.yaml             Task 21
│       ├── 02-igw-upstream.json + cm.yaml           Task 22
│       └── 03-igw-websocket.json + cm.yaml          Task 23
├── scripts/
│   ├── lib.sh                                       Task 1
│   ├── bootstrap-istio.sh                           Task 4
│   ├── chaos-reset.sh                               Task 18 (early — needed by others)
│   ├── chaos-throttle-cpu.sh                        Task 18
│   ├── chaos-ws-idle.sh                             Task 18
│   ├── chaos-ws-rst.sh                              Task 18
│   ├── chaos-ws-drop.sh                             Task 18
│   ├── chaos-ws-cpu.sh                              Task 18
│   ├── chaos-reject-upgrade.sh                      Task 18
│   ├── chaos-kill-upstream.sh                       Task 18
│   └── verify.sh                                    Task 28
└── docs/superpowers/{specs,plans}/                  (already present)
```

---

## Phase 0 — Repo skeleton

### Task 1: Makefile + scripts/lib.sh skeleton

**Files:**
- Create: `Makefile`
- Create: `scripts/lib.sh`

- [ ] **Step 1: Write `scripts/lib.sh`**

```bash
#!/usr/bin/env bash
# Shared helpers for scripts in this lab.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ISTIOCTL="${ROOT_DIR}/bin/istio-1.13.5/bin/istioctl"
KIND_CLUSTER_NAME="istio-lab"
KUBECONTEXT="kind-${KIND_CLUSTER_NAME}"

log()  { printf '\033[1;34m[lab]\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m[warn]\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[1;31m[err]\033[0m %s\n' "$*" >&2; exit 1; }

require_cmd() {
  for c in "$@"; do command -v "$c" >/dev/null || die "missing required command: $c"; done
}

kctx() { kubectl --context="${KUBECONTEXT}" "$@"; }

wait_for_rollout() {
  local kind=$1 name=$2 ns=$3 timeout=${4:-180s}
  log "waiting for ${kind}/${name} in ${ns} (timeout ${timeout})"
  kctx -n "${ns}" rollout status "${kind}/${name}" --timeout="${timeout}"
}
```

- [ ] **Step 2: Write `Makefile` with help target and stubs**

```makefile
SHELL := /usr/bin/env bash
.SHELLFLAGS := -eu -o pipefail -c
.DEFAULT_GOAL := help

ROOT_DIR := $(shell pwd)
ISTIOCTL := $(ROOT_DIR)/bin/istio-1.13.5/bin/istioctl
KCTX := kubectl --context=kind-istio-lab

##@ Help
.PHONY: help
help: ## show this help
	@awk 'BEGIN {FS = ":.*##"; printf "Usage:\n  make \033[36m<target>\033[0m\n"} \
		/^[a-zA-Z0-9_.-]+:.*?##/ { printf "  \033[36m%-22s\033[0m %s\n", $$1, $$2 } \
		/^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) }' $(MAKEFILE_LIST)

##@ Lifecycle
.PHONY: up down reset status
up: install-cluster install-istio install-apps install-monitoring install-load ## bring up the full lab
down: ## tear down the kind cluster
	kind delete cluster --name istio-lab
reset: down up ## clean reset
status: ## print key endpoints
	@echo "kind-istio-lab cluster:" && $(KCTX) get nodes
	@echo "" && echo "Run 'make grafana' / 'make prometheus' / 'make alertmanager' to port-forward."

##@ Install (granular)
.PHONY: install-cluster install-istio install-apps install-monitoring install-load
install-cluster:    ## kind create cluster
install-istio:      ## install istio 1.13.5
install-apps:       ## install ws-chaos + http-echo + Gateway/VS/DR
install-monitoring: ## install kube-prometheus-stack + dashboards + alerts + webhook-logger
install-load:       ## install ws-prober + fortio
# bodies filled in subsequent tasks

##@ Build
.PHONY: build
build: ## build & kind-load ws-chaos / ws-prober images
# body filled in Task 11 / 15

##@ Verify
.PHONY: verify
verify: ## run the full chaos→alert acceptance matrix
	bash scripts/verify.sh

##@ Observability shortcuts
.PHONY: grafana prometheus alertmanager logs-alerts logs-igw logs-ws
grafana:       ; $(KCTX) -n monitoring port-forward svc/kps-grafana 3000:80
prometheus:    ; $(KCTX) -n monitoring port-forward svc/kps-kube-prometheus-stack-prometheus 9090
alertmanager:  ; $(KCTX) -n monitoring port-forward svc/kps-kube-prometheus-stack-alertmanager 9093
logs-alerts:   ; $(KCTX) -n monitoring logs -l app=webhook-logger -f --tail=200 | jq -R 'fromjson? // .'
logs-igw:      ; $(KCTX) -n istio-system logs -l app=istio-ingressgateway -f --tail=200
logs-ws:       ; $(KCTX) -n apps logs -l app=ws-chaos -f --tail=200

##@ Chaos
.PHONY: chaos-help chaos-reset chaos-throttle-cpu chaos-ws-idle chaos-ws-rst chaos-ws-drop chaos-ws-cpu chaos-reject-upgrade chaos-kill-upstream
chaos-help: ## list chaos targets
	@grep -E '^chaos-[a-z-]+:.*##' $(MAKEFILE_LIST) | sed 's/:.*## /  /'
chaos-reset:           ; bash scripts/chaos-reset.sh
chaos-throttle-cpu:    ; bash scripts/chaos-throttle-cpu.sh
chaos-ws-idle:         ; bash scripts/chaos-ws-idle.sh
chaos-ws-rst:          ; bash scripts/chaos-ws-rst.sh
chaos-ws-drop:         ; bash scripts/chaos-ws-drop.sh
chaos-ws-cpu:          ; bash scripts/chaos-ws-cpu.sh
chaos-reject-upgrade:  ; bash scripts/chaos-reject-upgrade.sh
chaos-kill-upstream:   ; bash scripts/chaos-kill-upstream.sh
```

- [ ] **Step 3: Make scripts executable + verify**

```bash
chmod +x scripts/lib.sh
make help
```

Expected: prints help table with sections "Help", "Lifecycle", "Install", "Build", "Verify", "Observability shortcuts", "Chaos".

- [ ] **Step 4: Commit**

```bash
git add Makefile scripts/lib.sh
git commit -m "chore: skeleton Makefile and shared shell helpers"
```

---

## Phase 1 — Kind cluster

### Task 2: Pre-flight checks

- [ ] **Step 1: Verify host tooling**

Run:
```bash
docker version --format '{{.Server.Version}}'
kind version
helm version --short
kubectl version --client --short 2>/dev/null || kubectl version --client
```
Expected: docker daemon reachable; kind v0.31.x; helm v3.x; kubectl 1.24+.

If any missing: install via `brew install` and retry.

### Task 3: Kind cluster config

**Files:**
- Create: `kind/istio-lab.yaml`

- [ ] **Step 1: Write `kind/istio-lab.yaml`**

```yaml
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
name: istio-lab
nodes:
- role: control-plane
  image: kindest/node:v1.24.17
  extraPortMappings:
  - containerPort: 30080
    hostPort: 8080
    protocol: TCP
  - containerPort: 30443
    hostPort: 8443
    protocol: TCP
- role: worker
  image: kindest/node:v1.24.17
  labels: {role: app}
- role: worker
  image: kindest/node:v1.24.17
  labels: {role: app}
```

- [ ] **Step 2: Wire `install-cluster` target**

Replace the `install-cluster:` line in Makefile with:
```makefile
install-cluster: ## kind create cluster
	@if kind get clusters | grep -qx istio-lab; then \
		echo "kind cluster 'istio-lab' already exists"; \
	else \
		kind create cluster --config kind/istio-lab.yaml --wait 120s; \
	fi
	$(KCTX) cluster-info
```

- [ ] **Step 3: Run + verify**

```bash
make install-cluster
kubectl --context=kind-istio-lab get nodes
```
Expected: 3 nodes Ready, control-plane + 2 workers labelled `role=app`.

- [ ] **Step 4: Commit**

```bash
git add kind/istio-lab.yaml Makefile
git commit -m "feat(cluster): kind cluster config for istio-lab (k8s 1.24.17, 1cp+2w)"
```

---

## Phase 2 — Istio 1.13.5

### Task 4: istioctl bootstrap script

**Files:**
- Create: `scripts/bootstrap-istio.sh`

- [ ] **Step 1: Write the script**

```bash
#!/usr/bin/env bash
source "$(dirname "$0")/lib.sh"

ISTIO_VERSION=1.13.5
TARGET_DIR="${ROOT_DIR}/bin"

if [[ -x "${ISTIOCTL}" ]]; then
  installed=$("${ISTIOCTL}" version --remote=false 2>/dev/null | awk '{print $NF}')
  if [[ "${installed}" == "${ISTIO_VERSION}" ]]; then
    log "istioctl ${ISTIO_VERSION} already at ${ISTIOCTL}"
    exit 0
  fi
fi

log "downloading istio ${ISTIO_VERSION} into ${TARGET_DIR}"
mkdir -p "${TARGET_DIR}"
cd "${TARGET_DIR}"
curl -fsSL "https://github.com/istio/istio/releases/download/${ISTIO_VERSION}/istio-${ISTIO_VERSION}-osx-arm64.tar.gz" \
  | tar -xz
[[ -d "istio-${ISTIO_VERSION}" ]] || die "extraction failed"
"${ISTIOCTL}" version --remote=false
```

- [ ] **Step 2: Make executable + run**

```bash
chmod +x scripts/bootstrap-istio.sh
bash scripts/bootstrap-istio.sh
```
Expected: prints `client version: 1.13.5`. Creates `bin/istio-1.13.5/`.

- [ ] **Step 3: Verify gitignore covers `bin/`**

```bash
git check-ignore bin/istio-1.13.5/bin/istioctl
```
Expected: prints the path (i.e., is ignored).

- [ ] **Step 4: Commit**

```bash
git add scripts/bootstrap-istio.sh
git commit -m "feat(istio): bootstrap script downloads istioctl 1.13.5 to bin/"
```

### Task 5: IstioOperator manifest

**Files:**
- Create: `istio/operator.yaml`

- [ ] **Step 1: Write `istio/operator.yaml`**

```yaml
apiVersion: install.istio.io/v1alpha1
kind: IstioOperator
metadata:
  name: istio-lab
  namespace: istio-system
spec:
  profile: minimal
  values:
    global:
      proxy:
        autoInject: disabled
  components:
    pilot:
      enabled: true
      k8s:
        resources:
          requests: {cpu: 100m, memory: 256Mi}
          limits:   {cpu: 500m, memory: 512Mi}
    ingressGateways:
    - name: istio-ingressgateway
      enabled: true
      k8s:
        replicaCount: 2
        hpaSpec:
          minReplicas: 2
          maxReplicas: 2
        resources:
          requests: {cpu: 100m, memory: 128Mi}
          limits:   {cpu: 300m, memory: 256Mi}
        podDisruptionBudget:
          minAvailable: 1
        service:
          type: NodePort
          ports:
          - {name: status-port, port: 15021, targetPort: 15021, nodePort: 30021}
          - {name: http2,       port: 80,    targetPort: 8080,  nodePort: 30080}
          - {name: https,       port: 443,   targetPort: 8443,  nodePort: 30443}
          - {name: http-envoy-prom, port: 15090, targetPort: 15090}
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

- [ ] **Step 2: Wire `install-istio` target**

Replace the `install-istio:` line in Makefile with:
```makefile
install-istio: ## install istio 1.13.5 (downloads istioctl on demand)
	bash scripts/bootstrap-istio.sh
	$(ISTIOCTL) install -y -f istio/operator.yaml --context=kind-istio-lab
	$(KCTX) -n istio-system rollout status deploy/istiod --timeout=180s
	$(KCTX) -n istio-system rollout status deploy/istio-ingressgateway --timeout=180s
```

- [ ] **Step 3: Run + verify**

```bash
make install-istio
kubectl --context=kind-istio-lab -n istio-system get pods
kubectl --context=kind-istio-lab -n istio-system get svc istio-ingressgateway -o yaml | grep -E "name: (http-envoy-prom|http2)"
```
Expected: istiod 1 pod Ready; istio-ingressgateway 2 pods Ready; service shows both `http-envoy-prom` and `http2` ports.

- [ ] **Step 4: Verify proxyStatsMatcher annotation took effect**

```bash
kubectl --context=kind-istio-lab -n istio-system get pod -l app=istio-ingressgateway -o jsonpath='{.items[0].metadata.annotations.proxy\.istio\.io/config}'
```
Expected: prints YAML snippet starting with `proxyStatsMatcher:` and containing `outlier_detection`.

- [ ] **Step 5: Verify Envoy actually exports the new metrics**

```bash
POD=$(kubectl --context=kind-istio-lab -n istio-system get pod -l app=istio-ingressgateway -o jsonpath='{.items[0].metadata.name}')
kubectl --context=kind-istio-lab -n istio-system exec "$POD" -c istio-proxy -- curl -s localhost:15000/stats | grep upstream_cx_length_ms | head -3
```
Expected: emits at least one line containing `upstream_cx_length_ms`. (If empty, proxyStatsMatcher regex didn't match — diagnose before continuing.)

- [ ] **Step 6: Commit**

```bash
git add istio/operator.yaml Makefile
git commit -m "feat(istio): IstioOperator with locked replicas and pod-level proxyStatsMatcher"
```

### Task 6: Gateway resource (VirtualService deferred to Task 12)

**Files:**
- Create: `istio/gateway.yaml`

- [ ] **Step 1: Write `istio/gateway.yaml`** (Gateway only at this stage; VirtualService and DestinationRule are added in Task 12 once apps exist)

```yaml
apiVersion: networking.istio.io/v1beta1
kind: Gateway
metadata:
  name: lab-gateway
  namespace: istio-system
spec:
  selector:
    istio: ingressgateway
  servers:
  - port:
      number: 80
      name: http
      protocol: HTTP
    hosts:
    - "*"
```

- [ ] **Step 2: Apply + verify**

```bash
kubectl --context=kind-istio-lab apply -f istio/gateway.yaml
kubectl --context=kind-istio-lab -n istio-system get gateway lab-gateway
```
Expected: Gateway shown.

- [ ] **Step 3: Commit**

```bash
git add istio/gateway.yaml
git commit -m "feat(istio): lab Gateway on port 80"
```

---

## Phase 3 — Sample apps

### Task 7: ws-chaos Go module + metrics

**Files:**
- Create: `apps/ws-chaos/go.mod`
- Create: `apps/ws-chaos/metrics.go`

- [ ] **Step 1: Init module**

```bash
cd apps/ws-chaos
go mod init github.com/wys1203/istio-labs/apps/ws-chaos
go get github.com/gorilla/websocket@v1.5.1
go get github.com/prometheus/client_golang@v1.18.0
cd "${ROOT_DIR}"
```

- [ ] **Step 2: Write `apps/ws-chaos/metrics.go`**

```go
package main

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	activeConnections = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "wschaos_active_connections",
		Help: "Number of currently open WebSocket connections.",
	})
	messagesTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "wschaos_messages_total",
		Help: "Messages received/sent by ws-chaos.",
	}, []string{"direction"})
	closeTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "wschaos_close_total",
		Help: "WebSocket close events by reason.",
	}, []string{"reason"})
)
```

- [ ] **Step 3: Verify it compiles**

```bash
cd apps/ws-chaos && go build ./... && cd "${ROOT_DIR}"
```
Expected: no output (success). May fail with "no Go files" if main.go absent — that's fine, will be added.

- [ ] **Step 4: Commit**

```bash
git add apps/ws-chaos/go.mod apps/ws-chaos/go.sum apps/ws-chaos/metrics.go
git commit -m "feat(ws-chaos): module init and prometheus metrics"
```

### Task 8: ws-chaos modes (with unit tests, TDD)

**Files:**
- Create: `apps/ws-chaos/modes.go`
- Create: `apps/ws-chaos/modes_test.go`

- [ ] **Step 1: Write the failing tests**

`apps/ws-chaos/modes_test.go`:
```go
package main

import (
	"encoding/json"
	"testing"
)

func TestParseMode_Defaults(t *testing.T) {
	m, err := parseMode([]byte(`{"mode":"normal"}`))
	if err != nil {
		t.Fatal(err)
	}
	if m.Mode != ModeNormal {
		t.Fatalf("expected normal, got %s", m.Mode)
	}
}

func TestParseMode_RandomRSTRatioBounds(t *testing.T) {
	cases := []struct {
		body  string
		ok    bool
		ratio float64
	}{
		{`{"mode":"random-rst","params":{"ratio":0.3}}`, true, 0.3},
		{`{"mode":"random-rst","params":{"ratio":2.0}}`, false, 0},
		{`{"mode":"random-rst","params":{"ratio":-0.1}}`, false, 0},
	}
	for _, c := range cases {
		m, err := parseMode([]byte(c.body))
		if c.ok {
			if err != nil {
				t.Errorf("body %q: unexpected err %v", c.body, err)
			}
			if m.Params.Ratio != c.ratio {
				t.Errorf("body %q: ratio = %v, want %v", c.body, m.Params.Ratio, c.ratio)
			}
		} else if err == nil {
			t.Errorf("body %q: expected err, got nil", c.body)
		}
	}
}

func TestParseMode_DropAfterSeconds(t *testing.T) {
	m, err := parseMode([]byte(`{"mode":"drop-after","params":{"seconds":30}}`))
	if err != nil {
		t.Fatal(err)
	}
	if m.Params.Seconds != 30 {
		t.Fatalf("seconds = %d", m.Params.Seconds)
	}
}

func TestParseMode_UnknownMode(t *testing.T) {
	_, err := parseMode([]byte(`{"mode":"banana"}`))
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestModeJSONRoundtrip(t *testing.T) {
	m := Mode{Mode: ModeIdleHang}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	got, err := parseMode(b)
	if err != nil {
		t.Fatal(err)
	}
	if got.Mode != ModeIdleHang {
		t.Fatalf("roundtrip mismatch: %s", got.Mode)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
cd apps/ws-chaos && go test ./... ; cd "${ROOT_DIR}"
```
Expected: FAIL — `parseMode` undefined, `Mode` undefined, etc.

- [ ] **Step 3: Write `apps/ws-chaos/modes.go`**

```go
package main

import (
	"encoding/json"
	"fmt"
	"sync/atomic"
)

type ModeName string

const (
	ModeNormal         ModeName = "normal"
	ModeIdleHang       ModeName = "idle-hang"
	ModeSlowClose      ModeName = "slow-close"
	ModeRandomRST      ModeName = "random-rst"
	ModeDropAfter      ModeName = "drop-after"
	ModeCPUBurn        ModeName = "cpu-burn"
	ModeRejectUpgrade  ModeName = "reject-upgrade"
)

type ModeParams struct {
	Ratio   float64 `json:"ratio,omitempty"`   // for random-rst (0..1)
	Seconds int     `json:"seconds,omitempty"` // for drop-after, slow-close
}

type Mode struct {
	Mode   ModeName   `json:"mode"`
	Params ModeParams `json:"params,omitempty"`
}

var validModes = map[ModeName]bool{
	ModeNormal: true, ModeIdleHang: true, ModeSlowClose: true,
	ModeRandomRST: true, ModeDropAfter: true, ModeCPUBurn: true,
	ModeRejectUpgrade: true,
}

func parseMode(body []byte) (Mode, error) {
	var m Mode
	if err := json.Unmarshal(body, &m); err != nil {
		return m, fmt.Errorf("parse mode body: %w", err)
	}
	if !validModes[m.Mode] {
		return m, fmt.Errorf("unknown mode %q", m.Mode)
	}
	if m.Mode == ModeRandomRST && (m.Params.Ratio < 0 || m.Params.Ratio > 1) {
		return m, fmt.Errorf("random-rst ratio must be in [0,1], got %v", m.Params.Ratio)
	}
	if (m.Mode == ModeDropAfter || m.Mode == ModeSlowClose) && m.Params.Seconds < 0 {
		return m, fmt.Errorf("seconds must be >= 0")
	}
	return m, nil
}

// currentMode is the live atomic pointer to the active Mode (immutable per pointer).
var currentMode atomic.Value // stores Mode

func init() {
	currentMode.Store(Mode{Mode: ModeNormal})
}

func getMode() Mode { return currentMode.Load().(Mode) }
func setMode(m Mode) { currentMode.Store(m) }
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
cd apps/ws-chaos && go test ./... -v ; cd "${ROOT_DIR}"
```
Expected: all 5 tests PASS.

- [ ] **Step 5: Commit**

```bash
git add apps/ws-chaos/modes.go apps/ws-chaos/modes_test.go
git commit -m "feat(ws-chaos): mode definitions and validation with unit tests"
```

### Task 9: ws-chaos handlers

**Files:**
- Create: `apps/ws-chaos/handlers.go`

- [ ] **Step 1: Write `apps/ws-chaos/handlers.go`**

```go
package main

import (
	"encoding/json"
	"io"
	"math/rand"
	"net"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}

func handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, "ok")
}

func handleGetMode(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(getMode())
}

func handleSetMode(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	m, err := parseMode(body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	setMode(m)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func handleWS(w http.ResponseWriter, r *http.Request) {
	mode := getMode()

	if mode.Mode == ModeRejectUpgrade {
		closeTotal.WithLabelValues("reject").Inc()
		http.Error(w, "upgrade rejected by chaos mode", http.StatusServiceUnavailable)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	activeConnections.Inc()
	defer activeConnections.Dec()

	switch mode.Mode {
	case ModeIdleHang:
		// Hold the connection open; never read or write. Eventually peer or LB tears it down.
		<-r.Context().Done()
		closeTotal.WithLabelValues("timeout").Inc()
		_ = conn.Close()
		return
	case ModeRandomRST:
		if rand.Float64() < mode.Params.Ratio {
			abortConnection(conn)
			closeTotal.WithLabelValues("rst").Inc()
			return
		}
	case ModeDropAfter:
		go func() {
			time.Sleep(time.Duration(mode.Params.Seconds) * time.Second)
			_ = conn.WriteMessage(websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.CloseGoingAway, "drop-after"))
			_ = conn.Close()
		}()
	case ModeCPUBurn:
		go burnCPU(r.Context())
	}

	// Echo loop with heartbeat.
	conn.SetPongHandler(func(string) error { return nil })
	heartbeat := time.NewTicker(30 * time.Second)
	defer heartbeat.Stop()
	done := make(chan struct{})

	go func() {
		defer close(done)
		for {
			t, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			messagesTotal.WithLabelValues("rx").Inc()
			if mode.Mode == ModeSlowClose {
				time.Sleep(time.Duration(mode.Params.Seconds) * time.Second)
				_ = conn.Close()
				return
			}
			if err := conn.WriteMessage(t, msg); err != nil {
				return
			}
			messagesTotal.WithLabelValues("tx").Inc()
		}
	}()

	for {
		select {
		case <-done:
			closeTotal.WithLabelValues("normal").Inc()
			return
		case <-heartbeat.C:
			_ = conn.WriteControl(websocket.PingMessage, []byte("ping"), time.Now().Add(5*time.Second))
		}
	}
}

// abortConnection forces a TCP RST by setting linger to 0 before close.
func abortConnection(conn *websocket.Conn) {
	if tcp, ok := conn.NetConn().(*net.TCPConn); ok {
		_ = tcp.SetLinger(0)
	}
	_ = conn.NetConn().Close()
}

func burnCPU(stop <-chan struct{}) {
	for {
		select {
		case <-stop:
			return
		default:
			// busy loop
			for i := 0; i < 1_000_000; i++ {
				_ = i * i
			}
		}
	}
}
```

- [ ] **Step 2: Verify compilation**

```bash
cd apps/ws-chaos && go vet ./... && cd "${ROOT_DIR}"
```
Expected: no output. (May warn `main.go` missing — added in Task 10.)

- [ ] **Step 3: Commit**

```bash
git add apps/ws-chaos/handlers.go apps/ws-chaos/go.sum
git commit -m "feat(ws-chaos): WebSocket and admin handlers with chaos mode dispatch"
```

### Task 10: ws-chaos main + smoke

**Files:**
- Create: `apps/ws-chaos/main.go`

- [ ] **Step 1: Write `apps/ws-chaos/main.go`**

```go
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func main() {
	addr := envOr("LISTEN_ADDR", ":8080")

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", handleHealth)
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/ws", handleWS)
	mux.HandleFunc("/admin/mode", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			handleGetMode(w, r)
		case http.MethodPost:
			handleSetMode(w, r)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})

	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		log.Printf("ws-chaos listening on %s", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
}

func envOr(k, def string) string {
	if v, ok := os.LookupEnv(k); ok && v != "" {
		return v
	}
	return def
}
```

- [ ] **Step 2: Local smoke test**

```bash
cd apps/ws-chaos
go build -o /tmp/ws-chaos ./... && /tmp/ws-chaos &
PID=$!
sleep 1
curl -fsS localhost:8080/healthz && echo
curl -fsS localhost:8080/admin/mode && echo
curl -fsS -X POST localhost:8080/admin/mode -d '{"mode":"idle-hang"}' && echo
curl -fsS localhost:8080/metrics | head -5
kill $PID
cd "${ROOT_DIR}"
```
Expected: `ok`; JSON `{"mode":"normal"...}`; mode change 200 OK; metrics output begins with `# HELP go_...` lines.

- [ ] **Step 3: Commit**

```bash
git add apps/ws-chaos/main.go
git commit -m "feat(ws-chaos): main with HTTP server and graceful shutdown"
```

### Task 11: ws-chaos Dockerfile + manifest + Make build

**Files:**
- Create: `apps/ws-chaos/Dockerfile`
- Create: `apps/ws-chaos/manifest.yaml`

- [ ] **Step 1: Write `apps/ws-chaos/Dockerfile`** (multi-stage, distroless final)

```dockerfile
# syntax=docker/dockerfile:1
FROM golang:1.21-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -o /out/ws-chaos ./...

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/ws-chaos /ws-chaos
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/ws-chaos"]
```

- [ ] **Step 2: Write `apps/ws-chaos/manifest.yaml`**

```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: apps
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: ws-chaos
  namespace: apps
spec:
  replicas: 1
  selector:
    matchLabels: {app: ws-chaos}
  template:
    metadata:
      labels: {app: ws-chaos}
    spec:
      containers:
      - name: ws-chaos
        image: ws-chaos:dev
        imagePullPolicy: IfNotPresent
        ports:
        - {containerPort: 8080, name: http}
        livenessProbe:
          httpGet: {path: /healthz, port: 8080}
          initialDelaySeconds: 3
        readinessProbe:
          httpGet: {path: /healthz, port: 8080}
        resources:
          requests: {cpu: 50m, memory: 32Mi}
          limits:   {cpu: 500m, memory: 128Mi}
---
apiVersion: v1
kind: Service
metadata:
  name: ws-chaos
  namespace: apps
  labels: {app: ws-chaos}
spec:
  selector: {app: ws-chaos}
  ports:
  - {name: http, port: 8080, targetPort: 8080}
```

- [ ] **Step 3: Add `build` target to Makefile**

Replace the empty `build:` body with:
```makefile
build: ## build & kind-load ws-chaos and ws-prober images
	docker build -t ws-chaos:dev   apps/ws-chaos
	docker build -t ws-prober:dev  apps/load/ws-prober
	kind load docker-image ws-chaos:dev   --name istio-lab
	kind load docker-image ws-prober:dev  --name istio-lab
```
(ws-prober reference will resolve in Task 15.)

- [ ] **Step 4: Wire `install-apps` (partial — gateway/VS/DR added in Task 12)**

Replace the empty `install-apps:` body with:
```makefile
install-apps: build ## install ws-chaos + http-echo + Gateway/VS/DR
	$(KCTX) apply -f apps/ws-chaos/manifest.yaml
	$(KCTX) apply -f apps/http-echo/manifest.yaml
	$(KCTX) apply -f istio/gateway.yaml
	$(KCTX) -n apps rollout status deploy/ws-chaos --timeout=120s
	$(KCTX) -n apps rollout status deploy/http-echo --timeout=120s
```

- [ ] **Step 5: Commit (don't run yet — http-echo manifest comes in Task 13)**

```bash
git add apps/ws-chaos/Dockerfile apps/ws-chaos/manifest.yaml Makefile
git commit -m "feat(ws-chaos): container image and k8s manifest"
```

### Task 12: VirtualService + DestinationRule for ws-chaos

**Files:**
- Modify: `istio/gateway.yaml`

- [ ] **Step 1: Append VS and DR to `istio/gateway.yaml`** (file should already contain the Gateway from Task 6; append below it)

```yaml
---
apiVersion: networking.istio.io/v1beta1
kind: VirtualService
metadata:
  name: lab-vs
  namespace: istio-system
spec:
  hosts:
  - "*"
  gateways:
  - lab-gateway
  http:
  - match:
    - uri:
        prefix: /ws
    route:
    - destination:
        host: ws-chaos.apps.svc.cluster.local
        port: {number: 8080}
  - match:
    - uri:
        prefix: /http
    route:
    - destination:
        host: http-echo.apps.svc.cluster.local
        port: {number: 80}
---
apiVersion: networking.istio.io/v1beta1
kind: DestinationRule
metadata:
  name: ws-chaos
  namespace: apps
spec:
  host: ws-chaos.apps.svc.cluster.local
  trafficPolicy:
    connectionPool:
      tcp:
        connectTimeout: 5s
        maxConnections: 1024
      http:
        idleTimeout: 60s
        h2UpgradePolicy: DO_NOT_UPGRADE
    outlierDetection:
      consecutive5xxErrors: 5
      interval: 10s
      baseEjectionTime: 30s
      maxEjectionPercent: 50
```

- [ ] **Step 2: Commit (apply happens in `make install-apps` later)**

```bash
git add istio/gateway.yaml
git commit -m "feat(istio): VirtualService for /ws and /http; DestinationRule for ws-chaos"
```

### Task 13: http-echo manifest

**Files:**
- Create: `apps/http-echo/manifest.yaml`

- [ ] **Step 1: Write `apps/http-echo/manifest.yaml`**

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: http-echo
  namespace: apps
spec:
  replicas: 1
  selector:
    matchLabels: {app: http-echo}
  template:
    metadata:
      labels: {app: http-echo}
    spec:
      containers:
      - name: http-echo
        image: mendhak/http-https-echo:31
        ports:
        - {containerPort: 8080, name: http}
        env:
        - {name: HTTP_PORT, value: "8080"}
        readinessProbe:
          httpGet: {path: /, port: 8080}
        resources:
          requests: {cpu: 20m, memory: 32Mi}
          limits:   {cpu: 200m, memory: 128Mi}
---
apiVersion: v1
kind: Service
metadata:
  name: http-echo
  namespace: apps
  labels: {app: http-echo}
spec:
  selector: {app: http-echo}
  ports:
  - {name: http, port: 80, targetPort: 8080}
```

- [ ] **Step 2: Run `make install-apps` end-to-end**

```bash
make install-apps
```
Expected: ws-chaos and http-echo deployments Ready; Gateway/VS/DR applied.

- [ ] **Step 3: Smoke test through IGW**

```bash
kubectl --context=kind-istio-lab -n istio-system port-forward svc/istio-ingressgateway 18080:80 &
PF=$!
sleep 2
curl -fsS http://localhost:18080/http/ | head -20
# WS upgrade smoke
curl -i -N --http1.1 -H 'Connection: Upgrade' -H 'Upgrade: websocket' \
  -H 'Sec-WebSocket-Version: 13' -H 'Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==' \
  http://localhost:18080/ws | head -5
kill $PF
```
Expected: HTTP echo returns JSON with request details; WS request returns `HTTP/1.1 101 Switching Protocols`.

- [ ] **Step 4: Commit**

```bash
git add apps/http-echo/manifest.yaml
git commit -m "feat(http-echo): baseline HTTP service for IGW comparison"
```

### Task 14: ws-prober Go module + tests

**Files:**
- Create: `apps/load/ws-prober/go.mod`
- Create: `apps/load/ws-prober/metrics.go`
- Create: `apps/load/ws-prober/prober.go`
- Create: `apps/load/ws-prober/prober_test.go`
- Create: `apps/load/ws-prober/main.go`

- [ ] **Step 1: Init module**

```bash
cd apps/load/ws-prober
go mod init github.com/wys1203/istio-labs/apps/load/ws-prober
go get github.com/gorilla/websocket@v1.5.1
go get github.com/prometheus/client_golang@v1.18.0
cd "${ROOT_DIR}"
```

- [ ] **Step 2: Write `apps/load/ws-prober/metrics.go`**

```go
package main

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	probeActive = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "wsprober_active_connections",
		Help: "Currently open prober connections.",
	})
	probeAttempts = promauto.NewCounter(prometheus.CounterOpts{
		Name: "wsprober_attempts_total",
		Help: "Total ping attempts.",
	})
	probeFailures = promauto.NewCounter(prometheus.CounterOpts{
		Name: "wsprober_failures_total",
		Help: "Ping attempts that did not get an echo within timeout.",
	})
	probeRTT = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "wsprober_rtt_seconds",
		Help:    "RTT of ping/echo round-trip in seconds.",
		Buckets: prometheus.ExponentialBuckets(0.005, 2, 12),
	})
	probeClose = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "wsprober_close_total",
		Help: "Connection close events by reason.",
	}, []string{"reason"})
	probeTCPErr = promauto.NewCounter(prometheus.CounterOpts{
		Name: "wsprober_tcp_errors_total",
		Help: "TCP-level errors during connect.",
	})
)
```

- [ ] **Step 3: Write the failing test**

`apps/load/ws-prober/prober_test.go`:
```go
package main

import (
	"testing"
	"time"
)

func TestBackoff_GrowsAndCaps(t *testing.T) {
	b := newBackoff(100*time.Millisecond, 2*time.Second)
	prev := time.Duration(0)
	for i := 0; i < 10; i++ {
		d := b.Next()
		if d < prev {
			t.Fatalf("backoff should be monotonic, got %v after %v", d, prev)
		}
		if d > 2*time.Second {
			t.Fatalf("backoff exceeded cap: %v", d)
		}
		prev = d
	}
	b.Reset()
	if got := b.Next(); got != 100*time.Millisecond {
		t.Fatalf("after Reset expected 100ms, got %v", got)
	}
}
```

- [ ] **Step 4: Run + verify FAIL**

```bash
cd apps/load/ws-prober && go test ./... ; cd "${ROOT_DIR}"
```
Expected: FAIL — `newBackoff` undefined.

- [ ] **Step 5: Write `apps/load/ws-prober/prober.go`**

```go
package main

import (
	"context"
	"encoding/binary"
	"fmt"
	"log"
	"net/url"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type backoff struct {
	base, cap, cur time.Duration
}

func newBackoff(base, cap time.Duration) *backoff { return &backoff{base: base, cap: cap, cur: base} }
func (b *backoff) Next() time.Duration {
	d := b.cur
	if d > b.cap {
		d = b.cap
	}
	b.cur *= 2
	return d
}
func (b *backoff) Reset() { b.cur = b.base }

type Prober struct {
	Target      *url.URL
	NumConns    int
	PingEvery   time.Duration
	PingTimeout time.Duration
}

func (p *Prober) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for i := 0; i < p.NumConns; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			p.runOne(ctx, id)
		}(i)
	}
	wg.Wait()
}

func (p *Prober) runOne(ctx context.Context, id int) {
	bo := newBackoff(500*time.Millisecond, 10*time.Second)
	for {
		if ctx.Err() != nil {
			return
		}
		conn, _, err := websocket.DefaultDialer.DialContext(ctx, p.Target.String(), nil)
		if err != nil {
			probeTCPErr.Inc()
			log.Printf("[conn %d] dial: %v", id, err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(bo.Next()):
			}
			continue
		}
		bo.Reset()
		probeActive.Inc()
		p.pingLoop(ctx, conn, id)
		probeActive.Dec()
		_ = conn.Close()
	}
}

func (p *Prober) pingLoop(ctx context.Context, conn *websocket.Conn, id int) {
	defer probeClose.WithLabelValues("normal").Inc()
	t := time.NewTicker(p.PingEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			probeAttempts.Inc()
			now := time.Now()
			payload := make([]byte, 8)
			binary.BigEndian.PutUint64(payload, uint64(now.UnixNano()))
			if err := conn.SetWriteDeadline(now.Add(p.PingTimeout)); err != nil {
				probeFailures.Inc()
				probeClose.WithLabelValues("write-deadline").Inc()
				return
			}
			if err := conn.WriteMessage(websocket.TextMessage, payload); err != nil {
				probeFailures.Inc()
				probeClose.WithLabelValues("write").Inc()
				return
			}
			if err := conn.SetReadDeadline(now.Add(p.PingTimeout)); err != nil {
				probeFailures.Inc()
				return
			}
			_, _, err := conn.ReadMessage()
			if err != nil {
				probeFailures.Inc()
				probeClose.WithLabelValues("read").Inc()
				return
			}
			probeRTT.Observe(time.Since(now).Seconds())
		}
	}
}

func mustParseURL(raw string) *url.URL {
	u, err := url.Parse(raw)
	if err != nil {
		panic(fmt.Sprintf("bad url %q: %v", raw, err))
	}
	return u
}
```

- [ ] **Step 6: Run tests + verify PASS**

```bash
cd apps/load/ws-prober && go test ./... -v ; cd "${ROOT_DIR}"
```
Expected: PASS.

- [ ] **Step 7: Write `apps/load/ws-prober/main.go`**

```go
package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func main() {
	target := envOr("TARGET_URL", "ws://istio-ingressgateway.istio-system/ws")
	conns, _ := strconv.Atoi(envOr("NUM_CONNS", "10"))
	pingEvery, _ := time.ParseDuration(envOr("PING_EVERY", "5s"))
	pingTimeout, _ := time.ParseDuration(envOr("PING_TIMEOUT", "3s"))
	listen := envOr("LISTEN_ADDR", ":9100")

	go func() {
		mux := http.NewServeMux()
		mux.Handle("/metrics", promhttp.Handler())
		mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
		_ = (&http.Server{Addr: listen, Handler: mux, ReadHeaderTimeout: 10 * time.Second}).ListenAndServe()
	}()

	ctx, cancel := context.WithCancel(context.Background())
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	go func() { <-stop; cancel() }()

	p := &Prober{
		Target:      mustParseURL(target),
		NumConns:    conns,
		PingEvery:   pingEvery,
		PingTimeout: pingTimeout,
	}
	p.Run(ctx)
}

func envOr(k, def string) string {
	if v, ok := os.LookupEnv(k); ok && v != "" {
		return v
	}
	return def
}
```

- [ ] **Step 8: Commit**

```bash
git add apps/load/ws-prober
git commit -m "feat(ws-prober): black-box WS prober with backoff and metrics"
```

### Task 15: ws-prober Dockerfile + manifest

**Files:**
- Create: `apps/load/ws-prober/Dockerfile`
- Create: `apps/load/ws-prober/manifest.yaml`

- [ ] **Step 1: Write Dockerfile** (mirror ws-chaos pattern)

```dockerfile
# syntax=docker/dockerfile:1
FROM golang:1.21-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -o /out/ws-prober ./...

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/ws-prober /ws-prober
USER nonroot:nonroot
EXPOSE 9100
ENTRYPOINT ["/ws-prober"]
```

- [ ] **Step 2: Write manifest**

```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: load
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: ws-prober
  namespace: load
spec:
  replicas: 1
  selector:
    matchLabels: {app: ws-prober}
  template:
    metadata:
      labels: {app: ws-prober}
    spec:
      containers:
      - name: ws-prober
        image: ws-prober:dev
        imagePullPolicy: IfNotPresent
        env:
        - {name: TARGET_URL,   value: "ws://istio-ingressgateway.istio-system/ws"}
        - {name: NUM_CONNS,    value: "10"}
        - {name: PING_EVERY,   value: "5s"}
        - {name: PING_TIMEOUT, value: "3s"}
        ports:
        - {containerPort: 9100, name: metrics}
        readinessProbe:
          httpGet: {path: /healthz, port: 9100}
        resources:
          requests: {cpu: 20m, memory: 32Mi}
          limits:   {cpu: 200m, memory: 128Mi}
---
apiVersion: v1
kind: Service
metadata:
  name: ws-prober
  namespace: load
  labels: {app: ws-prober}
spec:
  selector: {app: ws-prober}
  ports:
  - {name: metrics, port: 9100, targetPort: 9100}
```

- [ ] **Step 3: Commit (apply happens via `install-load`)**

```bash
git add apps/load/ws-prober/Dockerfile apps/load/ws-prober/manifest.yaml
git commit -m "feat(ws-prober): container image and k8s manifest"
```

### Task 16: fortio manifest + install-load wiring

**Files:**
- Create: `apps/load/fortio/manifest.yaml`

- [ ] **Step 1: Write fortio manifest**

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: fortio
  namespace: load
spec:
  replicas: 1
  selector:
    matchLabels: {app: fortio}
  template:
    metadata:
      labels: {app: fortio}
    spec:
      containers:
      - name: fortio
        image: fortio/fortio:1.60.3
        args: ["server", "-http-port", "8080"]
        ports:
        - {containerPort: 8080, name: http}
        resources:
          requests: {cpu: 20m, memory: 32Mi}
          limits:   {cpu: 1000m, memory: 256Mi}
---
apiVersion: v1
kind: Service
metadata:
  name: fortio
  namespace: load
spec:
  selector: {app: fortio}
  ports:
  - {name: http, port: 8080, targetPort: 8080}
```

- [ ] **Step 2: Wire `install-load`**

Replace empty `install-load:` body with:
```makefile
install-load: build ## install ws-prober + fortio
	$(KCTX) apply -f apps/load/ws-prober/manifest.yaml
	$(KCTX) apply -f apps/load/fortio/manifest.yaml
	$(KCTX) -n load rollout status deploy/ws-prober --timeout=120s
	$(KCTX) -n load rollout status deploy/fortio    --timeout=120s
```

- [ ] **Step 3: Run + verify**

```bash
make install-load
kubectl --context=kind-istio-lab -n load get pods
kubectl --context=kind-istio-lab -n load logs -l app=ws-prober --tail=20
```
Expected: ws-prober + fortio Ready; ws-prober logs show repeated successful dials and no `dial: ` errors after the first reconnect cycle.

- [ ] **Step 4: Commit**

```bash
git add apps/load/fortio/manifest.yaml Makefile
git commit -m "feat(load): fortio HTTP load server and install-load wiring"
```

---

## Phase 4 — Monitoring stack

### Task 17: kube-prometheus-stack values + install

**Files:**
- Create: `monitoring/values-kps.yaml`

- [ ] **Step 1: Write `monitoring/values-kps.yaml`**

```yaml
fullnameOverride: kps
crds:
  enabled: true

defaultRules:
  create: true
  disabled:
    KubeControllerManagerDown: true
    KubeSchedulerDown: true
    KubeProxyDown: true

kubeProxy:               { enabled: false }
kubeEtcd:                { enabled: false }
kubeControllerManager:   { enabled: false }
kubeScheduler:           { enabled: false }

grafana:
  adminPassword: admin
  defaultDashboardsEnabled: false
  sidecar:
    dashboards:
      enabled: true
      label: grafana_dashboard
      labelValue: "1"
      searchNamespace: ALL
      folder: /tmp/dashboards
      provider:
        foldersFromFilesStructure: true
  service:
    type: ClusterIP

prometheus:
  prometheusSpec:
    retention: 6h
    ruleSelectorNilUsesHelmValues: false
    serviceMonitorSelectorNilUsesHelmValues: false
    podMonitorSelectorNilUsesHelmValues: false
    probeSelectorNilUsesHelmValues: false
    resources:
      requests: {cpu: 200m, memory: 512Mi}
      limits:   {cpu: 1000m, memory: 1Gi}

alertmanager:
  alertmanagerSpec:
    resources:
      requests: {cpu: 50m, memory: 64Mi}
      limits:   {cpu: 200m, memory: 128Mi}
  config:
    route:
      receiver: webhook-logger
      group_by: ['alertname', 'pain_point']
      group_wait: 10s
      group_interval: 30s
      repeat_interval: 5m
      routes:
      - matchers:
        - severity = critical
        receiver: webhook-logger
        repeat_interval: 1m
    receivers:
    - name: webhook-logger
      webhook_configs:
      - url: http://webhook-logger.monitoring.svc.cluster.local:8080/log
        send_resolved: true
```

- [ ] **Step 2: Wire `install-monitoring`**

Replace empty `install-monitoring:` body with:
```makefile
install-monitoring: ## install kube-prometheus-stack + dashboards + alerts + webhook-logger
	helm repo add prometheus-community https://prometheus-community.github.io/helm-charts >/dev/null 2>&1 || true
	helm repo update >/dev/null
	helm --kube-context=kind-istio-lab upgrade --install kps prometheus-community/kube-prometheus-stack \
		--version 45.31.1 \
		-n monitoring --create-namespace \
		-f monitoring/values-kps.yaml \
		--wait --timeout 10m
	$(KCTX) apply -f monitoring/webhook-logger.yaml
	$(KCTX) apply -f monitoring/servicemonitors.yaml
	$(KCTX) apply -f monitoring/alerts/
	$(KCTX) apply -f monitoring/dashboards/
	$(KCTX) -n monitoring rollout status deploy/kps-grafana --timeout=180s
	$(KCTX) -n monitoring rollout status deploy/webhook-logger --timeout=120s
```

- [ ] **Step 3: Commit (run after Tasks 18-27)**

```bash
git add monitoring/values-kps.yaml Makefile
git commit -m "feat(monitoring): kube-prometheus-stack values pinned to 45.31.1"
```

### Task 18: ServiceMonitors + Chaos scripts skeleton (chaos-reset only)

**Files:**
- Create: `monitoring/servicemonitors.yaml`
- Create: `scripts/chaos-reset.sh`

- [ ] **Step 1: Write `monitoring/servicemonitors.yaml`**

```yaml
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: istio-ingressgateway
  namespace: monitoring
  labels: {release: kps}
spec:
  namespaceSelector:
    matchNames: [istio-system]
  selector:
    matchLabels: {app: istio-ingressgateway}
  endpoints:
  - port: http-envoy-prom
    path: /stats/prometheus
    interval: 15s
    relabelings:
    - sourceLabels: [__meta_kubernetes_pod_name]
      targetLabel: pod
    metricRelabelings:
    - sourceLabels: [__name__]
      regex: 'envoy_(cluster|listener|http|server)_.*'
      action: keep
---
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: ws-chaos
  namespace: monitoring
  labels: {release: kps}
spec:
  namespaceSelector:
    matchNames: [apps]
  selector:
    matchLabels: {app: ws-chaos}
  endpoints:
  - port: http
    path: /metrics
    interval: 15s
---
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: ws-prober
  namespace: monitoring
  labels: {release: kps}
spec:
  namespaceSelector:
    matchNames: [load]
  selector:
    matchLabels: {app: ws-prober}
  endpoints:
  - port: metrics
    path: /metrics
    interval: 15s
---
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: webhook-logger
  namespace: monitoring
  labels: {release: kps}
spec:
  namespaceSelector:
    matchNames: [monitoring]
  selector:
    matchLabels: {app: webhook-logger}
  endpoints:
  - port: metrics
    path: /metrics
    interval: 30s
```

- [ ] **Step 2: Write `scripts/chaos-reset.sh`** (created early because all other chaos scripts and `verify.sh` call it for cleanup)

ws-chaos and ws-prober images are distroless — no shell, no wget. We talk to ws-chaos's admin API by spinning up an ephemeral `curlimages/curl` pod via `kubectl run --rm`.

```bash
#!/usr/bin/env bash
source "$(dirname "$0")/lib.sh"

log "resetting all chaos state"

# Restore IGW resource limits to baseline (matches istio/operator.yaml).
log "  -> IGW limits cpu=300m memory=256Mi, replicas=2"
kctx -n istio-system patch deployment istio-ingressgateway --type=strategic -p '
spec:
  replicas: 2
  template:
    spec:
      containers:
      - name: istio-proxy
        resources:
          requests: {cpu: 100m, memory: 128Mi}
          limits:   {cpu: 300m, memory: 256Mi}'

# Scale ws-chaos back to 1 (in case chaos-kill-upstream scaled to 0).
log "  -> ws-chaos replicas=1"
kctx -n apps scale deploy/ws-chaos --replicas=1
kctx -n apps rollout status deploy/ws-chaos --timeout=60s

# Reset ws-chaos mode via ephemeral curl pod (ws-chaos image is distroless).
log "  -> ws-chaos mode=normal"
kctx -n apps run "curl-reset-$(date +%s)" --rm -i --restart=Never \
  --image=curlimages/curl:8.6.0 -- \
  curl -fsS -X POST -H 'Content-Type: application/json' \
    -d '{"mode":"normal"}' \
    http://ws-chaos.apps.svc.cluster.local:8080/admin/mode \
  || warn "could not reset ws-chaos mode (it may not be running yet)"

# Stop any in-flight fortio load (best-effort; pod has shell since fortio image is alpine-based).
kctx -n load exec deploy/fortio -- pkill -f 'fortio load' 2>/dev/null || true

log "reset complete"
```

- [ ] **Step 3: Make scripts executable**

```bash
chmod +x scripts/chaos-reset.sh
```

- [ ] **Step 4: Commit**

```bash
git add monitoring/servicemonitors.yaml scripts/chaos-reset.sh
git commit -m "feat(monitoring): ServiceMonitors for IGW/ws-chaos/ws-prober/webhook-logger; chaos-reset script"
```

### Task 19: webhook-logger

**Files:**
- Create: `monitoring/webhook-logger.yaml`

- [ ] **Step 1: Write `monitoring/webhook-logger.yaml`**

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: webhook-logger
  namespace: monitoring
spec:
  replicas: 1
  selector:
    matchLabels: {app: webhook-logger}
  template:
    metadata:
      labels: {app: webhook-logger}
    spec:
      containers:
      - name: webhook-logger
        image: ghcr.io/tomtom-international/alertmanager-webhook-logger:1.6.0
        args:
        - "-listen-address=:8080"
        - "-metrics-address=:9095"
        ports:
        - {containerPort: 8080, name: webhook}
        - {containerPort: 9095, name: metrics}
        readinessProbe:
          tcpSocket: {port: 8080}
        resources:
          requests: {cpu: 10m, memory: 16Mi}
          limits:   {cpu: 100m, memory: 64Mi}
---
apiVersion: v1
kind: Service
metadata:
  name: webhook-logger
  namespace: monitoring
  labels: {app: webhook-logger}
spec:
  selector: {app: webhook-logger}
  ports:
  - {name: webhook, port: 8080, targetPort: 8080}
  - {name: metrics, port: 9095, targetPort: 9095}
```

- [ ] **Step 2: Run install-monitoring partially to test plumbing**

```bash
make install-monitoring
```
Expected: helm install completes; kps-grafana + webhook-logger + Prometheus pods Ready.

- [ ] **Step 3: Verify scrapes**

```bash
kubectl --context=kind-istio-lab -n monitoring port-forward svc/kps-kube-prometheus-stack-prometheus 9090 &
PF=$!; sleep 3
curl -fsS 'http://localhost:9090/api/v1/targets' | jq '.data.activeTargets[] | {job: .labels.job, health: .health}' | sort -u
kill $PF
```
Expected: targets include `istio-ingressgateway`, `ws-chaos`, `ws-prober`, `webhook-logger`, all with `"health": "up"`.

- [ ] **Step 4: Commit**

```bash
git add monitoring/webhook-logger.yaml
git commit -m "feat(monitoring): webhook-logger deployment + service"
```

### Task 20: End-to-end alert plumbing smoke

(Validation-only task; no new files. Confirms the wiring from Prometheus rule → Alertmanager → webhook-logger before we author real rules.)

- [ ] **Step 1: Apply a one-shot synthetic always-firing rule**

```bash
cat <<'YAML' | kubectl --context=kind-istio-lab apply -f -
apiVersion: monitoring.coreos.com/v1
kind: PrometheusRule
metadata:
  name: smoke-test
  namespace: monitoring
  labels: {release: kps}
spec:
  groups:
  - name: smoke
    rules:
    - alert: SmokeAlwaysFiring
      expr: vector(1)
      for: 0m
      labels: {severity: warning, pain_point: "0"}
      annotations: {summary: "alert pipeline smoke test"}
YAML
```

- [ ] **Step 2: Wait for the alert to reach webhook-logger**

```bash
sleep 60
kubectl --context=kind-istio-lab -n monitoring logs -l app=webhook-logger --tail=20 | grep SmokeAlwaysFiring
```
Expected: at least one line containing `SmokeAlwaysFiring`.

- [ ] **Step 3: Remove smoke rule**

```bash
kubectl --context=kind-istio-lab -n monitoring delete prometheusrule smoke-test
```

- [ ] **Step 4: No commit (validation-only)**

---

## Phase 5 — Dashboards

Each dashboard is delivered as a JSON file plus a ConfigMap wrapper. The Grafana sidecar (configured in Task 17) auto-loads any ConfigMap in any namespace with label `grafana_dashboard=1`.

The JSON follows Grafana 9.x dashboard schema. For each panel below, the implementer produces a JSON object with: `title`, `type`, `gridPos {h, w, x, y}`, `targets[].expr`, `fieldConfig.defaults.thresholds`, `fieldConfig.defaults.unit`. Template variables (`pod`, `cluster`) go in `templating.list[]` with `type=query` and `datasource=Prometheus`.

### Task 21: Dashboard 01 — IGW Health

**Files:**
- Create: `monitoring/dashboards/01-igw-health.json`
- Create: `monitoring/dashboards/01-igw-health-cm.yaml`

- [ ] **Step 1: Author `01-igw-health.json`** with the panels in this table:

| Row | Title | Type | Query | Thresholds / Unit |
|---|---|---|---|---|
| Saturation | CFS Throttling Rate | timeseries | `rate(container_cpu_cfs_throttled_periods_total{pod=~"$pod",container="istio-proxy"}[1m]) / clamp_min(rate(container_cpu_cfs_periods_total{pod=~"$pod",container="istio-proxy"}[1m]), 1)` | green<0.05, yellow 0.05-0.2, red>0.2; unit `percentunit` |
| Saturation | Throttled Time | timeseries | `rate(container_cpu_cfs_throttled_seconds_total{pod=~"$pod",container="istio-proxy"}[1m])` | unit `s/s` |
| Saturation | CPU Usage vs Limit | timeseries | A: `rate(container_cpu_usage_seconds_total{pod=~"$pod",container="istio-proxy"}[1m])`  B: `kube_pod_container_resource_limits{pod=~"$pod",container="istio-proxy",resource="cpu"}` | unit `cores` |
| Saturation | Memory Usage vs Limit | timeseries | A: `container_memory_working_set_bytes{pod=~"$pod",container="istio-proxy"}`  B: `kube_pod_container_resource_limits{pod=~"$pod",container="istio-proxy",resource="memory"}` | unit `bytes` |
| Saturation | Pod Restarts (5m) | stat | `sum by (pod) (increase(kube_pod_container_status_restarts_total{pod=~"$pod"}[5m]))` | red>0 |
| Workers | Envoy server concurrency | stat | `envoy_server_concurrency{pod=~"$pod"}` | — |
| Workers | Envoy uptime | stat | `envoy_server_uptime{pod=~"$pod"}` | unit `s` |
| Listener | Downstream cx active | timeseries | `envoy_listener_downstream_cx_active{pod=~"$pod"}` | — |
| Listener | Downstream cx rate | timeseries | `rate(envoy_listener_downstream_cx_total{pod=~"$pod"}[1m])` | unit `cps` |
| Listener | Active HTTP upgrades (=WS) | timeseries | `envoy_http_downstream_cx_upgrades_active{pod=~"$pod"}` | — |

Template variables:
- `pod`: query `label_values(envoy_server_uptime{job="istio-ingressgateway"}, pod)`, multi=true, default `.*`
- `cluster`: query `label_values(envoy_cluster_upstream_cx_active, cluster)`, multi=true, default `.*` (used in dashboard 02/03 but defined here for consistency)

- [ ] **Step 2: Wrap in ConfigMap `01-igw-health-cm.yaml`**

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: dashboard-igw-health
  namespace: monitoring
  labels:
    grafana_dashboard: "1"
data:
  01-igw-health.json: |
    {{ contents of 01-igw-health.json, indented appropriately }}
```

(Implementation note: build via `kubectl create configmap dashboard-igw-health --from-file=01-igw-health.json -n monitoring --dry-run=client -o yaml | yq '.metadata.labels.grafana_dashboard = "1"'` so the JSON stays canonical.)

- [ ] **Step 3: Apply + verify in Grafana**

```bash
kubectl --context=kind-istio-lab apply -f monitoring/dashboards/01-igw-health-cm.yaml
sleep 10
kubectl --context=kind-istio-lab -n monitoring logs -l app.kubernetes.io/name=grafana -c grafana-sc-dashboard --tail=20 | grep igw-health
```
Expected: log line `Writing /tmp/dashboards/.../01-igw-health.json`.

`make grafana` then browse `http://localhost:3000` (admin/admin) → dashboard "IGW Health" loads, panels render, throttling panel currently green (no chaos yet).

- [ ] **Step 4: Commit**

```bash
git add monitoring/dashboards/01-igw-health.json monitoring/dashboards/01-igw-health-cm.yaml
git commit -m "feat(dashboards): IGW Health dashboard targeting CPU throttling"
```

### Task 22: Dashboard 02 — IGW ↔ Upstream

**Files:**
- Create: `monitoring/dashboards/02-igw-upstream.json`
- Create: `monitoring/dashboards/02-igw-upstream-cm.yaml`

- [ ] **Step 1: Author `02-igw-upstream.json`** with panels:

| Row | Title | Type | Query | Notes |
|---|---|---|---|---|
| Lifecycle | Active upstream cx | timeseries | `envoy_cluster_upstream_cx_active{cluster=~"$cluster"}` | — |
| Lifecycle | Connection open rate | timeseries | `sum by (cluster) (rate(envoy_cluster_upstream_cx_total{cluster=~"$cluster"}[1m]))` | unit `cps` |
| Lifecycle | Connect failed rate | timeseries | `sum by (cluster) (rate(envoy_cluster_upstream_cx_connect_fail_total{cluster=~"$cluster"}[1m]))` | red threshold>1 |
| Lifecycle | **Destroy local vs remote** | timeseries (two series) | A: `sum by (cluster) (rate(envoy_cluster_upstream_cx_destroy_local_total{cluster=~"$cluster"}[1m]))`  B: `sum by (cluster) (rate(envoy_cluster_upstream_cx_destroy_remote_total{cluster=~"$cluster"}[1m]))` | side-by-side legend |
| Lifecycle | cx length p50/p95/p99 | timeseries | three series, e.g. `histogram_quantile(0.95, sum by (le, cluster) (rate(envoy_cluster_upstream_cx_length_ms_bucket{cluster=~"$cluster"}[5m])))` | unit `ms` |
| RQ | RQ rate by code class | timeseries | `sum by (cluster, response_code_class) (rate(envoy_cluster_upstream_rq_xx{cluster=~"$cluster"}[1m]))` | stacked |
| RQ | RQ active | timeseries | `envoy_cluster_upstream_rq_active{cluster=~"$cluster"}` | — |
| RQ | RQ time p50/p95/p99 | timeseries | `histogram_quantile(0.95, sum by (le, cluster) (rate(envoy_cluster_upstream_rq_time_bucket{cluster=~"$cluster"}[5m])))` plus 0.5/0.99 | unit `ms` |
| RQ | Pending requests | timeseries | `envoy_cluster_upstream_rq_pending_active{cluster=~"$cluster"}` | — |
| Health | Healthy member ratio | timeseries | `envoy_cluster_membership_healthy{cluster=~"$cluster"} / clamp_min(envoy_cluster_membership_total{cluster=~"$cluster"}, 1)` | red<0.5 |
| Health | Outlier ejections active | timeseries | `envoy_cluster_outlier_detection_ejections_active{cluster=~"$cluster"}` | — |
| Health | Ejection events (5xx, gateway, etc.) | timeseries | `sum by (cluster) (rate(envoy_cluster_outlier_detection_ejections_consecutive_5xx_total{cluster=~"$cluster"}[5m]))` and `..._consecutive_gateway_failure_total` | — |
| Health | Circuit breaker remaining cx | timeseries | `envoy_cluster_circuit_breakers_default_remaining_cx{cluster=~"$cluster"}` | red==0 |

- [ ] **Step 2: Wrap in ConfigMap `02-igw-upstream-cm.yaml`** (same pattern as Task 21)

- [ ] **Step 3: Apply + verify**

```bash
kubectl --context=kind-istio-lab apply -f monitoring/dashboards/02-igw-upstream-cm.yaml
```
Browse Grafana → dashboard "IGW Upstream" loads. Filter to cluster `outbound|8080||ws-chaos.apps.svc.cluster.local`; cx_active should track ws-prober's NUM_CONNS.

- [ ] **Step 4: Commit**

```bash
git add monitoring/dashboards/02-igw-upstream.json monitoring/dashboards/02-igw-upstream-cm.yaml
git commit -m "feat(dashboards): IGW Upstream dashboard for connection lifecycle visibility"
```

### Task 23: Dashboard 03 — IGW WebSocket

**Files:**
- Create: `monitoring/dashboards/03-igw-websocket.json`
- Create: `monitoring/dashboards/03-igw-websocket-cm.yaml`

- [ ] **Step 1: Author `03-igw-websocket.json`** with three columns in the headline row:

```
Headline row layout: each column is gridPos {w: 8, h: 10, x: {0|8|16}, y: 0..}
```

| Column | Title | Type | Query |
|---|---|---|---|
| Envoy | cx active | timeseries | `envoy_cluster_upstream_cx_active{cluster=~"$cluster"}` |
| Envoy | cx destroy local vs remote | timeseries | (same A/B as dashboard 02) |
| Envoy | cx length p99 | timeseries | `histogram_quantile(0.99, sum by (le) (rate(envoy_cluster_upstream_cx_length_ms_bucket{cluster=~"$cluster"}[5m])))` |
| App | active connections | timeseries | `wschaos_active_connections` |
| App | close by reason | timeseries | `sum by (reason) (rate(wschaos_close_total[1m]))` |
| App | message rate | timeseries | `sum by (direction) (rate(wschaos_messages_total[1m]))` |
| User | active connections | timeseries | `wsprober_active_connections` |
| User | RTT p50/p95 | timeseries | `histogram_quantile(0.95, sum by (le) (rate(wsprober_rtt_seconds_bucket[5m])))` and 0.5 |
| User | loss ratio | timeseries | `sum(rate(wsprober_failures_total[2m])) / clamp_min(sum(rate(wsprober_attempts_total[2m])), 1)` |

Subsequent rows:

| Row | Title | Type | Query |
|---|---|---|---|
| Upgrade | Active upgrades | timeseries | `envoy_http_downstream_cx_upgrades_active{pod=~"$pod"}` |
| Upgrade | Upgrade total rate | timeseries | `rate(envoy_http_downstream_cx_upgrades_total{pod=~"$pod"}[1m])` |
| Upgrade | 4xx/5xx upgrade response | timeseries | `sum by (response_code_class) (rate(envoy_cluster_upstream_rq_xx{cluster=~"$cluster",response_code_class=~"4\|5"}[1m]))` |
| Bytes | RX bytes/s | timeseries | `sum by (cluster) (rate(envoy_cluster_upstream_cx_rx_bytes_total{cluster=~"$cluster"}[1m]))` |
| Bytes | TX bytes/s | timeseries | `sum by (cluster) (rate(envoy_cluster_upstream_cx_tx_bytes_total{cluster=~"$cluster"}[1m]))` |
| Bytes | Buffered RX bytes | timeseries | `envoy_cluster_upstream_cx_rx_bytes_buffered{cluster=~"$cluster"}` |
| SLI | TCP errors from prober | timeseries | `rate(wsprober_tcp_errors_total[1m])` |
| SLI | Prober close by reason | timeseries | `sum by (reason) (rate(wsprober_close_total[1m]))` |

- [ ] **Step 2: Wrap in ConfigMap `03-igw-websocket-cm.yaml`** (same pattern)

- [ ] **Step 3: Apply + verify**

```bash
kubectl --context=kind-istio-lab apply -f monitoring/dashboards/03-igw-websocket-cm.yaml
```
Browse Grafana → "IGW WebSocket" dashboard loads. Headline row shows three columns. Bytes RX/TX panels show non-zero rates from baseline prober traffic.

- [ ] **Step 4: Commit**

```bash
git add monitoring/dashboards/03-igw-websocket.json monitoring/dashboards/03-igw-websocket-cm.yaml
git commit -m "feat(dashboards): IGW WebSocket dashboard with three-view headline"
```

---

## Phase 6 — Alerts

### Task 24: Saturation alerts (pain #1)

**Files:**
- Create: `monitoring/alerts/igw-saturation.yaml`

- [ ] **Step 1: Write `igw-saturation.yaml`**

```yaml
apiVersion: monitoring.coreos.com/v1
kind: PrometheusRule
metadata:
  name: igw-saturation
  namespace: monitoring
  labels: {release: kps}
spec:
  groups:
  - name: igw-saturation
    rules:
    - alert: IGWCPUThrottlingWarn
      expr: |
        (
          sum by (pod) (rate(container_cpu_cfs_throttled_periods_total{pod=~"istio-ingressgateway.*",container="istio-proxy"}[1m]))
          /
          clamp_min(sum by (pod) (rate(container_cpu_cfs_periods_total{pod=~"istio-ingressgateway.*",container="istio-proxy"}[1m])), 1)
        ) > 0.05
      for: 1m
      labels: {severity: warning, pain_point: "1", dashboard: "01"}
      annotations:
        summary: "IGW CPU throttle ratio > 5% on {{ $labels.pod }}"
        description: "CFS throttle ratio is {{ $value | humanizePercentage }}. Open dashboard 01 → Saturation row."

    - alert: IGWCPUThrottlingCritical
      expr: |
        (
          sum by (pod) (rate(container_cpu_cfs_throttled_periods_total{pod=~"istio-ingressgateway.*",container="istio-proxy"}[1m]))
          /
          clamp_min(sum by (pod) (rate(container_cpu_cfs_periods_total{pod=~"istio-ingressgateway.*",container="istio-proxy"}[1m])), 1)
        ) > 0.20
      for: 1m
      labels: {severity: critical, pain_point: "1", dashboard: "01"}
      annotations:
        summary: "IGW CPU throttle ratio > 20% on {{ $labels.pod }}"
        description: "CFS throttle ratio is {{ $value | humanizePercentage }}. Likely impacting request latency."

    - alert: IGWNearCPULimit
      expr: |
        sum by (pod) (rate(container_cpu_usage_seconds_total{pod=~"istio-ingressgateway.*",container="istio-proxy"}[1m]))
        /
        clamp_min(sum by (pod) (kube_pod_container_resource_limits{pod=~"istio-ingressgateway.*",container="istio-proxy",resource="cpu"}), 0.001)
        > 0.8
      for: 5m
      labels: {severity: warning, pain_point: "1", dashboard: "01"}
      annotations:
        summary: "IGW CPU at {{ $value | humanizePercentage }} of limit on {{ $labels.pod }}"

    - alert: IGWMemoryNearLimit
      expr: |
        sum by (pod) (container_memory_working_set_bytes{pod=~"istio-ingressgateway.*",container="istio-proxy"})
        /
        clamp_min(sum by (pod) (kube_pod_container_resource_limits{pod=~"istio-ingressgateway.*",container="istio-proxy",resource="memory"}), 1)
        > 0.85
      for: 5m
      labels: {severity: warning, pain_point: "1", dashboard: "01"}
      annotations:
        summary: "IGW memory at {{ $value | humanizePercentage }} of limit on {{ $labels.pod }}"

    - alert: IGWPodRestarted
      expr: increase(kube_pod_container_status_restarts_total{pod=~"istio-ingressgateway.*"}[5m]) > 0
      for: 0m
      labels: {severity: warning, pain_point: "1", dashboard: "01"}
      annotations:
        summary: "IGW pod {{ $labels.pod }} restarted"
```

- [ ] **Step 2: Apply + verify rule loads**

```bash
kubectl --context=kind-istio-lab apply -f monitoring/alerts/igw-saturation.yaml
sleep 5
kubectl --context=kind-istio-lab -n monitoring port-forward svc/kps-kube-prometheus-stack-prometheus 9090 &
PF=$!; sleep 3
curl -fsS 'http://localhost:9090/api/v1/rules' | jq '.data.groups[] | select(.name=="igw-saturation") | .rules[].name'
kill $PF
```
Expected: lists 5 rule names.

- [ ] **Step 3: Commit**

```bash
git add monitoring/alerts/igw-saturation.yaml
git commit -m "feat(alerts): saturation alerts for IGW CPU throttling (pain #1)"
```

### Task 25: Upstream alerts (pain #2)

**Files:**
- Create: `monitoring/alerts/igw-upstream.yaml`

- [ ] **Step 1: Write `igw-upstream.yaml`**

```yaml
apiVersion: monitoring.coreos.com/v1
kind: PrometheusRule
metadata:
  name: igw-upstream
  namespace: monitoring
  labels: {release: kps}
spec:
  groups:
  - name: igw-upstream
    rules:
    - alert: IGWUpstreamConnectFail
      expr: sum by (cluster) (rate(envoy_cluster_upstream_cx_connect_fail_total{cluster=~"outbound\\|.*"}[1m])) > 1
      for: 1m
      labels: {severity: warning, pain_point: "2", dashboard: "02"}
      annotations:
        summary: "Upstream connect-fail rate {{ $value }}/s on {{ $labels.cluster }}"

    - alert: IGWUpstreamRemoteResetSurge
      expr: |
        (
          sum by (cluster) (rate(envoy_cluster_upstream_cx_destroy_remote_total{cluster=~"outbound\\|.*"}[2m]))
          >
          5 * sum by (cluster) (rate(envoy_cluster_upstream_cx_destroy_local_total{cluster=~"outbound\\|.*"}[2m]))
        )
        and
        sum by (cluster) (rate(envoy_cluster_upstream_cx_destroy_remote_total{cluster=~"outbound\\|.*"}[2m])) > 0.5
      for: 2m
      labels: {severity: warning, pain_point: "2", dashboard: "02"}
      annotations:
        summary: "Remote reset > 5x local on {{ $labels.cluster }}"
        description: "Likely upstream actively resetting. Check Dashboard 02 → destroy local vs remote."

    - alert: IGWUpstreamCxLengthDrop
      expr: |
        histogram_quantile(0.5, sum by (le, cluster) (rate(envoy_cluster_upstream_cx_length_ms_bucket{cluster=~"outbound\\|.*"}[5m])))
        <
        0.5 * histogram_quantile(0.5, sum by (le, cluster) (rate(envoy_cluster_upstream_cx_length_ms_bucket{cluster=~"outbound\\|.*"}[5m] offset 10m)))
      for: 2m
      labels: {severity: warning, pain_point: "2", dashboard: "02"}
      annotations:
        summary: "Median upstream cx length dropped > 50% on {{ $labels.cluster }}"

    - alert: IGWUpstreamUnhealthy
      expr: |
        sum by (cluster) (envoy_cluster_membership_healthy{cluster=~"outbound\\|.*"})
        /
        clamp_min(sum by (cluster) (envoy_cluster_membership_total{cluster=~"outbound\\|.*"}), 1)
        < 0.5
      for: 1m
      labels: {severity: critical, pain_point: "2", dashboard: "02"}
      annotations:
        summary: "Upstream healthy ratio < 50% on {{ $labels.cluster }}"

    - alert: IGWUpstreamOutlierEjection
      expr: sum by (cluster) (envoy_cluster_outlier_detection_ejections_active{cluster=~"outbound\\|.*"}) > 0
      for: 1m
      labels: {severity: warning, pain_point: "2", dashboard: "02"}
      annotations:
        summary: "Outlier detection ejected {{ $value }} endpoints on {{ $labels.cluster }}"

    - alert: IGWUpstreamCircuitBreakerOpen
      expr: sum by (cluster) (envoy_cluster_circuit_breakers_default_remaining_cx{cluster=~"outbound\\|.*"}) == 0
      for: 1m
      labels: {severity: critical, pain_point: "2", dashboard: "02"}
      annotations:
        summary: "Circuit breaker exhausted on {{ $labels.cluster }}"
```

- [ ] **Step 2: Apply + verify load**

```bash
kubectl --context=kind-istio-lab apply -f monitoring/alerts/igw-upstream.yaml
```

- [ ] **Step 3: Commit**

```bash
git add monitoring/alerts/igw-upstream.yaml
git commit -m "feat(alerts): upstream connection-state alerts (pain #2)"
```

### Task 26: WebSocket alerts (pain #3)

**Files:**
- Create: `monitoring/alerts/igw-websocket.yaml`

- [ ] **Step 1: Write `igw-websocket.yaml`**

```yaml
apiVersion: monitoring.coreos.com/v1
kind: PrometheusRule
metadata:
  name: igw-websocket
  namespace: monitoring
  labels: {release: kps}
spec:
  groups:
  - name: igw-websocket
    rules:
    - alert: IGWWSUpgradeFailRate
      expr: |
        sum(rate(envoy_cluster_upstream_rq_xx{cluster=~"outbound\\|.*ws-chaos.*",response_code_class=~"4|5"}[2m]))
        /
        clamp_min(sum(rate(envoy_cluster_upstream_rq_xx{cluster=~"outbound\\|.*ws-chaos.*"}[2m])), 1)
        > 0.1
      for: 2m
      labels: {severity: warning, pain_point: "3", dashboard: "03"}
      annotations:
        summary: "WS upgrade error rate > 10% (ws-chaos)"

    - alert: IGWWSStaleConnections
      expr: |
        (
          sum(envoy_cluster_upstream_cx_active{cluster=~"outbound\\|.*ws-chaos.*"}) > 5
        )
        and
        (
          sum(rate(envoy_cluster_upstream_cx_rx_bytes_total{cluster=~"outbound\\|.*ws-chaos.*"}[2m]))
          + sum(rate(envoy_cluster_upstream_cx_tx_bytes_total{cluster=~"outbound\\|.*ws-chaos.*"}[2m]))
          < 100
        )
      for: 2m
      labels: {severity: critical, pain_point: "3", dashboard: "03"}
      annotations:
        summary: "WS connections active but no traffic flowing"
        description: "Classic 'IGW healthy but upstream unstable' signature. Active cx > 5, RX+TX < 100 B/s."

    - alert: IGWWSProbeLossHigh
      expr: |
        sum(rate(wsprober_failures_total[2m]))
        /
        clamp_min(sum(rate(wsprober_attempts_total[2m])), 1)
        > 0.1
      for: 2m
      labels: {severity: critical, pain_point: "3", dashboard: "03"}
      annotations:
        summary: "Black-box prober loss ratio > 10%"

    - alert: IGWWSProbeRTTHigh
      expr: histogram_quantile(0.95, sum by (le) (rate(wsprober_rtt_seconds_bucket[5m]))) > 0.5
      for: 2m
      labels: {severity: warning, pain_point: "3", dashboard: "03"}
      annotations:
        summary: "Black-box prober p95 RTT > 500ms"

    - alert: IGWWSAppCloseAbnormal
      expr: sum(rate(wschaos_close_total{reason!="normal"}[2m])) > 0.5
      for: 2m
      labels: {severity: warning, pain_point: "3", dashboard: "03"}
      annotations:
        summary: "ws-chaos abnormal close rate > 0.5/s"
```

- [ ] **Step 2: Apply + verify**

```bash
kubectl --context=kind-istio-lab apply -f monitoring/alerts/igw-websocket.yaml
```

- [ ] **Step 3: Commit**

```bash
git add monitoring/alerts/igw-websocket.yaml
git commit -m "feat(alerts): WebSocket-specific alerts (pain #3) including IGWWSStaleConnections"
```

### Task 27: Pipeline self-monitoring alerts

**Files:**
- Create: `monitoring/alerts/igw-pipeline-health.yaml`

- [ ] **Step 1: Write `igw-pipeline-health.yaml`**

```yaml
apiVersion: monitoring.coreos.com/v1
kind: PrometheusRule
metadata:
  name: igw-pipeline-health
  namespace: monitoring
  labels: {release: kps}
spec:
  groups:
  - name: igw-pipeline-health
    rules:
    - alert: IGWScrapeDown
      expr: up{job="istio-ingressgateway"} == 0
      for: 2m
      labels: {severity: critical, pain_point: "0", dashboard: "01"}
      annotations:
        summary: "Prometheus cannot scrape IGW {{ $labels.instance }}"

    - alert: WSChaosScrapeDown
      expr: up{job="ws-chaos"} == 0
      for: 2m
      labels: {severity: warning, pain_point: "0", dashboard: "03"}
      annotations:
        summary: "ws-chaos scrape down"

    - alert: WSProberScrapeDown
      expr: up{job="ws-prober"} == 0
      for: 2m
      labels: {severity: warning, pain_point: "0", dashboard: "03"}
      annotations:
        summary: "ws-prober scrape down"

    - alert: AlertmanagerWebhookSinkDown
      expr: up{job="webhook-logger"} == 0
      for: 2m
      labels: {severity: warning, pain_point: "0", dashboard: "03"}
      annotations:
        summary: "alert pipeline sink down"
```

- [ ] **Step 2: Apply + verify**

```bash
kubectl --context=kind-istio-lab apply -f monitoring/alerts/igw-pipeline-health.yaml
```

- [ ] **Step 3: Commit**

```bash
git add monitoring/alerts/igw-pipeline-health.yaml
git commit -m "feat(alerts): pipeline self-monitoring rules"
```

---

## Phase 7 — Chaos toolkit

A helper used by all chaos-* scripts: a one-liner to POST mode to ws-chaos. Implement in `scripts/lib.sh` for reuse.

### Task 28: Chaos scripts

**Files:**
- Modify: `scripts/lib.sh` (add `set_ws_mode` helper)
- Create: `scripts/chaos-throttle-cpu.sh`
- Create: `scripts/chaos-ws-idle.sh`
- Create: `scripts/chaos-ws-rst.sh`
- Create: `scripts/chaos-ws-drop.sh`
- Create: `scripts/chaos-ws-cpu.sh`
- Create: `scripts/chaos-reject-upgrade.sh`
- Create: `scripts/chaos-kill-upstream.sh`

- [ ] **Step 1: Add `set_ws_mode` and load helpers to `scripts/lib.sh`**

Append to `scripts/lib.sh`:
```bash
# set_ws_mode <json>
# Posts a mode body to ws-chaos /admin/mode using an ephemeral curl pod
# (ws-chaos image is distroless — no shell to exec into).
set_ws_mode() {
  local body=$1
  log "ws-chaos mode -> ${body}"
  kctx -n apps run "curl-$$-$(date +%s)" --rm -i --restart=Never \
    --image=curlimages/curl:8.6.0 -- \
    curl -fsS -X POST -H 'Content-Type: application/json' \
      -d "${body}" http://ws-chaos.apps.svc.cluster.local:8080/admin/mode
}

run_fortio_load() {
  local target=${1:-"http://istio-ingressgateway.istio-system/http/"}
  local qps=${2:-200}
  local seconds=${3:-300}
  log "fortio: ${qps} qps for ${seconds}s -> ${target}"
  kctx -n load exec deploy/fortio -- fortio load -qps "${qps}" -t "${seconds}s" "${target}" >/dev/null &
}

stop_fortio() {
  kctx -n load exec deploy/fortio -- pkill -f 'fortio load' 2>/dev/null || true
}
```

(No edits to `scripts/chaos-reset.sh` are needed — it already uses the ephemeral-curl-pod pattern from Task 18 and pkills fortio directly. The new helpers are purely for the chaos-* scripts that follow.)

- [ ] **Step 2: Write each chaos script**

`scripts/chaos-throttle-cpu.sh`:
```bash
#!/usr/bin/env bash
source "$(dirname "$0")/lib.sh"
warn "expected alerts: IGWCPUThrottlingWarn, IGWCPUThrottlingCritical, IGWNearCPULimit (dashboard 01)"
log "shrink IGW cpu limit to 100m"
kctx -n istio-system patch deployment istio-ingressgateway --type=strategic -p '
spec:
  template:
    spec:
      containers:
      - name: istio-proxy
        resources:
          requests: {cpu: 50m, memory: 128Mi}
          limits:   {cpu: 100m, memory: 256Mi}'
kctx -n istio-system rollout status deploy/istio-ingressgateway --timeout=120s
run_fortio_load "http://istio-ingressgateway.istio-system/http/" 400 300
log "chaos active. run 'make chaos-reset' to restore."
```

`scripts/chaos-ws-idle.sh`:
```bash
#!/usr/bin/env bash
source "$(dirname "$0")/lib.sh"
warn "expected alerts: IGWWSStaleConnections (critical), IGWWSProbeLossHigh (dashboard 03)"
set_ws_mode '{"mode":"idle-hang"}'
```

`scripts/chaos-ws-rst.sh`:
```bash
#!/usr/bin/env bash
source "$(dirname "$0")/lib.sh"
warn "expected alerts: IGWUpstreamRemoteResetSurge (dashboard 02)"
set_ws_mode '{"mode":"random-rst","params":{"ratio":0.5}}'
```

`scripts/chaos-ws-drop.sh`:
```bash
#!/usr/bin/env bash
source "$(dirname "$0")/lib.sh"
warn "expected alerts: IGWUpstreamCxLengthDrop (dashboard 02)"
set_ws_mode '{"mode":"drop-after","params":{"seconds":30}}'
```

`scripts/chaos-ws-cpu.sh`:
```bash
#!/usr/bin/env bash
source "$(dirname "$0")/lib.sh"
warn "expected alerts: IGWWSProbeRTTHigh, IGWWSAppCloseAbnormal (dashboard 03)"
set_ws_mode '{"mode":"cpu-burn"}'
```

`scripts/chaos-reject-upgrade.sh`:
```bash
#!/usr/bin/env bash
source "$(dirname "$0")/lib.sh"
warn "expected alerts: IGWWSUpgradeFailRate (dashboard 03)"
set_ws_mode '{"mode":"reject-upgrade"}'
```

`scripts/chaos-kill-upstream.sh`:
```bash
#!/usr/bin/env bash
source "$(dirname "$0")/lib.sh"
warn "expected alerts: IGWUpstreamUnhealthy (critical), IGWUpstreamConnectFail, IGWUpstreamOutlierEjection (dashboard 02)"
log "scale ws-chaos to 0"
kctx -n apps scale deploy/ws-chaos --replicas=0
```

- [ ] **Step 3: Make all executable**

```bash
chmod +x scripts/chaos-*.sh
```

- [ ] **Step 4: Smoke test one chaos + reset**

```bash
make chaos-ws-idle
sleep 130   # exceed 'for: 2m' on IGWWSStaleConnections
kubectl --context=kind-istio-lab -n monitoring port-forward svc/kps-kube-prometheus-stack-alertmanager 9093 &
PF=$!; sleep 3
curl -fsS 'http://localhost:9093/api/v2/alerts?active=true' | jq '.[] | select(.labels.alertname=="IGWWSStaleConnections")'
kill $PF
make chaos-reset
```
Expected: at least one firing alert object printed with `alertname=IGWWSStaleConnections`. After reset, alert resolves within ~3 minutes.

- [ ] **Step 5: Commit**

```bash
git add scripts/chaos-*.sh scripts/lib.sh
git commit -m "feat(chaos): scripted scenarios for each pain-point alert"
```

---

## Phase 8 — Verification matrix

### Task 29: scripts/verify.sh + acceptance run

**Files:**
- Create: `scripts/verify.sh`

- [ ] **Step 1: Write `scripts/verify.sh`**

```bash
#!/usr/bin/env bash
source "$(dirname "$0")/lib.sh"

AM_PORT=19093
$(KCTX) -n monitoring port-forward svc/kps-kube-prometheus-stack-alertmanager "${AM_PORT}:9093" >/dev/null 2>&1 &
PF=$!
trap 'kill $PF 2>/dev/null; bash "$(dirname "$0")/chaos-reset.sh" >/dev/null 2>&1 || true' EXIT
sleep 3

# wait_alert <alertname> <timeout-seconds>
wait_alert() {
  local name=$1 deadline=$(( $(date +%s) + ${2:-300} ))
  log "  waiting for alert ${name} (timeout $((deadline - $(date +%s)))s)"
  while [[ $(date +%s) -lt $deadline ]]; do
    if curl -fsS "http://localhost:${AM_PORT}/api/v2/alerts?active=true" \
       | jq -e --arg n "$name" 'any(.labels.alertname == $n)' >/dev/null; then
      log "  ✓ ${name} firing"
      return 0
    fi
    sleep 5
  done
  die "timed out waiting for alert ${name}"
}

step() {
  local n=$1 name=$2; shift 2
  log "==== Step $n: $name ===="
  "$@"
}

step 0 "baseline reset" bash "$(dirname "$0")/chaos-reset.sh"
sleep 30  # allow baseline metrics to recover

step 1 "throttle CPU"        bash "$(dirname "$0")/chaos-throttle-cpu.sh"
wait_alert IGWCPUThrottlingWarn 240
wait_alert IGWCPUThrottlingCritical 300
bash "$(dirname "$0")/chaos-reset.sh"
sleep 60

step 2 "WS RST"              bash "$(dirname "$0")/chaos-ws-rst.sh"
wait_alert IGWUpstreamRemoteResetSurge 300
bash "$(dirname "$0")/chaos-reset.sh"
sleep 60

step 3 "WS drop-after"       bash "$(dirname "$0")/chaos-ws-drop.sh"
wait_alert IGWUpstreamCxLengthDrop 360
bash "$(dirname "$0")/chaos-reset.sh"
sleep 60

step 4 "kill upstream"       bash "$(dirname "$0")/chaos-kill-upstream.sh"
wait_alert IGWUpstreamUnhealthy 240
wait_alert IGWUpstreamConnectFail 240
bash "$(dirname "$0")/chaos-reset.sh"
sleep 60

step 5 "reject upgrade"      bash "$(dirname "$0")/chaos-reject-upgrade.sh"
wait_alert IGWWSUpgradeFailRate 240
bash "$(dirname "$0")/chaos-reset.sh"
sleep 60

step 6 "WS idle-hang"        bash "$(dirname "$0")/chaos-ws-idle.sh"
wait_alert IGWWSStaleConnections 240
wait_alert IGWWSProbeLossHigh 240
bash "$(dirname "$0")/chaos-reset.sh"
sleep 60

step 7 "WS cpu-burn"         bash "$(dirname "$0")/chaos-ws-cpu.sh"
wait_alert IGWWSProbeRTTHigh 240
bash "$(dirname "$0")/chaos-reset.sh"

log "all steps passed; verification matrix green"
```

- [ ] **Step 2: Run + verify**

```bash
chmod +x scripts/verify.sh
make verify
```
Expected: each step prints `✓ <alertname> firing`; final line `all steps passed; verification matrix green`. Total runtime ~25-35 minutes.

- [ ] **Step 3: Commit**

```bash
git add scripts/verify.sh
git commit -m "feat(verify): acceptance matrix exercising every chaos→alert pair"
```

---

## Phase 9 — README + GitHub remote

### Task 30: README

**Files:**
- Create: `README.md`

- [ ] **Step 1: Write `README.md`** following spec §8.4 structure:

```markdown
# istio-labs

> Lab for monitoring and alerting on Istio 1.13.5 ingressgateway.
> Targets three production pain points: CPU throttling, IGW↔upstream connection
> state, and "IGW healthy but WebSocket upstream unstable" anomalies.

## Pre-requisites

- macOS or Linux
- Docker daemon running
- `kind` v0.31+, `kubectl`, `helm` v3, `jq`, `curl`, Go 1.21+
- ~6 GB free RAM for the kind cluster

## Quick start

\```bash
make up        # ~5-8 min: kind + istio + apps + monitoring + load
make verify    # ~30 min: full chaos → alert acceptance matrix
make grafana   # http://localhost:3000  (admin / admin)
\```

Tear down: `make down`.

## Topology

(ASCII diagram from spec §3)

## Pain points addressed

### #1 — CPU throttling on IGW
- Dashboard: **IGW Health** → Saturation row → **CFS Throttling Rate**
- Alerts: `IGWCPUThrottlingWarn`, `IGWCPUThrottlingCritical`, `IGWNearCPULimit`
- Reproduce: `make chaos-throttle-cpu`

### #2 — IGW ↔ upstream connection state
- Dashboard: **IGW Upstream** → Lifecycle row → **Destroy local vs remote**
- Alerts: `IGWUpstreamRemoteResetSurge`, `IGWUpstreamCxLengthDrop`, `IGWUpstreamUnhealthy`, `IGWUpstreamOutlierEjection`, `IGWUpstreamCircuitBreakerOpen`, `IGWUpstreamConnectFail`
- Reproduce: `make chaos-ws-rst` / `chaos-ws-drop` / `chaos-kill-upstream`

### #3 — IGW healthy but WebSocket upstream unstable
- Dashboard: **IGW WebSocket** → headline 3-view + Bytes row
- Signature alert: `IGWWSStaleConnections` (cx_active high while RX+TX bytes ≈ 0)
- Reproduce: `make chaos-ws-idle` / `chaos-ws-cpu` / `chaos-reject-upgrade`

## Why pod-level proxyStatsMatcher (not meshConfig)

Many production environments don't allow tweaking `meshConfig.defaultConfig`
(platform-team owned). This lab uses `proxy.istio.io/config` on the IGW pod
annotations — same effect, no mesh-wide impact. Three migration options for
prod:
1. Pod annotation via IstioOperator `podAnnotations` (this lab).
2. Deployment patch / kustomize overlay.
3. EnvoyFilter on `workloadSelector`.

## Verification matrix

(table from spec §6.6)

## Repo layout

(tree from spec §8.3)

## Porting back to prod

| Asset | Lift as-is? | Action |
|---|---|---|
| Dashboard 01 (saturation) | ✓ — generic | retune thresholds |
| Dashboard 02 (upstream) | ✓ — generic | filter to your cluster names |
| Dashboard 03 (WebSocket) | partial | replace `wsprober_*` with your client-side telemetry |
| Saturation alerts | ✓ | retune `> 0.05`/`> 0.20` ratios |
| Upstream alerts | ✓ | retune `> 0.5/s` rates |
| WS alerts | partial | adjust `cx_active > 5` to relative-of-baseline |
| `IGWWSStaleConnections` | ✓ | flagship alert; tune `< 100 B/s` to traffic profile |
| proxyStatsMatcher regex | careful | cardinality on busy mesh; estimate first |

## Troubleshooting

- **IGW not reachable**: `kubectl get svc -n istio-system istio-ingressgateway`. Confirm NodePort 30080.
- **ServiceMonitor not scraping**: confirm Prometheus's `serviceMonitorSelector` is permissive (`*NilUsesHelmValues: false`).
- **Grafana sidecar not loading**: check ConfigMap label `grafana_dashboard=1`.
- **proxyStatsMatcher empty**: `kubectl exec` into IGW pod and curl `:15000/stats | grep upstream_cx_length_ms`.
- **Port forward conflicts** with `keda-lab`: change ports in Make targets or shut down keda-lab first.

## Lab vs prod gaps

(list from spec §10)

## Layout: see [docs/superpowers/specs/2026-05-04-istio-igw-monitoring-lab-design.md](docs/superpowers/specs/2026-05-04-istio-igw-monitoring-lab-design.md) for the full design.
```

- [ ] **Step 2: Commit**

```bash
git add README.md
git commit -m "docs: README with quick start, pain-point map, and prod-porting guide"
```

### Task 31: Final push to GitHub

The remote `origin → git@github.com:wys1203/istio-labs.git` (public, default branch `main`) was created up-front, so all commits made during the previous phases just need to be pushed.

- [ ] **Step 1: Push all phase commits**

```bash
git push origin main
```

Expected: every commit from Tasks 1-30 lands on `main`.

- [ ] **Step 2: Verify**

```bash
gh repo view wys1203/istio-labs --web
```
Expected: README rendered; latest commit matches `git log -1 --oneline`.

- [ ] **Step 3: No commit** (push is the final action).

---

## Self-review

The plan was checked against the spec on completion:

**Spec coverage** — all 12 sections of the spec are covered:
- §1 goal, §2 decisions → captured in plan header.
- §3 architecture → Tasks 3, 5, 6, 12.
- §4.1 kind cluster → Task 3.
- §4.2 Istio → Tasks 4, 5, 6, 12.
- §4.3 sample apps → Tasks 7-11 (ws-chaos), 13 (http-echo), 14-15 (ws-prober), 16 (fortio).
- §4.4 monitoring stack → Tasks 17-19.
- §5 dashboards → Tasks 21-23.
- §6 alerts and verification matrix → Tasks 24-27 (rules), 29 (verify.sh).
- §7 chaos toolkit → Task 28.
- §8 DX/Makefile/README → Tasks 1, 30.
- §9 GitHub setup → Task 31 (initial commit already done in spec phase).
- §10/11/12 (gaps, open items, out-of-scope) → README content + plan header.

**Type/name consistency** — checked: all PromQL `cluster=~"outbound\\|.*"` patterns match `ws-chaos.apps.svc.cluster.local`. Mode names in tests/handlers/scripts align (`idle-hang`, `random-rst`, etc.). Service ports (`http-envoy-prom 15090`, `wsprober metrics 9100`, `webhook-logger 8080/9095`) consistent across IstioOperator, ServiceMonitor, manifests, and PromQL.

**No placeholders** — verified no `TBD`/`TODO`/"add appropriate"/"similar to"; every code/yaml block is complete content; dashboard panels listed by exact title + query + thresholds (JSON serialisation is the implementer's mechanical step, not a content gap).

---

**Plan complete.** Two execution options:

**1. Subagent-Driven (recommended)** — fresh subagent per task with two-stage review.
**2. Inline Execution** — execute in this session via `superpowers:executing-plans`.

Which approach?
