#!/bin/sh
# Runs inside the VM, as root, from /root/ci: installs the manager's package
# the way an admin would, then runs the PAM and end-to-end tests.
set -eux
. /etc/os-release
PW=Ci-Only-Passw0rd
case " $ID ${ID_LIKE:-} " in
  *" debian "*|*" ubuntu "*)
    export DEBIAN_FRONTEND=noninteractive
    apt-get update -q
    apt-get install -y -q cryptsetup dosfstools rsync sudo e2fsprogs curl
    apt-get install -y ./shiver-server-manager_*.deb ;;
  *suse*)
    zypper -n -q install cryptsetup dosfstools rsync sudo e2fsprogs curl tar
    zypper -n install --allow-unsigned-rpm ./shiver-server-manager-*.rpm ;;
  *)
    dnf -y -q install cryptsetup dosfstools rsync sudo e2fsprogs curl tar firewalld
    dnf -y install ./shiver-server-manager-*.rpm ;;
esac
getent group shiver-admins
test "$(stat -c %a /var/lib/shiver-server-manager)" = 711
test "$(stat -c %a /etc/sudoers.d/shiver-server-manager)" = 440
cat /etc/pam.d/shiver-server-manager
visudo -cf /etc/sudoers.d/shiver-server-manager
systemctl is-enabled shiver-server-manager-health.timer shiver-server-manager-backup.timer
systemctl stop shiver-server-manager-health.timer shiver-server-manager-backup.timer
getenforce 2>/dev/null || true
# What the SELinux policy says about Caddy's paths, for diagnosing denials.
grep -h caddy /etc/selinux/*/contexts/files/file_contexts* 2>/dev/null || true
command -v matchpathcon >/dev/null && matchpathcon /run/caddy /run/caddy/admin.sock /var/lib/caddy/admin.sock /var/lib/caddy || true

useradd -m -G shiver-admins ssm-admin
useradd -m ssm-outsider
echo "ssm-admin:$PW" | chpasswd
echo "ssm-outsider:$PW" | chpasswd
su ssm-admin -s /bin/sh -c "sudo -n /usr/bin/shiver-server-manager version"
if su ssm-outsider -s /bin/sh -c "sudo -n /usr/bin/shiver-server-manager version"; then echo "a non-admin got in"; exit 1; fi

cd /root/ci/pebble
PEBBLE_VA_ALWAYS_VALID=1 PEBBLE_WFE_NONCEREJECT=0 nohup ./pebble -config test/config/pebble-config.json > /root/ci/pebble.log 2>&1 &
cd /root/ci
chmod +x auth.test cli.test
echo "127.0.0.1 chat-a.example.com chat-b.example.com chat-c.example.com" >> /etc/hosts
for i in $(seq 30); do curl -sfk https://localhost:14000/dir >/dev/null && break; sleep 1; done

export SSM_PAM_ADMIN=ssm-admin SSM_PAM_OUTSIDER=ssm-outsider SSM_PAM_PASSWORD=$PW SSM_E2E_BIN=/usr/bin/shiver-server-manager
export SSM_ACME_CA=https://localhost:14000/dir SSM_ACME_CA_ROOT=/root/ci/pebble/test/certs/pebble.minica.pem SSM_PEBBLE_ROOTS=https://localhost:15000/roots/0
export SSM_PUBLIC_IP=$(ip -4 route get 1.1.1.1 | awk '{for (i = 1; i < NF; i++) if ($i == "src") print $(i + 1)}')
./auth.test -test.run TestPAM -test.v
./cli.test -test.run TestE2E -test.v -test.timeout 40m

# Removing the package leaves every server alone.
case " $ID ${ID_LIKE:-} " in
  *" debian "*|*" ubuntu "*) apt-get remove -y shiver-server-manager ;;
  *suse*) zypper -n remove shiver-server-manager ;;
  *) dnf -y remove shiver-server-manager ;;
esac
systemctl is-active sharkord@chat-a
test -d /var/lib/shiver-server-manager/vault
