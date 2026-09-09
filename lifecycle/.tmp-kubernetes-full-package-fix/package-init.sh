#!/usr/bin/env bash
set -euo pipefail

package_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
rootfs=$(CDPATH= cd -- "${package_dir}/../.." && pwd)
bin_dir=${BIN_DIR:-/usr/bin}
registry_domain=${registryDomain:-sealos.hub}
registry_port=${registryPort:-5000}
registry_username=${registryUsername:-admin}
registry_password=${registryPassword:-passw0rd}
sandbox_image=${sandboxImage:-pause:3.9}

install -D -m 0755 "${rootfs}/bin/kubeadm" "${bin_dir}/kubeadm"
install -D -m 0755 "${rootfs}/bin/kubectl" "${bin_dir}/kubectl"
install -D -m 0755 "${rootfs}/bin/kubelet" "${bin_dir}/kubelet"
install -D -m 0755 "${rootfs}/bin/crictl" "${bin_dir}/crictl"
install -D -m 0755 "${rootfs}/bin/conntrack" "${bin_dir}/conntrack"
install -D -m 0755 "${rootfs}/cri/image-cri-shim" "${bin_dir}/image-cri-shim"
install -D -m 0755 "${rootfs}/scripts/kubelet-pre-start.sh" "${bin_dir}/kubelet-pre-start.sh"
install -D -m 0755 "${rootfs}/scripts/kubelet-post-stop.sh" "${bin_dir}/kubelet-post-stop.sh"

install -D -m 0644 "${package_dir}/package-assets/image-cri-shim.service" /etc/systemd/system/image-cri-shim.service
install -D -m 0644 "${package_dir}/package-assets/kubelet.service" /etc/systemd/system/kubelet.service
install -D -m 0644 "${package_dir}/package-assets/10-kubeadm.conf" /etc/systemd/system/kubelet.service.d/10-kubeadm.conf
install -D -m 0644 "${rootfs}/etc/crictl.yaml" /etc/crictl.yaml
install -D -m 0644 "${rootfs}/etc/sysctl.d/sealos-k8s.conf" /etc/sysctl.d/sealos-k8s.conf
install -D -m 0644 "${rootfs}/etc/limits.d/sealos-k8s.conf" /etc/security/limits.d/sealos-k8s.conf
install -D -m 0644 "${rootfs}/statics/audit-policy.yml" /etc/kubernetes/audit-policy.yml

cat > /etc/image-cri-shim.yaml <<EOF
shim: /var/run/image-cri-shim.sock
cri: /var/run/containerd/containerd.sock
address: http://${registry_domain}:${registry_port}
force: true
debug: false
timeout: 15m
auth: ${registry_username}:${registry_password}
EOF

swapoff --all 2>/dev/null || true
for module in bridge br_netfilter ip_vs ip_vs_rr ip_vs_wrr ip_vs_sh nf_conntrack nf_conntrack_ipv4; do
  modprobe "${module}" >/dev/null 2>&1 || true
done
sysctl --system >/dev/null 2>&1 || true
systemctl stop firewalld >/dev/null 2>&1 || true
systemctl disable firewalld >/dev/null 2>&1 || true

systemctl daemon-reload
systemctl enable image-cri-shim.service
systemctl restart image-cri-shim.service
systemctl is-active --quiet image-cri-shim.service

registry_source="${rootfs}/registry/docker"
if [ -d "${registry_source}" ]; then
  mkdir -p "${registryData}/docker"
  cp -a "${registry_source}/." "${registryData}/docker/"
  systemctl restart registry
fi

crictl pull "${registry_domain}:${registry_port}/${sandbox_image}"
systemctl enable kubelet.service
echo "Kubernetes 1.28.15 runtime initialized"
