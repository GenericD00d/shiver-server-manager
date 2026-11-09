#!/bin/sh
# Managed by shiver-server-manager.
# Removing the package never stops, changes or deletes a server: use
# "Remove everything" in the menu first if that's what you want.
set -e
if [ "$1" = remove ] && [ -d /run/systemd/system ]; then
  systemctl disable --now shiver-server-manager-health.timer shiver-server-manager-backup.timer shiver-server-manager-web.service || true
fi
exit 0
