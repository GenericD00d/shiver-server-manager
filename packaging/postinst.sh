#!/bin/sh
# Managed by shiver-server-manager.
# Runs after installing or upgrading the .deb ("configure") or the .rpm
# ("1" install, "2" upgrade).
set -e
case "$1" in
  abort-*) exit 0 ;;
esac
getent group shiver-admins >/dev/null || groupadd --system shiver-admins
# The .rpm doesn't ship the PAM file: Fedora and RHEL name their shared
# stack password-auth, openSUSE common-auth like Debian.
if [ ! -e /etc/pam.d/shiver-server-manager ]; then
  if [ -e /etc/pam.d/password-auth ]; then
    printf '#%%PAM-1.0\n# Managed by shiver-server-manager\nauth     include password-auth\naccount  include password-auth\n' > /etc/pam.d/shiver-server-manager
  else
    printf '#%%PAM-1.0\n# Managed by shiver-server-manager\nauth     include common-auth\naccount  include common-account\n' > /etc/pam.d/shiver-server-manager
  fi
  chmod 644 /etc/pam.d/shiver-server-manager
fi
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
  # An upgrade replaces the program; the web page keeps running the old
  # one until it restarts. Only restarts it if it was already running.
  systemctl try-restart shiver-server-manager-web.service || true
  # Pick up a changed timer schedule.
  systemctl try-restart shiver-server-manager-backup.timer || true
fi
echo "Run 'sudo shiver-server-manager' to finish setting up."
exit 0
