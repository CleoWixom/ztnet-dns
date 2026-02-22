#!/usr/bin/env bash
set -euo pipefail

TOKEN_FILE="/run/secrets/ztnet_token"
CORE_FILE="/etc/coredns/Corefile"
GROUP_NAME="coredns"
MODE="setup"
TOKEN=""

usage() {
  cat <<USAGE
Usage:
  $(basename "$0") [TOKEN] [options]

Description:
  Securely install/rotate ZTNET API token for ztnet-dns and verify Corefile token_file setting.

Token input methods (priority):
  1) positional TOKEN argument
  2) piped stdin (non-interactive): echo "token" | $(basename "$0")
  3) interactive prompt (hidden input)

Options:
  --token-file PATH   Token file path (default: ${TOKEN_FILE})
  --corefile PATH     CoreDNS Corefile path (default: ${CORE_FILE})
  --group NAME        Service group for token file ownership (default: ${GROUP_NAME})
  --rotate            Rotation mode (same secure write process)
  -h, --help          Show this help
USAGE
}

log() { printf '[ztnet-token] %s\n' "$*"; }
fail() { printf '[ztnet-token] ERROR: %s\n' "$*" >&2; exit 1; }

while [[ $# -gt 0 ]]; do
  case "$1" in
    --token-file)
      [[ $# -ge 2 ]] || fail "--token-file requires value"
      TOKEN_FILE="$2"; shift 2 ;;
    --corefile)
      [[ $# -ge 2 ]] || fail "--corefile requires value"
      CORE_FILE="$2"; shift 2 ;;
    --group)
      [[ $# -ge 2 ]] || fail "--group requires value"
      GROUP_NAME="$2"; shift 2 ;;
    --rotate)
      MODE="rotate"; shift ;;
    -h|--help)
      usage; exit 0 ;;
    --)
      shift; break ;;
    -*)
      fail "unknown option: $1" ;;
    *)
      if [[ -z "$TOKEN" ]]; then
        TOKEN="$1"
      else
        fail "unexpected extra argument: $1"
      fi
      shift ;;
  esac
done

if [[ -n "${1:-}" ]]; then
  fail "unexpected arguments: $*"
fi

if [[ $EUID -ne 0 ]]; then
  fail "run as root (required for writing ${TOKEN_FILE} and setting ownership/permissions)"
fi

if [[ -z "$TOKEN" && ! -t 0 ]]; then
  IFS= read -r TOKEN || true
fi

if [[ -z "$TOKEN" ]]; then
  log "No token was provided."
  log "Generate token in ZTNET UI (API tokens / personal access token) and paste it below."
  read -r -s -p "ZTNET API token: " TOKEN
  printf '\n'
fi

[[ -n "$TOKEN" ]] || fail "empty token"

if ! getent group "$GROUP_NAME" >/dev/null; then
  fail "group '${GROUP_NAME}' does not exist"
fi

TOKEN_DIR=$(dirname "$TOKEN_FILE")
install -d -m 0750 "$TOKEN_DIR"

TMP_FILE=$(mktemp "${TOKEN_FILE}.tmp.XXXXXX")
trap 'rm -f "$TMP_FILE"' EXIT

printf '%s\n' "$TOKEN" > "$TMP_FILE"
chown root:"$GROUP_NAME" "$TMP_FILE"
chmod 0440 "$TMP_FILE"
mv -f "$TMP_FILE" "$TOKEN_FILE"
trap - EXIT

log "Token ${MODE} completed: ${TOKEN_FILE}"
log "Owner/perm set to root:${GROUP_NAME} and 0440"

if [[ -f "$CORE_FILE" ]]; then
  if rg -n "^[[:space:]]*token_file[[:space:]]+${TOKEN_FILE//\//\/}[[:space:]]*$" "$CORE_FILE" >/dev/null; then
    log "Corefile check passed: token_file ${TOKEN_FILE}"
  else
    fail "Corefile check failed: '${CORE_FILE}' does not contain exact line: token_file ${TOKEN_FILE}"
  fi
else
  fail "Corefile not found: ${CORE_FILE}"
fi

log "Done. Token is not stored in Corefile; plugin reads token from file during refresh cycle."
