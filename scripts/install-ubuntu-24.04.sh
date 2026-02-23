#!/usr/bin/env bash
set -euo pipefail

REPO_URL="https://github.com/CleoWixom/ztnet-dns.git"
DEFAULT_TOKEN_FILE="/run/secrets/ztnet_token"
DEFAULT_API_URL="http://127.0.0.1:3000"
DEFAULT_ZONE="zt.local"
WORKDIR=""

log() { printf '[ztnet-install] %s\n' "$*"; }
warn() { printf '[ztnet-install] WARNING: %s\n' "$*" >&2; }
fail() { printf '[ztnet-install] ERROR: %s\n' "$*" >&2; exit 1; }

cleanup() {
  if [[ -n "$WORKDIR" && -d "$WORKDIR" ]]; then
    rm -rf "$WORKDIR"
  fi
}
trap cleanup EXIT

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || fail "missing required command: $1"
}

prompt_api_url() {
  local api_url=""
  while true; do
    read -r -p "API_URL [default: ${DEFAULT_API_URL}]: " input
    api_url="${input:-$DEFAULT_API_URL}"
    if validate_url "$api_url"; then
      printf '%s\n' "$api_url"
      return 0
    fi
    warn "invalid or unreachable API_URL, try again"
  done
}

prompt_network_id() {
  local current="${1:-}"
  read -r -p "NETWORK_ID [default: ${current}]: " input
  printf '%s\n' "${input:-$current}"
}

prompt_token_file() {
  local current="${1:-$DEFAULT_TOKEN_FILE}"
  read -r -p "TOKEN ZTNET API file [default: ${current}]: " input
  printf '%s\n' "${input:-$current}"
}

validate_url() {
  local input="$1"
  [[ "$input" =~ ^https?://[^[:space:]]+$ ]] || return 1

  if ! curl -sS -o /dev/null --connect-timeout 5 --max-time 10 "$input"; then
    return 1
  fi
  return 0
}

read_token_file() {
  local token_file="$1"

  if ! sudo test -f "$token_file"; then
    warn "token file not found: ${token_file}"
    return 1
  fi

  local token
  token="$(sudo sh -c "tr -d '\\r' < '$token_file' | head -n1" 2>/dev/null || true)"
  if [[ -z "$token" ]]; then
    warn "token file is empty: ${token_file}"
    return 1
  fi

  printf '%s\n' "$token"
}

validate_token() {
  local api_url="$1"
  local network_id="$2"
  local token="$3"
  local endpoint="${api_url%/}/api/v1/network/${network_id}"

  local status
  status="$(curl -sS -o /dev/null -w '%{http_code}' \
    --connect-timeout 5 --max-time 15 \
    -H "x-ztnet-auth: ${token}" \
    "$endpoint" || true)"

  case "$status" in
    200) return 0 ;;
    401|403) return 2 ;;
    404) return 3 ;;
    000) return 4 ;;
    *) return 5 ;;
  esac
}

if [[ "${EUID}" -eq 0 ]]; then
  fail "run this script as a regular user with sudo access, not as root"
fi

if [[ -f /etc/os-release ]]; then
  . /etc/os-release
  if [[ "${ID:-}" != "ubuntu" || "${VERSION_ID:-}" != "24.04" ]]; then
    warn "this installer is designed for Ubuntu 24.04 (detected: ${PRETTY_NAME:-unknown})"
  fi
fi

require_cmd sudo
require_cmd curl
require_cmd git
require_cmd make
require_cmd rg

sudo -v

TOKEN_FILE_INPUT="$DEFAULT_TOKEN_FILE"
API_URL="$(prompt_api_url)"

read -r -p "ZONE [default: ${DEFAULT_ZONE}]: " input
ZONE="${input:-$DEFAULT_ZONE}"
[[ -n "$ZONE" ]] || fail "zone cannot be empty"

NETWORK_ID=""
NETWORK_ID="$(prompt_network_id "$NETWORK_ID")"

while true; do
  TOKEN_FILE_INPUT="$(prompt_token_file "$TOKEN_FILE_INPUT")"
  TOKEN="$(read_token_file "$TOKEN_FILE_INPUT" || true)"
  if [[ -z "$TOKEN" ]]; then
    warn "please update token file and try again"
    continue
  fi

  if validate_token "$API_URL" "$NETWORK_ID" "$TOKEN"; then
    log "token validation passed"
    break
  fi

  case "$?" in
    2)
      warn "token is invalid (HTTP 401/403). Update token file and try again." ;;
    3)
      warn "network not found (HTTP 404). Check NETWORK_ID and API_URL."
      NETWORK_ID="$(prompt_network_id "$NETWORK_ID")" ;;
    4)
      warn "failed to connect to API. Check API_URL and connectivity."
      API_URL="$(prompt_api_url)" ;;
    *)
      warn "API returned unexpected response. Update token file and try again." ;;
  esac
done

if sudo ss -luntp | rg -q '(:53\s)'; then
  warn "port 53 is currently in use. Installation may fail until the port is free."
  read -r -p "Attempt to stop systemd-resolved now? [y/N]: " stop_resolved
  if [[ "$stop_resolved" =~ ^[Yy]$ ]]; then
    sudo systemctl disable --now systemd-resolved || true
  fi
fi

WORKDIR="$(mktemp -d /tmp/ztnet-dns-install.XXXXXX)"
log "cloning repository to ${WORKDIR}"
git clone --depth=1 "$REPO_URL" "$WORKDIR/repo"

pushd "$WORKDIR/repo" >/dev/null

log "installing dependencies"
make install-deps

log "building and installing CoreDNS with ztnet plugin"
make install

log "writing /etc/coredns/Corefile"
sudo tee /etc/coredns/Corefile >/dev/null <<CORE
${ZONE} {
    ztnet {
        api_url ${API_URL}
        network_id ${NETWORK_ID}
        zone ${ZONE}
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

. {
    forward . 1.1.1.1 8.8.8.8
    cache
    errors
    log
}
CORE

log "installing token securely"
sudo /usr/bin/ztnet.token.install --token-file /run/secrets/ztnet_token --corefile /etc/coredns/Corefile --group coredns "$(sudo sh -c "tr -d '\r' < '$TOKEN_FILE_INPUT' | head -n1")"

log "restarting service"
sudo systemctl daemon-reload
sudo systemctl enable --now coredns-ztnet.service
sudo systemctl restart coredns-ztnet.service

log "done"
log "CoreDNS status:"
sudo systemctl --no-pager --full status coredns-ztnet.service || true

popd >/dev/null
