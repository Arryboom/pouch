#!/usr/bin/env bash

set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")"
source utils.sh

cd ../../
readonly REPO_BASE="$(pwd -P)"

# keep the first one only
GOPATH="${GOPATH%%:*}"

# add bin folder into PATH so that pouch-integration is available.
export PATH="${REPO_BASE}/bin:${PATH}"

# add bin folder into PATH.
export PATH="${GOPATH}/bin:${PATH}"

# CRI_SKIP skips the test to skip.
DEFAULT_CRI_SKIP="should error on create with wrong options"
DEFAULT_CRI_SKIP+="|seccomp"
DEFAULT_CRI_SKIP+="|image"
# TODO: support failed test
DEFAULT_CRI_SKIP+="|runtime should support Privileged is false"
DEFAULT_CRI_SKIP+="|runtime should support reopening container log"
CRI_SKIP="${CRI_SKIP:-"${DEFAULT_CRI_SKIP}"}"

# CRI_FOCUS focuses the test to run.
# With the CRI manager completes its function, we may need to expand this field.
CRI_FOCUS=${CRI_FOCUS:-}

POUCH_SOCK="/var/run/pouchcri.sock"

# tmplog_dir stores the background job log data
tmplog_dir="$(mktemp -d /tmp/integration-daemon-cri-testing-XXXXX)"
local_persist_log="${tmplog_dir}/local_persist.log"
trap 'rm -rf /tmp/integration-daemon-cri-testing-*' EXIT

# integration::install_critest installs test case.
integration::install_critest() {
  hack/install/install_critest.sh
}

# integration::install_cni installs cni plugins.
integration::install_cni() {
   hack/install/install_cni.sh
}

# integration::install_local_persist installs local persist plugin.
integration::install_local_persist() {
   hack/install/install_local_persist.sh
}

# integration::run_daemon_cri_test_cases runs CRI test cases.
integration::run_daemon_cri_test_cases() {
  local cri_runtime code
  cri_runtime=$1
  echo "start pouch daemon cri-${cri_runtime} integration test..."

  set +e
  critest --runtime-endpoint=${POUCH_SOCK} \
    --ginkgo.focus="${CRI_FOCUS}" --ginkgo.skip="${CRI_SKIP}" --parallel=8
  code=$?

  integration::stop_local_persist
  integration::stop_pouchd
  set -e

  if [[ "${code}" != "0" ]]; then
    echo "failed to pass integration cases!"
    echo "there is daemon logs..."
    cat /var/log/pouch
    exit ${code}
  fi

  # sleep for pouchd stop and got the coverage
  sleep 5
}

integration::run_cri_test(){
  local cri_runtime
  cri_runtime=$1

  integration::install_critest

  integration::stop_local_persist
  integration::run_local_persist_background "${local_persist_log}"

  set +e; integration::ping_pouchd; code=$?; set -e
  if [[ "${code}" != "0" ]]; then
    echo "there is daemon logs..."
    cat /var/log/pouch
    exit ${code}
  fi
  integration::run_daemon_cri_test_cases "${cri_runtime}"
}

main() {
  local cri_runtime
  cri_runtime=$1

  integration::install_cni
  integration::install_local_persist
  integration::run_cri_test "${cri_runtime}"
}

main "$@"
