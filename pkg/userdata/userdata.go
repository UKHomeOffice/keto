/*
Copyright 2017 The Keto Authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package userdata

import (
	"bytes"
	"text/template"
)

// UserData defines a user data struct
type UserData struct {
}

// New returns a new UserData struct
func New() *UserData {
	return &UserData{}
}

// RenderMasterCloudConfig renders a master cloud-config.
func (u UserData) RenderMasterCloudConfig(
	cloudProviderName string,
	clusterName string,
	kubeVersion string,
	masterPersistentNodeIDIP map[string]string,
) ([]byte, error) {

	const masterTemplate = `#cloud-config

coreos:
  update:
    reboot-strategy: "off"
  units:
  - name: systemd-sysctl.service
    command: restart
  - name: systemd-resolved.service
    command: restart
  - name: systemd-networkd.service
    command: restart
  - name: update-engine.service
    command: stop
    enable: false
  - name: smilodon.service
    command: start
    enable: true
    content: |
      [Unit]
      Description=Smilodon - manage ebs+eni attachment
      [Service]
      Environment="URL=https://github.com/UKHomeOffice/smilodon/releases/download/v0.0.4/smilodon-0.0.4-linux-amd64"
      Environment="OUTPUT_FILE=/opt/bin/smilodon"
      Environment="MD5SUM=071d32e53fdb53fa17c7bbe03744fdf6"
      ExecStartPre=/usr/bin/mkdir -p /opt/bin
      ExecStartPre=/usr/bin/bash -c 'until [[ -x ${OUTPUT_FILE} ]] && [[ $(md5sum ${OUTPUT_FILE} | cut -f1 -d" ") == ${MD5SUM} ]]; do wget -q -O ${OUTPUT_FILE} ${URL} && chmod +x ${OUTPUT_FILE}; done'
      ExecStart=/opt/bin/smilodon \
        --filters='tag:managed-by-keto:true,tag:cluster-name={{ .ClusterName }},tag-key=NodeID' \
        --create-file-system \
        --mount-fs \
        --mount-point=/data
      Restart=always
      RestartSec=10
      TimeoutStartSec=300
  # This is a dirty workaround hack until this has been fixed: https://github.com/systemd/systemd/issues/1784
  - name: networkd-restart.service
    command: start
    enable: true
    content: |
      [Unit]
      Description=Restart systemd-networkd when DOWN interface is found
      [Service]
      ExecStart=/usr/bin/bash -c 'while true; do ip -o -4 link show | grep -q "eth[0-1]:.*state DOWN" && systemctl restart systemd-networkd; sleep 60; done'
      Restart=always
      RestartSec=10
  - name: 20-eth1.network
    runtime: false
    content: |
      [Match]
      Name=eth1
      [Network]
      DHCP=ipv4
      [DHCP]
      SendHostname=true
      UseRoutes=false
      RouteMetric=2048
  - name: etcd-member.service
    enable: true
    command: start
    drop-ins:
    - name: 10-etcd-member.conf
      content: |
        [Service]
        EnvironmentFile=/etc/etcd.env
        EnvironmentFile=/run/smilodon/environment
        Environment=ETCD_CLIENT_CERT_AUTH=true
        Environment=ETCD_INITIAL_CLUSTER_STATE=new
        Environment=ETCD_IMAGE_TAG=v3.1.5
        Environment=ETCD_SSL_DIR=/run/etcd/certs
        Environment=ETCD_DATA_DIR=/data/etcd

        # Create the ETCD certs from the ETCD CA
        ExecStartPre=/bin/mkdir -p /run/etcd/certs
        ExecStartPre=/bin/grep ' /data ' /proc/mounts
        ExecStartPre=/usr/bin/docker run \
          -v /run/etcd/certs:/etc/ssl/certs \
          -v /run/kubeapiserver:/run/kubeapiserver \
          -v /data/ca/etcd:/data/ca/etcd \
          -e ETCD_CA_FILE \
          -e ETCD_CERT_FILE \
          -e ETCD_INITIAL_CLUSTER \
          -e ETCD_KEY_FILE \
          -e ETCD_PEER_CERT_FILE \
          -e ETCD_PEER_KEY_FILE \
          quay.io/ukhomeofficedigital/kmm \
          etcdcerts \
          --etcd-ca-key /data/ca/etcd/ca.key \
          --etcd-client-cert /run/kubeapiserver/etcd-client.crt \
          --etcd-client-key /run/kubeapiserver/etcd-client.key \
          --etcd-local-hostnames ${NODE_IP},localhost,127.0.0.1

        # Only mount the public key for ETCD
        Environment="RKT_RUN_ARGS=\
          --volume data-ca-etcd,kind=host,source=/data/ca/etcd/ca.crt --mount volume=data-ca-etcd,target=/data/ca/etcd/ca.crt"

        ExecStartPre=/usr/bin/chown -R etcd:etcd ${ETCD_SSL_DIR}

        ExecStart=
        ExecStart=/usr/lib/coreos/etcd-wrapper \
          --name=Node${NODE_ID} \
          --advertise-client-urls=https://${NODE_IP}:2379 \
          --initial-advertise-peer-urls=https://${NODE_IP}:2380 \
          --listen-client-urls=https://${NODE_IP}:2379,https://localhost:2379 \
          --listen-peer-urls=https://${NODE_IP}:2380
  - name: docker.service
    enable: true
    drop-ins:
    - name: 10-opts.conf
      content: |
        [Service]
        Environment="DOCKER_OPTS=--iptables=false --log-opt max-size=100m --log-opt max-file=1 --default-ulimit=nofile=65536:65536 --default-ulimit=nproc=16384:16384 --default-ulimit=memlock=-1:-1"
  - name: kmm.service
    command: start
    enable: true
    content: |
      [Unit]
      Description=Kubernetes Multi-master
      Documentation=https://github.com/UKHomeOffice/kmm

      [Service]
      EnvironmentFile=/etc/environment
      EnvironmentFile=/etc/etcd.env

      # Make sure the API server can access JUST the etcd ca cert...
      ExecStartPre=/usr/bin/cp /data/ca/etcd/ca.crt /run/kubeapiserver/etcd-ca.crt

      # Generate / check master kubernetes resources...
      ExecStartPre=/usr/bin/docker run \
        --net host \
        -v /data/ca/kube:/data/ca/kube \
        -v /run/kubeapiserver:/run/kubeapiserver \
        -v /etc/kubernetes/:/etc/kubernetes/ \
        -e ETCD_ADVERTISE_CLIENT_URLS \
        -e ETCD_CA_FILE \
        quay.io/ukhomeofficedigital/kmm \
        --etcd-client-ca /run/kubeapiserver/etcd-ca.crt \
        --etcd-client-cert /run/kubeapiserver/etcd-client.crt \
        --etcd-client-key /run/kubeapiserver/etcd-client.key \
        --kube-ca-cert=/data/ca/kube/ca.crt \
        --kube-ca-key=/data/ca/kube/ca.key \
        --cloud-provider={{ .CloudProviderName }} \
        --etcd-endpoints=https://127.0.0.1:2379
      ExecStart=/usr/bin/bash -c "while true; do sleep 1000; done"
      Restart=always
      RestartSec=10
  - name: kubelet.service
    command: start
    enable: true
    content: |
      [Unit]
      Description=kubelet: The Kubernetes Node Agent
      Documentation=http://kubernetes.io/docs/

      [Service]
      Environment=KUBELET_IMAGE_URL=quay.io/coreos/hyperkube
      Environment=KUBELET_IMAGE_TAG={{ .KubeVersion }}_coreos.0
      Environment="RKT_OPTS=\
        --uuid-file-save=/var/run/kubelet-pod.uuid \
        --volume etc-resolv,kind=host,source=/etc/resolv.conf --mount volume=etc-resolv,target=/etc/resolv.conf \
        --volume var-lib-cni,kind=host,source=/var/lib/cni --mount volume=var-lib-cni,target=/var/lib/cni"
      EnvironmentFile=/etc/environment
      ExecStartPre=/bin/mkdir -p /etc/kubernetes/manifests
      ExecStartPre=/bin/mkdir -p /etc/kubernetes/cni/net.d
      ExecStartPre=/bin/mkdir -p /etc/kubernetes/checkpoint-secrets
      ExecStartPre=/bin/mkdir -p /srv/kubernetes/manifests
      ExecStartPre=/bin/mkdir -p /var/lib/cni

      ExecStartPre=-/usr/bin/rkt rm --uuid-file=/var/run/kubelet-pod.uuid
      ExecStart=/usr/lib/coreos/kubelet-wrapper \
        --allow-privileged=true \
        --cloud-provider={{ .CloudProviderName }} \
        --cluster-dns=10.96.0.10 \
        --cluster-domain=cluster.local \
        --cni-conf-dir=/etc/kubernetes/cni/net.d \
        --kubeconfig=/etc/kubernetes/kubelet.conf \
        --lock-file=/var/run/lock/kubelet.lock \
        --minimum-container-ttl-duration=3m0s \
        --network-plugin=cni \
        --hostname-override="${COREOS_PRIVATE_IPV4}" \
        --node-labels=master=true \
        --pod-manifest-path=/etc/kubernetes/manifests \
        --api-servers=https://${COREOS_PRIVATE_IPV4}:6443 \
        --require-kubeconfig=true \
        --image-gc-high-threshold=60 \
        --image-gc-low-threshold=40 \
        --logtostderr=true \
        --maximum-dead-containers-per-container=1 \
        --maximum-dead-containers=10 \
        --register-schedulable=false \
        --system-reserved=cpu=50m,memory=100Mi

      ExecStop=-/usr/bin/rkt stop --uuid-file=/var/run/kubelet-pod.uuid
      Restart=always
      RestartSec=5

      [Install]
      WantedBy=multi-user.target

write_files:
- path: /etc/etcd.env
  permissions: "0644"
  owner: root
  content: |
    # File used by both etcd-member.service and kmm.service
    ETCD_INITIAL_CLUSTER={{ range $id, $ip := .MasterPersistentNodeIDIP }}{{if $id}},{{end}}Node{{ $id }}=https://{{ $ip }}:2380{{ end }}
    ETCD_CA_FILE=/data/ca/etcd/ca.crt
    ETCD_CERT_FILE=/etc/ssl/certs/server.crt
    ETCD_KEY_FILE=/etc/ssl/certs/server.key
    ETCD_PEER_CA_FILE=/data/ca/etcd/ca.crt
    ETCD_PEER_CERT_FILE=/etc/ssl/certs/peer.crt
    ETCD_PEER_KEY_FILE=/etc/ssl/certs/peer.key

- path: /etc/kubernetes/cloud-config
  permissions: "0600"
  owner: root
  content: |
    [Global]
    DisableSecurityGroupIngress = true
    KubernetesClusterTag = "{{ .ClusterName }}"

- path: /etc/sysctl.d/10-disable-ipv6.conf
  permissions: 0644
  owner: root
  content: |
    net.ipv6.conf.all.disable_ipv6 = 1
# Seems the only way to override default sysctl options added by CoreOS
- path: /etc/sysctl.d/baselayout.conf
  permissions: 0644
  owner: root
  content: |
    net.ipv4.ip_forward = 1
    net.ipv4.conf.default.rp_filter = 2
    net.ipv4.conf.all.rp_filter = 2
    kernel.kptr_restrict = 1
- path: /etc/sysctl.d/50-coredump.conf
  permissions: 0644
  owner: root
  content: |
    kernel.core_pattern=' '
- path: /etc/sysctl.d/10-max_map_count.conf
  permissions: 0644
  owner: root
  content: |
    vm.max_map_count=262144
`

	data := struct {
		CloudProviderName        string
		ClusterName              string
		KubeVersion              string
		MasterPersistentNodeIDIP map[string]string
	}{
		CloudProviderName:        cloudProviderName,
		ClusterName:              clusterName,
		KubeVersion:              kubeVersion,
		MasterPersistentNodeIDIP: masterPersistentNodeIDIP,
	}

	t := template.Must(template.New("master-cloud-config").Parse(masterTemplate))
	var b bytes.Buffer
	if err := t.Execute(&b, data); err != nil {
		return b.Bytes(), err
	}

	return b.Bytes(), nil
}

// RenderComputeCloudConfig renders a compute cloud-config.
func (u UserData) RenderComputeCloudConfig(kubeVersion, kubeAPIURL string) ([]byte, error) {
	const computeTemplate = `#cloud-config
coreos:
  update:
    reboot-strategy: "off"
# TODO
`

	data := struct {
		KubeVersion string
		KubeAPIURL  string
	}{
		KubeVersion: kubeVersion,
		KubeAPIURL:  kubeAPIURL,
	}

	t := template.Must(template.New("compute-cloud-config").Parse(computeTemplate))
	var b bytes.Buffer
	if err := t.Execute(&b, data); err != nil {
		return b.Bytes(), err
	}

	return b.Bytes(), nil
}
