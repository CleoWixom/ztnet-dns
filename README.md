# ZTNET DNS based on CoreDNS

Secure CoreDNS external plugin for serving ZeroTier member DNS records (A/AAAA) from ZTNET API with strict access control for the plugin zone.

## Run installer directly from GitHub

```bash
sudo bash <(curl -fsSL https://raw.githubusercontent.com/CleoWixom/ztnet-dns/main/scripts/install)
```

## Quick features

- Authoritative responses for your ZT zone only.
- Global DNS passthrough stays handled by other CoreDNS plugins (for example `forward`).
- Token is stored in file (`token_file`), not in Corefile.
- Token hot-rotation supported via `ztnetool`.
- Stale-on-error refresh behavior for resiliency.

## Corefile example

```corefile
ztnet.local {
    bind 192.168.55.1 fc4e:1d7b:a5ac:d8cf:20e2::1
    hosts {
        192.168.55.1 ztnet.local
        fc4e:1d7b:a5ac:d8cf:20e2::1 ztnet.local
    }
    ztnet {
        api_url http://ztnet.local:3000
        network_id 8056c2e21c000001
        zone ztnet.local
        token_file /run/secrets/ztnet_token
        auto_allow_zt true
        refresh 30s
        timeout 5s
        ttl 60
    }
    prometheus :9153
    errors
    log
}
```

## `ztnetool` (token + API helper)

```bash
ztnetool [option]
```

Options:

- `-t "<token>"` — set token into `/run/secrets/ztnet_token`. If token is omitted, asks interactive input.
- `-c` — verify token installation and API availability (`network_id` check).
- `-l` — list available controllers/networks.
- `-n [network_id or node_id]` — show network and connected clients.

Examples:

```bash
sudo ztnetool -t "<ZTNET_API_TOKEN>"
sudo ztnetool -c --api-url http://127.0.0.1:3000 --network-id 8056c2e21c000001
sudo ztnetool -l --api-url http://127.0.0.1:3000
sudo ztnetool -n 8056c2e21c000001 --api-url http://127.0.0.1:3000
```

## Installer flow (`scripts/install`)

Installer behavior:

1. Collects required values (`API_URL`, API token, `NETWORK_ID`, `ZONE`).
2. Optionally asks for advanced parameters (`NODE_IPV4`, `NODE_IPV6`, `AUTO_ALLOW_ZT`, `REFRESH`, `TIMEOUT`, `TTL`, `DNS_UPSTREAM`).
3. Installs dependencies:
   - `apt-get update`
   - `apt-get install -y git make build-essential ca-certificates curl dnsutils net-tools iproute2 golang`
4. Ensures `coredns:coredns` user/group exists.
5. Validates `zerotier-one` and applies `-U` patch to `/lib/systemd/system/zerotier-one.service` if required.
6. Checks port `53` availability on selected bind addresses.
7. Builds/installs CoreDNS with plugin and writes `/etc/coredns/Corefile`.
8. Saves token through `ztnetool` into `/run/secrets/ztnet_token`.
9. Validates API (`ztnetool -c`) and starts `coredns-ztnet.service`.

Generated Corefile template:

```corefile
{$ZONE} {
    bind {$NODE_IPV4} {$NODE_IPV6}
    hosts {
        {$NODE_IPV4} {$ZONE}
        {$NODE_IPV6} {$ZONE}
    }
    ztnet {
        api_url {$API_URL}
        network_id {$NETWORK_ID}
        zone {$ZONE}
        token_file {$FILE_TOKEN}
        auto_allow_zt {$AUTO_ALLOW_ZT}
        refresh {$REFRESH}
        timeout {$TIMEOUT}
        ttl {$TTL}
    }
    prometheus :9153
    errors
    log
}

. {
    bind 127.0.0.1 {$NODE_IPV4} {$NODE_IPV6}
    forward . {$DNS_UPSTREAM}
    cache
}
```

## Build from source

Build instructions moved to [`BUILD.md`](BUILD.md).

## Validation and tests

```bash
go test ./... -race -count=1
golangci-lint run ./...
```

## Versioning and release

- Version is derived from git (`make version`).
- No manual VERSION file or manual tags.
- Releases are automated by GitHub Actions on push to `main`.
- Commit markers control semantic bump:
  - `#patch` (or omitted) → patch
  - `#minor` → minor
  - `#major` → major

Example commits:

```bash
git commit -m "fix: token validation flow #patch"
git commit -m "feat: new installer options #minor"
git commit -m "refactor!: breaking config parser #major"
```
