#!/usr/bin/env bash
set -euo pipefail

package_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
rootfs=$(CDPATH= cd -- "${package_dir}/../.." && pwd)
bin_dir=${BIN_DIR:-/usr/bin}
data_dir=${registryData:-/var/lib/registry}
config_dir=${registryConfig:-/etc/registry}
registry_user=${registryUsername:-admin}
registry_password=${registryPassword:-passw0rd}

case "$data_dir:$config_dir" in
  *:/|/:*) echo "refusing to use an unsafe registry path" >&2; exit 1 ;;
esac

install -D -m 0755 "${package_dir}/payload/usr/bin/registry" "${bin_dir}/registry"
install -D -m 0644 "${rootfs}/etc/registry.service" /etc/systemd/system/registry.service
install -D -m 0644 "${rootfs}/etc/registry/registry_config.yml" \
  "${config_dir}/registry_config.yml"
install -d "${data_dir}" "${config_dir}"

hash=$("${rootfs}/usr/bin/sealos-package-bcrypt" "${registry_password}")
printf '%s:%s\n' "${registry_user}" "${hash}" > "${config_dir}/registry_htpasswd"
chmod 0600 "${config_dir}/registry_htpasswd"

systemctl daemon-reload
systemctl enable registry.service
systemctl restart registry.service
systemctl is-active --quiet registry.service
echo "registry 2.8.3 initialized"
