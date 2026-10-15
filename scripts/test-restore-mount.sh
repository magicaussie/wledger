#!/usr/bin/env sh
#
# D1 regression harness: restore into a bind-mounted uploads directory.
#
# Reproduces the original D1 failure — the default restore protocol renames the
# live uploads directory aside, which returns EBUSY when that directory is a
# mount point — and proves the mount-aware restore activates a new uploads
# generation through the mount without renaming, unmounting or replacing it.
#
# The uploads path is provided to the container as a real Docker bind mount, the
# exact layout docker-compose.yaml uses (./uploads:/wledger/app/uploads). The
# test therefore exercises genuine mount detection and a genuine EBUSY rename.
#
# The test runs as root inside the container (so it can attempt the rename whose
# EBUSY it asserts); the trap removes the root-owned host artifacts through a
# throwaway container.
#
# Usage: scripts/test-restore-mount.sh
set -eu

GO_IMAGE="${GO_IMAGE:-golang:1.26.9}"
MODCACHE="${GOMODCACHE:-$HOME/go/pkg/mod}"

REPO_DIR="$(cd "$(dirname "$0")/.." && pwd)"
HOST_ROOT="$(mktemp -d)"
HOST_TMP="$(dirname "$HOST_ROOT")"
mkdir -p "$HOST_ROOT/uploads"

cleanup() {
	rm -rf "$HOST_ROOT" 2>/dev/null || true
	docker run --rm --user 0:0 -v "$HOST_TMP":/hosttmp "$GO_IMAGE" \
		rm -rf "/hosttmp/$(basename "$HOST_ROOT")" >/dev/null 2>&1 || true
}
trap cleanup EXIT INT TERM

echo "repo:     $REPO_DIR"
echo "uploads:  $HOST_ROOT/uploads (bind-mounted to /wledger/app/uploads)"
echo "go image: $GO_IMAGE"

docker run --rm \
	-e CGO_ENABLED=1 \
	-e WLEDGER_TEST_MOUNT_UPLOADS=/wledger/app/uploads \
	-v "$REPO_DIR":/src:ro \
	-v "$HOST_ROOT/uploads":/wledger/app/uploads \
	-v "$MODCACHE":/go/pkg/mod \
	-w /src \
	"$GO_IMAGE" \
	go test -tags fts5 -count=1 -run TestRestore_RealBindMountPoint -v ./internal/backup/
