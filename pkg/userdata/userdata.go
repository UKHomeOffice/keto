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
	"os"
	"text/template"

	"github.com/UKHomeOffice/keto/pkg/constants"
)

// UserDater is an abstract interface for UserData, mainly for testing.
type UserDater interface {
	RenderMasterCloudConfig(string, string, string, map[string]string) ([]byte, error)
	RenderComputeCloudConfig(string, string, string) ([]byte, error)
}

// UserData defines a user data struct.
type UserData struct {
	Logger logger
}

// logger is a generic interface that is used for passing in a logger.
type logger interface {
	Printf(string, ...interface{})
}

// Compile-time check whether UserData type value implements UserDater interface.
var _ UserDater = (*UserData)(nil)

// New returns a new UserData struct
func New(logger logger) *UserData {
	return &UserData{Logger: logger}
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
  - name: flanneld.service
    mask: true
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
      Environment="URL=https://github.com/UKHomeOffice/smilodon/releases/download/v0.1.0/smilodon-0.1.0-linux-amd64"
      Environment="OUTPUT_FILE=/opt/bin/smilodon"
      Environment="MD5SUM=500aa5f37a332d8e680c7d707b524077"
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

        # Save the CA files from the cloudprovider
        ExecStartPre=/bin/grep ' /data ' /proc/mounts
        ExecStartPre=/usr/bin/docker run \
          --rm \
          --net host \
          -v /data/ca:/data/ca \
          -e ETCD_CA_FILE \
          {{ .KetoK8Image }} \
          save-assets \
          --cloud-provider={{ .CloudProviderName }} \
          --etcd-ca-key /data/ca/etcd/ca.key \
          --kube-ca-cert=/data/ca/kube/ca.crt \
          --kube-ca-key=/data/ca/kube/ca.key

        # Create the ETCD certs from the ETCD CA
        ExecStartPre=/bin/mkdir -p /run/etcd/certs
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
          {{ .KetoK8Image }} \
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
          --advertise-client-urls=https://${NODE_IP}:2379 \
          --initial-advertise-peer-urls=https://${NODE_IP}:2380 \
          --listen-client-urls=https://${NODE_IP}:2379,https://localhost:2379 \
          --listen-peer-urls=https://${NODE_IP}:2380 \
          --name=Node${NODE_ID}
  - name: docker.service
    enable: true
    drop-ins:
    - name: 10-opts.conf
      content: |
        [Service]
        Environment="DOCKER_OPTS=--iptables=false --log-opt max-size=100m --log-opt max-file=1 --default-ulimit=nofile=65536:65536 --default-ulimit=nproc=16384:16384 --default-ulimit=memlock=-1:-1"
  - name: keto-k8.service
    command: start
    enable: true
    content: |
      [Unit]
      Description=Keto K8 Service
      Documentation=https://github.com/UKHomeOffice/keto-k8

      [Service]
      Type=simple
      EnvironmentFile=/etc/environment
      EnvironmentFile=/etc/etcd.env

      # Make sure the API server can access JUST the etcd ca cert...
      ExecStartPre=/usr/bin/cp /data/ca/etcd/ca.crt /run/kubeapiserver/etcd-ca.crt

      # Generate / check master kubernetes resources...
      ExecStart=/usr/bin/docker run \
        --rm \
        --net host \
        -v /data/ca/kube:/data/ca/kube \
        -v /run/kubeapiserver:/run/kubeapiserver \
        -v /etc/kubernetes/:/etc/kubernetes/ \
        -v /var/run/dbus/:/var/run/dbus/ \
        -v /etc/systemd/system/:/etc/systemd/system/ \
        -e ETCD_INITIAL_CLUSTER \
        -e ETCD_ADVERTISE_CLIENT_URLS \
        -e ETCD_CA_FILE \
        {{ .KetoK8Image }} \
        master \
        --cloud-provider={{ .CloudProviderName }} \
        --etcd-client-ca /run/kubeapiserver/etcd-ca.crt \
        --etcd-client-cert /run/kubeapiserver/etcd-client.crt \
        --etcd-client-key /run/kubeapiserver/etcd-client.key \
        --etcd-endpoints=https://127.0.0.1:2379 \
        --kube-ca-cert=/data/ca/kube/ca.crt \
        --kube-ca-key=/data/ca/kube/ca.key \
        --network-provider={{ .NetworkProvider }}
      TimeoutStartSec=infinity
      RestartSec=20
      Restart=always

write_files:
- path: /etc/etcd.env
  permissions: "0644"
  owner: root
  content: |
    # File used by both etcd-member.service and keto-k8.service
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
		KetoK8Image              string
		MasterPersistentNodeIDIP map[string]string
		NetworkProvider          string
	}{
		CloudProviderName:        cloudProviderName,
		ClusterName:              clusterName,
		KubeVersion:              kubeVersion,
		KetoK8Image:              constants.DefaultKetoK8Image,
		MasterPersistentNodeIDIP: masterPersistentNodeIDIP,
		NetworkProvider:          constants.DefaultNetworkProvider,
	}

	t := template.Must(template.New("master-cloud-config").Parse(masterTemplate))
	var b bytes.Buffer
	if err := t.Execute(&b, data); err != nil {
		return b.Bytes(), err
	}

	u.Logger.Printf("cloud-config for masterpool: %s", b.String())

	return b.Bytes(), nil
}

// RenderComputeCloudConfig renders a compute cloud-config.
func (u UserData) RenderComputeCloudConfig(cloudProviderName, clusterName, kubeVersion string) ([]byte, error) {
	const computeTemplate = `#cloud-config
coreos:
  update:
    reboot-strategy: "off"
  units:
  - name: flanneld.service
    mask: true
  - name: docker.service
    drop-ins:
    - name: 10-opts.conf
      content: |
        [Service]
        Environment="DOCKER_OPTS=--iptables=false --log-opt max-size=100m --log-opt max-file=1 --default-ulimit=nofile=65536:65536 --default-ulimit=nproc=16384:16384 --default-ulimit=memlock=-1:-1"
  - name: keto-k8.service
    command: start
    enable: true
    content: |
      [Unit]
      Description=keto-k8 (compute)
      Documentation=https://github.com/UKHomeOffice/keto-k8

      [Service]
      Type=simple
      EnvironmentFile=/etc/environment

      # Generate / check keto-token env (only needed until we update keto-tokens)...
      ExecStartPre=/usr/bin/mkdir -p /etc/kubernetes
      ExecStart=/usr/bin/docker run \
        --rm \
        --net host \
        -v /etc/kubernetes/:/etc/kubernetes/ \
        -v /var/run/dbus/:/var/run/dbus/ \
        -v /etc/systemd/system/:/etc/systemd/system/ \
        {{ .KetoK8Image }} \
        setup-compute \
        --cloud-provider={{ .CloudProviderName }}

  - name: keto-tokens.service
    command: start
    enable: true
    content: |
      [Unit]
      Description=keto-tokens
      Documentation=https://github.com/UKHomeOffice/keto-tokens

      [Service]
      Type=simple
      EnvironmentFile=/etc/kubernetes/keto-token.env

      ExecStartPre=/usr/bin/docker run \
        --rm \
        --net host \
        -v /etc/kubernetes/:/etc/kubernetes/ \
        ${KETO_TOKENS_IMAGE} \
        --verbose \
        --cloud=${KETO_TOKENS_CLOUD} \
        client \
        --tag-name ${KETO_TOKENS_TAG} \
        --master ${KETO_TOKENS_API_URL} \
        --kubeconfig ${KETO_TOKENS_KUBELET_CONF}

      ExecStart=/usr/bin/bash -c "while true; do sleep 1000; done"
      Restart=always
      RestartSec=10

write_files:
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

	// TODO: remove this. This is only for testing until we find a better and safer way.
	ketoK8ImageURI := constants.DefaultKetoK8Image
	if uri := os.Getenv("KETO_K8_IMAGE_URI"); uri != "" {
		ketoK8ImageURI = uri
	}

	data := struct {
		ClusterName       string
		KubeVersion       string
		CloudProviderName string
		KetoK8Image       string
	}{
		ClusterName:       clusterName,
		KubeVersion:       kubeVersion,
		CloudProviderName: cloudProviderName,
		KetoK8Image:       ketoK8ImageURI,
	}

	t := template.Must(template.New("compute-cloud-config").Parse(computeTemplate))
	var b bytes.Buffer
	if err := t.Execute(&b, data); err != nil {
		return b.Bytes(), err
	}

	u.Logger.Printf("cloud-config for computepool: %s", b.String())

	return b.Bytes(), nil
}
