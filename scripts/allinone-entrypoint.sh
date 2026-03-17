#!/bin/sh
set -e

echo "PrimitiveBox All-in-One starting..."
echo "  Workspace: ${PB_WORKSPACE:-/workspace}"
echo "  Data dir:  ${PB_DATA_DIR:-/data}"
echo "  Port:      ${PB_PORT:-8080}"

exec pb server start \
  --host 0.0.0.0 \
  --port "${PB_PORT:-8080}" \
  --workspace "${PB_WORKSPACE:-/workspace}" \
  --ui
