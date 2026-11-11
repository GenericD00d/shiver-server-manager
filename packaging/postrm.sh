#!/bin/sh
# Managed by shiver-server-manager.
set -e
if [ -d /run/systemd/system ]; then
  systemctl daemon-reload || true
fi
# "purge" (.deb) or "0" (the .rpm being erased).
if [ "$1" = purge ] || [ "$1" = 0 ]; then
  if [ "$1" = 0 ]; then
    # The .rpm doesn't own the PAM file postinst wrote.
    grep -q "Managed by shiver-server-manager" /etc/pam.d/shiver-server-manager 2>/dev/null && rm -f /etc/pam.d/shiver-server-manager
  fi
  echo "Kept on purpose:"
  echo "  /var/lib/shiver-server-manager (the vault and records)"
  echo "  /var/lib/sharkord and any other storage locations (every server's data)"
  echo "  /var/backups/sharkord (backups)"
  echo "Delete them yourself once you're sure you don't need them."
fi
exit 0
