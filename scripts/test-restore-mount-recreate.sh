#!/usr/bin/env sh
#
# D1 regression harness: restore recovery across a real Docker container
# recreation.
#
# The uploads directory and the database are provided to the container as real
# Docker bind mounts — the exact layout docker-compose.yaml uses
# (./uploads:/wledger/app/uploads and ./data:/wledger/data). A first container
# performs a restore that is killed at a failpoint; a second, freshly created
# container over the same persistent volumes runs startup recovery and asserts
# the outcome.
#
# Because the second container has a fresh container filesystem, any recovery
# metadata kept outside the persistent volumes is lost. This is exactly the
# failure the D1 follow-up fixes: the durable restore journal must live inside
# the persistent uploads mount, not on the container filesystem.
#
# Failpoints exercised (each in its own pair of containers):
#   drain        killed after the first drain rename (before the drain marker)
#   drain-marker killed after the durable drain marker (before any fill)
#   fill         killed after the first fill rename
#   commit       killed after the database transaction committed
#
# The first three must roll back to the exact previous generation; the last must
# complete the committed new generation.
#
# The containers run as root (as the shipped image does), so the bind-mounted
# volumes are root-owned and are reset between scenarios through a throwaway
# container.
#
# Usage: scripts/test-restore-mount-recreate.sh
set -eu

GO_IMAGE="${GO_IMAGE:-golang:1.26.9}"
MODCACHE="${GOMODCACHE:-$HOME/go/pkg/mod}"

REPO_DIR="$(cd "$(dirname "$0")/.." && pwd)"
HOST_ROOT="$(mktemp -d)"
HOST_TMP="$(dirname "$HOST_ROOT")"
mkdir -p "$HOST_ROOT/uploads" "$HOST_ROOT/data" "$HOST_ROOT/gocache"

cleanup() {
	rm -rf "$HOST_ROOT" 2>/dev/null || true
	docker run --rm --user 0:0 -v "$HOST_TMP":/hosttmp "$GO_IMAGE" \
		rm -rf "/hosttmp/$(basename "$HOST_ROOT")" >/dev/null 2>&1 || true
}
trap cleanup EXIT INT TERM

# reset_volumes empties and recreates the persistent uploads and data volumes
# through a throwaway container, because the harness containers write them as
# root.
reset_volumes() {
	docker run --rm --user 0:0 -v "$HOST_TMP":/hosttmp "$GO_IMAGE" \
		sh -c "rm -rf /hosttmp/$(basename "$HOST_ROOT")/uploads /hosttmp/$(basename "$HOST_ROOT")/data && mkdir -p /hosttmp/$(basename "$HOST_ROOT")/uploads /hosttmp/$(basename "$HOST_ROOT")/data" >/dev/null
}

run_phase() {
	phase="$1"
	point="$2"
	docker run --rm \
		-e HOME=/tmp \
		-e CGO_ENABLED=1 \
		-e GOCACHE=/gocache \
		-e GOMODCACHE=/go/pkg/mod \
		-e WLEDGER_TEST_MOUNT_RECREATE_PHASE="$phase" \
		-e WLEDGER_TEST_MOUNT_RECREATE_POINT="$point" \
		-e WLEDGER_TEST_RESTORE_DB=/wledger/data/wledger.db \
		-e WLEDGER_TEST_RESTORE_UPLOADS=/wledger/app/uploads \
		-e WLEDGER_TEST_RESTORE_ARCHIVE=/tmp/restore.zip \
		-v "$REPO_DIR":/src:ro \
		-v "$HOST_ROOT/uploads":/wledger/app/uploads \
		-v "$HOST_ROOT/data":/wledger/data \
		-v "$HOST_ROOT/gocache":/gocache \
		-v "$MODCACHE":/go/pkg/mod \
		-w /src \
		"$GO_IMAGE" \
		go test -tags fts5 -count=1 -run '^TestRestoreMountRecreateHelper$' -v ./internal/backup/
}

echo "repo:     $REPO_DIR"
echo "volumes:  $HOST_ROOT/{uploads,data}"
echo "go image: $GO_IMAGE"

for point in drain drain-marker fill commit; do
	echo
	echo "=== container recreation at failpoint: $point ==="
	reset_volumes

	# Container A: perform the restore and get killed at the failpoint.
	crash_output="$(run_phase crash "$point" 2>&1 || true)"
	if [ ! -f "$HOST_ROOT/data/.crash-ready" ]; then
		echo "crash phase did not reach the failpoint:"
		printf '%s\n' "$crash_output"
		exit 1
	fi

	# Container B: a brand-new container over the same persistent volumes.
	set +e
	recover_output="$(run_phase recover "$point" 2>&1)"
	recover_status=$?
	set -e
	if [ "$recover_status" -ne 0 ]; then
		echo "recover phase failed (exit $recover_status):"
		printf '%s\n' "$recover_output"
		exit 1
	fi
done

echo
echo "PASS: mount-aware restore recovery survives container recreation"
