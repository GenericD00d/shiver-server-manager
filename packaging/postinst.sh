#!/bin/sh
# Managed by shiver-server-manager.
set -e
if [ "$1" = configure ]; then
  getent group shiver-admins >/dev/null || groupadd --system shiver-admins
  # 711: server users pass through to their version link in bin/.
  install -d -m 711 /var/lib/shiver-server-manager
  install -d -m 700 /var/lib/shiver-server-manager/vault
  install -d -m 755 /var/lib/shiver-server-manager/bin
  install -d -m 700 /etc/shiver-server-manager /etc/shiver-server-manager/instances
  install -d -m 711 /var/lib/sharkord
  install -d -m 700 /var/backups/sharkord
  touch /var/log/shiver-server-manager.log
  chmod 600 /var/log/shiver-server-manager.log
  if [ -d /run/systemd/system ]; then
    systemctl daemon-reload
    systemctl enable --now shiver-server-manager-health.timer shiver-server-manager-backup.timer
  fi
  echo "Run 'sudo shiver-server-manager' to finish setting up."
fi
exit 0
