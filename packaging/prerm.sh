#!/bin/sh
# Managed by shiver-server-manager.
# Removing the package never stops, changes or deletes a server: use
# "Remove everything" in the menu first if that's what you want.
# Runs before removing the .deb ("remove") or the .rpm ("0"); an upgrade
# ("upgrade", "1") leaves the timers running.
set -e
case "$1" in
  remove|0)
    if [ -d /run/systemd/system ]; then
      systemctl disable --now shiver-server-manager-health.timer shiver-server-manager-backup.timer shiver-server-manager-web.service || true
    fi
    ;;
esac
exit 0
