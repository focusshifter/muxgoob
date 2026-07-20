#!/usr/bin/env bash
# Atomically update muxgoob from the latest GitHub release and request a
# graceful systemd restart. Requires the graceful-restart systemd drop-in.
set -Eeuo pipefail

USER="gooby"
APP_DIR="/home/gooby/app"
SERVICE="muxgoob.service"
REPO="focusshifter/muxgoob"
ARCH="linux_amd64"
VERSION_FILE="$APP_DIR/.muxgoob_version"
LOCK_FILE="$APP_DIR/.muxgoob-update.lock"

exec 9>"$LOCK_FILE"
flock -n 9 || { echo "Another muxgoob update is already running" >&2; exit 1; }

CURRENT_VERSION=""
[[ -f "$VERSION_FILE" ]] && CURRENT_VERSION=$(<"$VERSION_FILE")
LATEST_VERSION=$(curl --fail --silent --show-error --location \
  "https://api.github.com/repos/$REPO/releases/latest" | jq -er '.tag_name')

[[ "$CURRENT_VERSION" == "$LATEST_VERSION" ]] && exit 0

VERSION="${LATEST_VERSION#v}"
ASSET="muxgoob_${VERSION}_${ARCH}.tar.gz"
CHECKSUMS="muxgoob_${VERSION}_checksums.txt"
BASE_URL="https://github.com/$REPO/releases/download/$LATEST_VERSION"
TMP_DIR=$(mktemp -d /tmp/muxgoob-update.XXXXXX)
STAGED_BINARY="$APP_DIR/.muxgoob.new.$$"
STAGED_VERSION="$APP_DIR/.muxgoob_version.new.$$"
BACKUP_BINARY="$APP_DIR/.muxgoob.previous.$$"
BACKUP_VERSION="$APP_DIR/.muxgoob_version.previous.$$"
cleanup() {
  rm -rf "$TMP_DIR" "$STAGED_BINARY" "$STAGED_VERSION" "$BACKUP_BINARY" "$BACKUP_VERSION"
}
trap cleanup EXIT

printf 'Updating %s -> %s\n' "${CURRENT_VERSION:-unknown}" "$LATEST_VERSION"
curl --fail --silent --show-error --location --retry 3 \
  "$BASE_URL/$ASSET" -o "$TMP_DIR/$ASSET"
curl --fail --silent --show-error --location --retry 3 \
  "$BASE_URL/$CHECKSUMS" -o "$TMP_DIR/$CHECKSUMS"
(
  cd "$TMP_DIR"
  grep -F "  $ASSET" "$CHECKSUMS" | sha256sum --check --status -
)
tar -xzf "$TMP_DIR/$ASSET" -C "$TMP_DIR"
[[ -f "$TMP_DIR/muxgoob" ]] || { echo "Release archive has no muxgoob binary" >&2; exit 1; }

# Stage inside APP_DIR so the final rename is atomic. The existing executable
# remains mapped by the running process until systemd restarts it.
install -m 0755 "$TMP_DIR/muxgoob" "$STAGED_BINARY"
printf '%s\n' "$LATEST_VERSION" > "$STAGED_VERSION"
cp -p "$APP_DIR/muxgoob" "$BACKUP_BINARY"
[[ -f "$VERSION_FILE" ]] && cp -p "$VERSION_FILE" "$BACKUP_VERSION" || : > "$BACKUP_VERSION"
mv -f "$STAGED_BINARY" "$APP_DIR/muxgoob"
mv -f "$STAGED_VERSION" "$VERSION_FILE"
sudo -n chown "$USER:$USER" "$APP_DIR/muxgoob" "$VERSION_FILE"

# systemctl restart sends SIGTERM. Gooby drains active work for up to ten minutes.
if ! sudo -n systemctl restart "$SERVICE"; then
  echo "Restart failed; restoring previous binary" >&2
  mv -f "$BACKUP_BINARY" "$APP_DIR/muxgoob"
  if [[ -s "$BACKUP_VERSION" ]]; then
    mv -f "$BACKUP_VERSION" "$VERSION_FILE"
  else
    rm -f "$VERSION_FILE"
  fi
  sudo -n chown "$USER:$USER" "$APP_DIR/muxgoob" "$VERSION_FILE" 2>/dev/null || true
  sudo -n systemctl restart "$SERVICE" || true
  exit 1
fi

printf 'Updated and gracefully restarted: %s\n' "$LATEST_VERSION"
