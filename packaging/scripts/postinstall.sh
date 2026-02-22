#!/bin/bash
set -e

# Выдать право слушать привилегированные порты (53) без root
# Это безопаснее чем запускать весь процесс от root
setcap 'cap_net_bind_service=+ep' /usr/sbin/coredns

# Активировать и запустить сервис
systemctl daemon-reload
systemctl enable --now coredns-ztnet.service || true

echo ""
echo "coredns-ztnet установлен."
echo "Отредактируйте /etc/coredns/Corefile, затем:"
echo "  systemctl restart coredns-ztnet"
