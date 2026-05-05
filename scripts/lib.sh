#!/usr/bin/env bash
# Shared helpers for scripts in this lab.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ISTIOCTL="${ROOT_DIR}/bin/istio-1.16.7/bin/istioctl"
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
