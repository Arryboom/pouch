#!/usr/bin/env bash

set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")"
source utils.sh

readonly REPO_BASE="$(cd ../../ && pwd -P)"

# add bin folder into PATH so that pouch-integration is available.
export PATH="${REPO_BASE}/bin:${PATH}"

# tmplog_dir stores the background job log data
tmplog_dir="$(mktemp -d /tmp/integration-testing-XXXXX)"
pouchd_log="${tmplog_dir}/pouchd.log"
local_persist_log="${tmplog_dir}/local_persist.log"
trap 'rm -rf /tmp/integration-testing-*' EXIT

# daemon integration coverage profile
coverage_profile="${REPO_BASE}/coverage/integration_daemon_profile.out"
rm -rf "${coverage_profile}"

# integration::run_daemon_test_cases starts cases.
integration::run_daemon_test_cases() {
  echo "start pouch daemon integration test..."
  local code=0
  local concurrent=${concurrent:-1}
  local job_id=$1
  [[ $concurrent == 1 ]] && job_id=0
  local logfile=/tmp/test$$.log
  cp -rf "${REPO_BASE}/test/tls" /tmp/
  set +e
  pushd "${REPO_BASE}/test"
  local testcases total_case_num
  if grep -q "^ID=\"alios\"$" /etc/os-release; then
    if grep "\"default-runtime\".*:.*\"kata-runtime\"" /etc/pouch/config.json;
    then
      testcases=$(cat "${REPO_BASE}/test/testcase.kata")
      # shellcheck disable=SC2002
      total_case_num=$(cat "${REPO_BASE}/test/testcase.kata" | wc -l)
      echo "start to run kata test cases"
    else
      testcases=$(cat "${REPO_BASE}/test/testcase."{common,alios})
      total_case_num=$(cat "${REPO_BASE}/test/testcase."{common,alios} | wc -l)
      echo "start to run common test cases and alios specified cases"
    fi
  else
    testcases=$(cat "${REPO_BASE}/test/testcase.common")
    # shellcheck disable=SC2002
    total_case_num=$(cat "${REPO_BASE}/test/testcase.common" | wc -l)
    echo "start to run common test cases"
  fi
  local rest=$((total_case_num%concurrent))
  local index_start=$(((total_case_num/concurrent)*job_id))
  local index_end=$(((total_case_num/concurrent)*(job_id+1)-1))
  [[ $job_id == $((concurrent-1)) ]] && index_end=$((index_end+rest))
  # shellcheck disable=SC2206
  local test_case_array=( $testcases )
  local index=${index_start}
  for ((;index<=index_end;index++));
  do
    "${REPO_BASE}/bin/pouchd-integration-test" -test.v -check.v -check.f \
	    "${test_case_array[$index]}" 2>&1 | tee -a $logfile
    ret=$?
    if [[ ${ret} -ne 0 ]]; then
      code=${ret}
    fi
  done
  local passed_count=0
  local failed_count=0
  local skipped_count=0
  local missed_count=0
  local expected_failures_count=0
  local panicked_count=0
  local fixture_panicked_count=0
  # shellcheck disable=SC2013
  for i in $(grep -E "^(OOPS:|OK:)\s" ${logfile} | grep -o -e "[0-9]\+ passed" | awk -F ' ' '{print $1}');
  do
    passed_count=$((passed_count + i))
  done
  # shellcheck disable=SC2013
  for i in $(grep -E "^(OOPS:|OK:)\s" ${logfile} | grep -o -e "[0-9]\+ skipped" | awk -F ' ' '{print $1}');
  do
    skipped_count=$((skipped_count + i))
  done
  # shellcheck disable=SC2013
  for i in $(grep -E "^(OOPS:|OK:)\s" ${logfile} | grep -o -e "[0-9]\+ FAILED" | awk -F ' ' '{print $1}');
  do
    failed_count=$((failed_count + i))
  done
  # shellcheck disable=SC2013
  for i in $(grep -E "^(OOPS:|OK:)\s" ${logfile} | grep -o -e "[0-9]\+ MISSED" | awk -F ' ' '{print $1}');
  do
    missed_count=$((missed_count + i))
  done
  # shellcheck disable=SC2013
  for i in $(grep -E "^(OOPS:|OK:)\s" ${logfile} | grep -o -e "[0-9]\+ expected failures" | awk -F ' ' '{print $1}');
  do
    expected_failures_count=$((expected_failures_count + i))
  done
  # shellcheck disable=SC2013
  for i in $(grep -E "^(OOPS:|OK:)\s" ${logfile} | grep -o -e "[0-9]\+ PANICKED" | awk -F ' ' '{print $1}');
  do
    panicked_count=$((panicked_count + i))
  done
  # shellcheck disable=SC2013
  for i in $(grep -E "^(OOPS:|OK:)\s" ${logfile} | grep -o -e "[0-9]\+ FIXTURE-PANICKED" | awk -F ' ' '{print $1}');
  do
    fixture_panicked_count=$((fixture_panicked_count + i))
  done
  echo "---------Test Result---------"
  echo "passed case $passed_count"
  echo "skipped case $skipped_count"
  echo "failed case $failed_count"
  echo "expected failures case $expected_failures_count"
  echo "missed case $missed_count"
  echo "panicked case $panicked_count"
  echo "fixture panicked case $fixture_panicked_count"
  echo "---------Test Result---------"
  integration::stop_local_persist
  integration::stop_pouchd
  set -e

  if [[ "${code}" != "0" ]]; then
    echo "failed to pass integration cases!"
    echo "here is daemon logs...."
    cat "${pouchd_log}"
    exit ${code}
  fi

  # sleep for pouchd stop and got the coverage
  sleep 5
}

main() {
  local cmd flags
  # assign default value for job_id, if no params passed to this script
  # will run all the test cases on this node
  local job_id=${1:-0}
  cmd="pouchd-integration"
  flags=" -test.coverprofile=${coverage_profile} DEVEL"
  flags="${flags} --add-runtime runv=runv --add-runtime kata-runtime=kata-runtime"

  integration::stop_local_persist
  integration::run_local_persist_background "${local_persist_log}"

  integration::stop_mount_lxcfs
  integration::run_mount_lxcfs_background

  integration::stop_pouchd
  integration::run_pouchd_background "${cmd}" "${flags}" "${pouchd_log}"

  # use subshell to ping
  set +e; ( integration::ping_pouchd ); code=$?; set -e
  if [[ "${code}" != "0" ]]; then
    echo "there is daemon logs..."
    cat "${pouchd_log}"
    exit ${code}
  fi
  integration::run_daemon_test_cases "${job_id}"
}

main "$@"
