#!/bin/bash
set -e

# Ensure dedicated system group/user exist before assigning capabilities
if ! getent group coredns >/dev/null; then
    groupadd --system coredns
fi

if ! getent passwd coredns >/dev/null; then
    useradd --system --gid coredns --home-dir /var/lib/coredns --no-create-home --shell /usr/sbin/nologin coredns
fi

# Grant permission to bind privileged port (53) without root
# Safer than running the full process as root
setcap 'cap_net_bind_service=+ep' /usr/sbin/coredns

# Enable and start service
systemctl daemon-reload
systemctl enable --now coredns-ztnet.service || true

echo ""
echo "coredns-ztnet installed."
echo "Edit /etc/coredns/Corefile, then run:"
echo "  systemctl restart coredns-ztnet"
