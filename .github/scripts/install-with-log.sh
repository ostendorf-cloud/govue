#!/usr/bin/env bash
# Run a command and tee its combined output to a log file. The exit code is the
# command's own, never tee's, so a failed install stays a failed job.
#
# Usage: install-with-log.sh LOGFILE COMMAND [ARGS...]
set -o pipefail

log=${1:?usage: install-with-log.sh LOGFILE COMMAND [ARGS...]}
shift
"$@" 2>&1 | tee "$log"
