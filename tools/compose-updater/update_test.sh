#!/bin/sh
# Contract test for tools/compose-updater/update.sh: run the real script with a
# fake docker/git on PATH and check the files it exchanges with the gateway.
# Not part of the Go suite (it needs a POSIX shell and a real filesystem loop);
# run it with: sh tools/compose-updater/update_test.sh
set -u

HERE=$(cd "$(dirname "$0")" && pwd)
WORK=$(mktemp -d)
PROJ="$WORK/project"
STATE="$WORK/state"
BIN="$WORK/bin"
mkdir -p "$PROJ/.git" "$STATE" "$BIN"
CALLS="$WORK/calls.log"
: >"$CALLS"

fail() {
  echo "FAIL: $1" >&2
  exit 1
}

# Fake docker: records its arguments and honours the exit code in the flag file.
# The flag is a file, not an environment variable: the updater is started once and
# its environment is fixed, so a later change to the fake's behaviour has to come
# through the filesystem.
echo 0 >"$WORK/up_exit"
cat >"$BIN/docker" <<EOF
#!/bin/sh
echo "docker \$*" >>"$CALLS"
case "\$*" in
  *"up -d"*) echo "recreating meta-gateway"; exit \$(cat "$WORK/up_exit") ;;
  *"pull"*) echo "pulled image"; exit 0 ;;
esac
exit 0
EOF
chmod +x "$BIN/docker"
PATH="$BIN:$PATH"
export PATH

PROJECT_DIR="$PROJ" STATE_DIR="$STATE" SERVICE="meta-gateway" POLL_SECONDS=1 HEARTBEAT_SECONDS=1 \
  sh "$HERE/update.sh" >"$WORK/stdout.log" 2>&1 &
UPDATER=$!
trap 'kill $UPDATER 2>/dev/null' EXIT

wait_for() { # wait_for <path> <seconds>
  _i=0
  while [ ! -f "$1" ]; do
    _i=$((_i + 1))
    [ "$_i" -gt "$2" ] && return 1
    sleep 1
  done
  return 0
}

wait_for "$STATE/.ready" 10 || fail "the sidecar never wrote a heartbeat"
grep -q '"project_dir":"'"$PROJ"'"' "$STATE/.ready" || fail "heartbeat does not name the project dir: $(cat "$STATE/.ready")"

# --- success path -----------------------------------------------------------
cat >"$STATE/request.json.tmp" <<EOF
{"target":"v4.0.0","from":"v4.0.0-beta.8","requested_at":$(date +%s)}
EOF
mv "$STATE/request.json.tmp" "$STATE/request.json"
wait_for "$STATE/result.json" 30 || fail "no result.json after a request"
grep -q '"exit_code":0' "$STATE/result.json" || fail "success path reported a failure: $(cat "$STATE/result.json")"
grep -q '"up":"ok"' "$STATE/result.json" || fail "up step not marked ok"
grep -q '"target":"v4.0.0"' "$STATE/result.json" || fail "result lost the target"
[ -f "$STATE/request.json" ] && fail "the request was not claimed"
# The project name must be pinned with -p: without it compose derives the project
# from the directory this sidecar mounts the deployment at (/work), which is a
# PARALLEL stack with its own empty data volume. The sidecar falls back to the
# directory name when its container carries no compose label — which is this test's
# situation, so the fallback name is what the calls must carry.
grep -q 'compose -p project pull meta-gateway' "$CALLS" || fail "compose pull was not run with the project pinned: $(cat "$CALLS")"
grep -q 'compose -p project up -d --no-build --no-deps meta-gateway' "$CALLS" || fail "compose up was not run with the service scope: $(cat "$CALLS")"

# --- failure path -----------------------------------------------------------
rm -f "$STATE/result.json"
echo 1 >"$WORK/up_exit"
cat >"$STATE/request.json.tmp" <<EOF
{"target":"v4.0.1","requested_at":$(date +%s)}
EOF
mv "$STATE/request.json.tmp" "$STATE/request.json"
wait_for "$STATE/result.json" 30 || fail "no result.json after the failing request"
grep -q '"exit_code":1' "$STATE/result.json" || fail "failure path reported success: $(cat "$STATE/result.json")"
grep -q '"up":"failed"' "$STATE/result.json" || fail "the failing step is not named"
grep -q 'recreating meta-gateway' "$STATE/result.json" || fail "the log tail was not carried into the result"
echo 0 >"$WORK/up_exit"

echo "PASS: compose-updater contract (heartbeat, request/result, failure reporting)"
