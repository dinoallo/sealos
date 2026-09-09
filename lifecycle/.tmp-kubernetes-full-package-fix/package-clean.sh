#!/bin/sh
set -eu

if command -v kubeadm >/dev/null 2>&1; then
  kubeadm reset --force >/dev/null 2>&1 || true
fi

systemctl stop kubelet image-cri-shim 2>/dev/null || true
systemctl disable kubelet image-cri-shim 2>/dev/null || true
systemctl daemon-reload 2>/dev/null || true

rm -f /etc/systemd/system/kubelet.service
rm -f /etc/systemd/system/image-cri-shim.service
rm -rf /etc/systemd/system/kubelet.service.d
rm -rf /etc/kubernetes /var/lib/kubelet /var/lib/etcd
rm -f /etc/image-cri-shim.yaml /etc/crictl.yaml
rm -f /usr/bin/conntrack /usr/bin/crictl /usr/bin/kubeadm /usr/bin/kubectl /usr/bin/kubelet
rm -f /usr/bin/image-cri-shim /usr/bin/kubelet-pre-start.sh /usr/bin/kubelet-post-stop.sh
