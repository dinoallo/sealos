#!/bin/bash
set -euo pipefail
set -o noglob

# Compatibility wrapper for the native installer.
# New installations should use:
#   sealos distribution install
#
# Existing SEALOS_V2_* environment variables remain supported by the CLI.

if [[ -f "sealos.env" ]]; then
  # shellcheck disable=SC1091
  source sealos.env
fi

usage() {
  cat <<'HELP'
Usage:
  sealos distribution install [distribution@version]
  ./install-v2.sh [--distribution distribution@version]

The native CLI prompts for missing values. For automation, export the
existing SEALOS_V2_* variables and this wrapper will disable prompting.

Examples:
  sealos distribution install
  sealos distribution install cloud@v5.1.0 --masters 192.0.2.10:22 --cloud-domain 192.0.2.10.nip.io
  SEALOS_V2_MASTERS=192.0.2.10:22 \
  SEALOS_V2_CLOUD_DOMAIN=192.0.2.10.nip.io \
  ./install-v2.sh
HELP
}

distribution_ref="${SEALOS_V2_DISTRIBUTION:-cloud@v5.1.0}"
while [[ $# -gt 0 ]]; do
  case "$1" in
    -h|--help)
      usage
      exit 0
      ;;
    -d|--distribution)
      if [[ $# -lt 2 ]]; then
        echo "missing value for $1" >&2
        exit 2
      fi
      distribution_ref="$2"
      shift 2
      ;;
    --distribution=*)
      distribution_ref="${1#*=}"
      shift
      ;;
    *)
      echo "unknown argument: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

sealos_bin="${SEALOS_BIN:-sealos}"
if ! command -v "$sealos_bin" >/dev/null 2>&1; then
  cli_version="${SEALOS_V2_CLI_VERSION:-v5.1.0}"
  proxy_prefix=""
  if [[ "${SEALOS_V2_PROXY:-false}" == "true" ]]; then
    proxy_prefix="https://ghfast.top"
  fi
  install_url="https://raw.githubusercontent.com/labring/sealos/main/scripts/install.sh"
  if [[ -n "$proxy_prefix" ]]; then
    install_url="${proxy_prefix%/}/$install_url"
  fi
  echo "Sealos CLI is not installed. Install version ${cli_version}? [y/N]" >&2
  read -r answer
  if [[ "${answer,,}" != "y" ]]; then
    echo "Please install sealos and run this command again." >&2
    exit 1
  fi
  curl -fsSL "$install_url" | PROXY_PREFIX="$proxy_prefix" sh -s "$cli_version" labring/sealos
  sealos_bin="${SEALOS_BIN:-sealos}"
fi

exec "$sealos_bin" distribution install --interactive=false --distribution "$distribution_ref"
