#!/bin/sh
# Managed by shiver-server-manager.
set -e
if [ -d /run/systemd/system ]; then
  systemctl daemon-reload || true
fi
if [ "$1" = purge ]; then
  echo "Kept on purpose:"
  echo "  /var/lib/shiver-server-manager (the vault and records)"
  echo "  /var/lib/sharkord and any other storage locations (every server's data)"
  echo "  /var/backups/sharkord (backups)"
  echo "Delete them yourself once you're sure you don't need them."
fi
exit 0
