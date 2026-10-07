#!/bin/sh
# compose-updater — the one-click update executor.
#
# Why this exists instead of watchtower: watchtower recreates a container from
# the OLD container's inspect data, so `environment:` and `.env` changes never
# reach the new container. Its maintainer, asked exactly this: "Watchtower works
# with env vars present in container metadata (like docker inspect
# _containerId_), so it looks like it can't use docker-compose variables"
# (containrrr/watchtower#233), and the follow-up was "outside of the scope of
# watchtower". So an env change meant a hand-typed `docker compose up` — forever.
#
# This sidecar holds the Docker socket and the project directory instead, and
# one click runs the same two commands an operator would type:
#
#   docker compose pull <service>
#   docker compose up -d --no-build --no-deps <service>
#
# Re-reading the deployment file is the point: `.env` values are interpolated
# fresh, so an env change lands with the image change instead of waiting for
# somebody to type the command. Changes to `docker-compose.yml` itself are still
# a deployment action (it is code, not configuration) — pull and recreate once
# when a release adds a service or a new variable.
#
# The gateway itself never sees the socket. The two containers share one small
# volume: the gateway writes request.json, this script answers with result.json
# and keeps .ready fresh — which is how the gateway tells "updater present" from
# "updater removed from the compose file".
#
# Deliberately NOT done here:
#   * no periodic polling — updates happen on an explicit operator action only
#   * no image-tag override — the deployment's own IMAGE_TAG decides, so the
#     channel semantics stay in the deployment file where they are documented
#   * no `git pull` — the deployment file belongs to the operator, and silently
#     rewriting it is not what "update the image" means
#   * no `--deps` — the updater must not recreate itself mid-run when a release
#     changes its own service definition

set -u

PROJECT_DIR="${PROJECT_DIR:-/work}"
SERVICE="${SERVICE:-meta-gateway}"
STATE_DIR="${STATE_DIR:-/update}"
POLL_SECONDS="${POLL_SECONDS:-2}"
HEARTBEAT_SECONDS="${HEARTBEAT_SECONDS:-30}"
LOG_LINES="${LOG_LINES:-60}"
LINE_LIMIT="${LINE_LIMIT:-400}"

LOG="$STATE_DIR/updater.log"

log() {
  printf '%s %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$*" >>"$LOG" 2>/dev/null || true
}

# resolve_project_name — the compose project this sidecar belongs to.
#
# Compose derives the project name from the directory the client runs in, and this
# sidecar mounts the deployment at /work: a plain `docker compose up` from here
# means "project work" — a PARALLEL stack, with its own network, its own freshly
# created EMPTY data volume, and a second gateway container. Production hit
# exactly this on 2026-10-07 and was saved only by the real gateway already holding
# port 4100; with the port free the operator would have been handed a gateway on an
# empty database, which reads as "the update wiped my configuration".
#
# The sidecar container carries the label of the stack that launched it, so ask
# the daemon for that name and pin it with -p. COMPOSE_PROJECT_NAME overrides it;
# a sidecar started by `docker run` has no label and falls back to the directory
# name (the old behaviour, logged).
resolve_project_name() {
  if [ -n "${COMPOSE_PROJECT_NAME:-}" ]; then
    printf '%s' "$COMPOSE_PROJECT_NAME"
    return
  fi
  docker inspect "$(hostname)" \
    --format '{{index .Config.Labels "com.docker.compose.project"}}' 2>/dev/null || true
}

# compose — run the deployment's own compose file under the deployment's own
# project, so `up` recreates the running stack instead of inventing a new one.
compose() {
  docker compose -p "$PROJECT_NAME" "$@"
}

# atomic_write <path> <content> — a reader must never see a half-written file.
atomic_write() {
  _tmp="$1.$$"
  printf '%s' "$2" >"$_tmp" 2>/dev/null && mv -f "$_tmp" "$1" 2>/dev/null
}

# json_string — read stdin, emit a JSON string literal (quotes included).
json_string() {
  awk '
    BEGIN { printf "\"" }
    {
      gsub(/\\/, "\\\\"); gsub(/"/, "\\\""); gsub(/\t/, "\\t"); gsub(/\r/, "")
      if (NR > 1) printf "\\n"
      printf "%s", $0
    }
    END { printf "\"" }
  '
}

# field <file> <key> — pull a flat string field out of the request the gateway
# wrote. The writer is our own code, so a plain sed is enough; a missing field
# yields an empty string, which is a valid request (follow the channel).
field() {
  sed -n "s/.*\"$2\"[[:space:]]*:[[:space:]]*\"\([^\"]*\)\".*/\1/p" "$1" 2>/dev/null | head -n 1
}

# Resolved once, before the loop: the name has to be the deployment's, not the
# directory this container happens to mount it at (see resolve_project_name).
PROJECT_NAME=$(resolve_project_name)
if [ -z "$PROJECT_NAME" ]; then
  PROJECT_NAME=$(basename "$PROJECT_DIR")
  log "no compose project label on this container; assuming project=$PROJECT_NAME from $PROJECT_DIR"
fi

heartbeat() {
  atomic_write "$STATE_DIR/.ready" \
    "{\"pid\":$$,\"service\":\"$SERVICE\",\"project_dir\":\"$PROJECT_DIR\",\"started_at\":$(date +%s)}"
}

run_update() {
  _target="$1"
  _started=$(date +%s)
  log "update requested (target=${_target:-follow-channel})"

  _pull="failed"
  if (cd "$PROJECT_DIR" && compose pull "$SERVICE" >>"$LOG" 2>&1); then
    _pull="ok"
  else
    log "docker compose pull failed"
  fi

  _up="failed"
  if (cd "$PROJECT_DIR" && compose up -d --no-build --no-deps "$SERVICE" >>"$LOG" 2>&1); then
    _up="ok"
  else
    log "docker compose up failed"
  fi

  _exit=1
  if [ "$_pull" = "ok" ] && [ "$_up" = "ok" ]; then
    _exit=0
  fi
  log "update finished (exit=$_exit pull=$_pull up=$_up)"

  _tail=$(tail -n "$LOG_LINES" "$LOG" 2>/dev/null | cut -c1-"$LINE_LIMIT" | json_string)
  atomic_write "$STATE_DIR/result.json" \
    "{\"target\":$(printf '%s' "$_target" | json_string),\"exit_code\":$_exit,\"started_at\":$_started,\"finished_at\":$(date +%s),\"pull\":\"$_pull\",\"up\":\"$_up\",\"log\":$_tail}"
}

mkdir -p "$STATE_DIR" 2>/dev/null || true
if [ ! -d "$STATE_DIR" ]; then
  echo "compose-updater: state directory $STATE_DIR is not mounted" >&2
  exit 1
fi
if [ ! -d "$PROJECT_DIR" ]; then
  echo "compose-updater: project directory $PROJECT_DIR is not mounted" >&2
  exit 1
fi

log "compose-updater started (service=$SERVICE project_dir=$PROJECT_DIR project=$PROJECT_NAME)"
heartbeat
_last_beat=$(date +%s)

while :; do
  _now=$(date +%s)
  if [ $((_now - _last_beat)) -ge "$HEARTBEAT_SECONDS" ]; then
    heartbeat
    _last_beat=$_now
  fi
  if [ -f "$STATE_DIR/request.json" ]; then
    # Claim the request before running: a crash mid-update must not replay it
    # on restart, and a second click during a run lands as a fresh file.
    _target=$(field "$STATE_DIR/request.json" target)
    rm -f "$STATE_DIR/request.json"
    run_update "$_target"
  fi
  sleep "$POLL_SECONDS"
done
