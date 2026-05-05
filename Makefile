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
