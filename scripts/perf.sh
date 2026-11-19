#!/usr/bin/env bash
# Copyright 2026 The Scanopsis Authors
# SPDX-License-Identifier: Apache-2.0
#
# Measure the performance targets and fail when one is missed:
#   initial JavaScript   < 300 KB gzip        (web build budget)
#   container cold start < 2 s                (docker run until /readyz is 200)
#   idle memory          < 64 MiB             (docker stats, 10 s after ready)
#   portfolio p95        < 150 ms at 500 services, and the read API p95
#   20 MB report ingest  < 3 s                (integration tests assert these)
#
# Needs Docker, Go and Node. Pulling images and building are not timed.
# Usage: bash scripts/perf.sh [image]   (default image: scanopsis:perf, built here)
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root"

image=${1:-scanopsis:perf}
start_limit_ms=2000
memory_limit_mib=64
settle_seconds=10

# One source for the PostgreSQL image: the pinned reference of the e2e stack.
postgres_image=$(awk '/image: postgres/ {print $2; exit}' test/e2e/compose.yaml)
[ -n "$postgres_image" ] || { echo "perf: no PostgreSQL image found in test/e2e/compose.yaml" >&2; exit 1; }

suffix="$$-$(date +%s)"
net="scanopsis-perf-$suffix"
db="scanopsis-perf-db-$suffix"
app="scanopsis-perf-app-$suffix"
log=$(mktemp -d)

cleanup() {
  docker rm -f "$app" "$db" >/dev/null 2>&1 || true
  docker network rm "$net" >/dev/null 2>&1 || true
  rm -rf "$log"
}
trap cleanup EXIT

rows=()
failed=0
# record <name> <value> <limit> <ok|fail>
record() {
  rows+=("$(printf '%-54s %14s %12s  %s' "$1" "$2" "$3" "$4")")
  [ "$4" = ok ] || failed=1
}
# to_ms reads "<number> <unit>" (s, ms or µs) and prints milliseconds.
to_ms() { awk '{ v = ($2 == "s") ? $1 * 1000 : ($2 == "ms") ? $1 : $1 / 1000; printf "%.1f", v }'; }
within() { awk -v v="$1" -v l="$2" 'BEGIN { exit !(v < l) }' && echo ok || echo fail; }

# --- Initial JavaScript -------------------------------------------------------
echo "== web build"
make web-build >"$log/web.txt" 2>&1 || { cat "$log/web.txt" >&2; echo "perf: web build failed (bundle over budget?)" >&2; exit 1; }
js_line=$(node web/scripts/check-size.mjs | grep 'Initial JavaScript')
js_kb=$(echo "$js_line" | awk '{print $3}')
js_budget=$(echo "$js_line" | sed -E 's/.*budget ([0-9.]+) KB.*/\1/')
record "initial JavaScript (gzip)" "${js_kb} KB" "< ${js_budget} KB" "$(within "$js_kb" "$js_budget")"

# --- Container cold start and idle memory ------------------------------------
echo "== image"
if [ $# -eq 0 ]; then make image IMAGE="$image" >"$log/image.txt" 2>&1 || { cat "$log/image.txt" >&2; exit 1; }; fi
docker image inspect "$image" >/dev/null
docker pull -q "$postgres_image" >/dev/null

docker network create "$net" >/dev/null
docker run -d --name "$db" --network "$net" --network-alias db \
  -e POSTGRES_USER=scanopsis -e POSTGRES_PASSWORD=scanopsis -e POSTGRES_DB=scanopsis \
  --tmpfs /var/lib/postgresql "$postgres_image" >/dev/null
for _ in $(seq 1 60); do
  docker exec "$db" pg_isready -q -U scanopsis -d scanopsis && break
  sleep 1
done
docker exec "$db" pg_isready -q -U scanopsis -d scanopsis || { echo "perf: PostgreSQL did not start" >&2; exit 1; }

echo "== cold start"
key=$(head -c 32 /dev/zero | base64) # a fixed all-zero key; this stack is thrown away
t0=$(python3 -c 'import time; print(time.monotonic_ns())')
docker run -d --name "$app" --network "$net" -p 127.0.0.1::8080 \
  -e SCANOPSIS_DATABASE_URL='postgres://scanopsis:scanopsis@db:5432/scanopsis?sslmode=disable' \
  -e SCANOPSIS_PUBLIC_URL=http://localhost:8080 \
  -e SCANOPSIS_SECRET_KEY="$key" \
  "$image" >/dev/null
port=$(docker port "$app" 8080/tcp | head -n1 | sed 's/.*://')
ready=0
for _ in $(seq 1 1500); do # up to 30 s
  if [ "$(curl -s -o /dev/null -w '%{http_code}' --max-time 1 "http://127.0.0.1:${port}/readyz" || true)" = 200 ]; then
    ready=1
    break
  fi
  sleep 0.02
done
t1=$(python3 -c 'import time; print(time.monotonic_ns())')
if [ "$ready" -ne 1 ]; then
  docker logs "$app" >&2 || true
  echo "perf: /readyz never returned 200" >&2
  exit 1
fi
start_ms=$(( (t1 - t0) / 1000000 ))
record "container cold start" "${start_ms} ms" "< ${start_limit_ms} ms" "$(within "$start_ms" "$start_limit_ms")"

echo "== idle memory (after ${settle_seconds} s)"
sleep "$settle_seconds"
mem_raw=$(docker stats --no-stream --format '{{.MemUsage}}' "$app" | awk '{print $1}')
mem_mib=$(echo "$mem_raw" | awk '
  /GiB$/ { sub(/GiB/, ""); printf "%.1f", $1 * 1024; next }
  /MiB$/ { sub(/MiB/, ""); printf "%.1f", $1; next }
  /KiB$/ { sub(/KiB/, ""); printf "%.1f", $1 / 1024; next }
  /B$/   { sub(/B/, "");   printf "%.1f", $1 / 1048576; next }')
record "idle memory" "${mem_mib} MiB" "< ${memory_limit_mib} MiB" "$(within "$mem_mib" "$memory_limit_mib")"

# --- Database-backed performance tests ---------------------------------------
# These assert their own limits (portfolio p95 < 150 ms at 500 services, read
# API p95 < 150 ms, 20 MB ingest < 3 s) and run without the race detector, so
# the numbers are real.
echo "== performance tests"
# The test containers are removed by the testcontainers reaper. CI disables
# it (TESTCONTAINERS_RYUK_DISABLED) because its runners are discarded.
go_status=ok
go test -count=1 -tags integration -v \
  -run 'TestPortfolioPerformance|TestReadPerformance|TestIntegrationIngest20MB' \
  ./internal/catalog ./internal/findings ./internal/ingest >"$log/go.txt" 2>&1 || go_status=fail

p95_line=$(grep -h 'portfolio, 500 services' "$log/go.txt" | head -n1 || true)
p95=$(echo "$p95_line" | sed -nE 's/.*p95 ([0-9.]+)(ms|µs|s).*/\1 \2/p')
if [ -n "$p95" ]; then
  p95_ms=$(echo "$p95" | to_ms)
  record "portfolio p95 (500 services)" "${p95_ms} ms" "< 150 ms" "$(within "$p95_ms" 150)"
else
  record "portfolio p95 (500 services)" "no result" "< 150 ms" fail
fi

while IFS= read -r line; do
  name=$(echo "$line" | sed -E 's/^[[:space:]]*[a-z_.]+:[0-9]+: //; s/: .*//')
  t=$(echo "$line" | sed -nE 's/.* p95 ([0-9.]+)(ms|µs|s) .*/\1 \2/p')
  [ -n "$t" ] || continue
  t_ms=$(echo "$t" | to_ms)
  record "read API p95: $name" "${t_ms} ms" "< 150 ms" "$(within "$t_ms" 150)"
done < <(grep -h '^[[:space:]]*perf_integration_test.go:.*: p50 ' "$log/go.txt" || true)

ingested=$(grep -h 'ingested in' "$log/go.txt" || true)
if [ -n "$ingested" ]; then
  while IFS= read -r line; do
    name=$(echo "$line" | sed -E 's/^[[:space:]]*[a-z_.]+:[0-9]+: //; s/: .*//')
    t=$(echo "$line" | sed -nE 's/.*ingested in ([0-9.]+)(ms|µs|s).*/\1 \2/p')
    t_ms=$(echo "$t" | to_ms)
    record "20 MB ingest: $name" "${t_ms} ms" "< 3000 ms" "$(within "$t_ms" 3000)"
  done <<<"$ingested"
else
  record "20 MB ingest" "no result" "< 3000 ms" fail
fi
record "performance tests (go test)" "$go_status" "pass" "$go_status"
if [ "$go_status" != ok ]; then
  tail -n 40 "$log/go.txt" >&2
fi

# --- Results ------------------------------------------------------------------
echo
printf '%-54s %14s %12s  %s\n' "measure" "result" "limit" "status"
printf '%-54s %14s %12s  %s\n' "------------------------------------------------------" "--------------" "------------" "------"
printf '%s\n' "${rows[@]}"
echo
if [ "$failed" -ne 0 ]; then
  echo "perf: at least one target was missed" >&2
  exit 1
fi
echo "perf: all targets met"
