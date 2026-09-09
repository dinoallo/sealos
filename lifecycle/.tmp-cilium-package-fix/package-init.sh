#!/usr/bin/env bash
set -euo pipefail

package_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
rootfs=$(CDPATH= cd -- "${package_dir}/../.." && pwd)
pod_cidr=${KUBEADM_POD_SUBNET:-10.0.0.0/10}
node_port_range=${KUBEADM_SERVICE_RANGE:-30000-50000}
mask_size=${CILIUM_MASKSIZE:-24}
native=${CILIUM_NATIVE:-false}
chart_dir=${package_dir}/payload/cilium/chart
values_file=${rootfs}/etc/cilium/cilium-values.yaml

install -D -m 0755 "${package_dir}/payload/usr/bin/helm" /usr/bin/helm
if [ ! -d "${chart_dir}" ] || [ ! -f "${chart_dir}/Chart.yaml" ]; then
  echo "Cilium chart is missing from package payload" >&2
  exit 1
fi
if [ ! -f "${values_file}" ]; then
  echo "Cilium values file is missing from package payload" >&2
  exit 1
fi

# Helm treats commas in --set values as separators; escape the comma in the
# Kubernetes NodePort range before passing the value as a single argument.
node_port_range=$(printf '%s' "${node_port_range}" | sed 's/-/\\,/' )
helm_args=(
  upgrade --install cilium "${chart_dir}"
  --namespace kube-system
  --create-namespace
  --wait
  --timeout 10m
  -f "${values_file}"
  --set-string "ipam.operator.clusterPoolIPv4PodCIDRList=${pod_cidr}"
  --set "ipam.operator.clusterPoolIPv4MaskSize=${mask_size}"
  --set-string "nodePort.range=${node_port_range}"
  --set hubble.enabled=false
  --set hubble.relay.enabled=false
  --set hubble.ui.enabled=false
)
if [ "${native}" = "true" ]; then
  helm_args+=(--set routingMode=native --set gke.enabled=true --set tunnelProtocol=)
  helm_args+=(--set-string "ipv4NativeRoutingCIDR=${pod_cidr}")
fi

/usr/bin/helm "${helm_args[@]}"
if [ -x "${package_dir}/dns.sh" ]; then
  bash "${package_dir}/dns.sh"
fi
echo "Cilium 1.17.1 initialized"
