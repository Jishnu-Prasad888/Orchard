#!/bin/sh
# Rebuild the Cairn Docker image from the current source tree and (re)run it,
# replacing any previously running dev container. Run this after every change
# you want to see live:
#
#   ./scripts/docker-dev.sh
#
# Every run starts from a clean slate: ./cairn-data (DB, sessions, sign-in) is
# wiped before the container starts, so you always land on first-run setup.
set -eu

cd "$(dirname "$0")/.."

IMAGE="cairn:dev"
CONTAINER="cairn-dev"
PORT="${CAIRN_PORT:-8715}"
DATA_DIR="$(pwd)/cairn-data"

rm -rf "$DATA_DIR"
mkdir -p "$DATA_DIR"

echo "==> Building $IMAGE"
docker build -t "$IMAGE" .

if docker ps -a --format '{{.Names}}' | grep -qx "$CONTAINER"; then
  echo "==> Removing previous $CONTAINER container"
  docker rm -f "$CONTAINER" >/dev/null
fi

echo "==> Starting $CONTAINER on port $PORT"
docker run -d \
  --name "$CONTAINER" \
  -p "$PORT:8715" \
  -v "$DATA_DIR:/data" \
  "$IMAGE" >/dev/null

echo "==> Cairn running at http://localhost:$PORT"
echo "==> Logs: docker logs -f $CONTAINER"
