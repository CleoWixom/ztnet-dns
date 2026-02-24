# Build on Linux from source

## 1) Clone repository

```bash
git clone https://github.com/CleoWixom/ztnet-dns.git
cd ztnet-dns
```

## 2) Install dependencies

```bash
make install-deps
make ensure-go
```

## 3) Verify source, tests and embedded release version log

```bash
make verify
make verify-release-log
```

## 4) Build plugin packages

```bash
make build-plugin
```

## 5) Build CoreDNS with `ztnet` plugin

```bash
make build-coredns
```

Output binary: `$(COREDNS_WORKDIR)/coredns`.

## 6) Install into system

```bash
sudo make install
```

This installs:

- `/usr/sbin/coredns`
- `/etc/coredns/Corefile` (if missing)
- `/lib/systemd/system/coredns-ztnet.service`
- `/usr/bin/ztnetool`

## 7) Print embedded version

```bash
make version
```
