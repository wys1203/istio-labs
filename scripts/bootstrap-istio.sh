#!/usr/bin/env bash
source "$(dirname "$0")/lib.sh"

ISTIO_VERSION=1.16.7
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
