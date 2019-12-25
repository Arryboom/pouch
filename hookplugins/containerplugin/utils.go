package containerplugin

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/alibaba/pouch/apis/types"
	"github.com/alibaba/pouch/daemon/mgr"
	"github.com/alibaba/pouch/pkg/log"

	"github.com/pkg/errors"
	"github.com/vishvananda/netlink"
)

var (
	networkLock sync.Mutex
	pouchClient = http.Client{
		Transport: &http.Transport{
			Dial: func(network, addr string) (net.Conn, error) {
				return net.Dial("unix", "/var/run/pouchd.sock")
			},
		},
		Timeout: time.Second * 30,
	}
)

func getEnv(env []string, key string) string {
	for _, pair := range env {
		parts := strings.SplitN(pair, "=", 2)
		if parts[0] == key {
			if len(parts) == 2 {
				return parts[1]
			}
			return ""
		}
	}
	return ""
}

func addParamsForOverlay(m map[string]string, env []string) {
	if getEnv(env, "OverlayNetwork") == optionOn {
		m["OverlayNetwork"] = optionOn
		m["OverlayTunnelId"] = getEnv(env, "OverlayTunnelId")
		m["OverlayGwIp"] = getEnv(env, "OverlayGwIp")
	}
	if getEnv(env, "VpcECS") == optionOn {
		m["VpcECS"] = optionOn
	}
	for _, oneEnv := range env {
		arr := strings.SplitN(oneEnv, "=", 2)
		if len(arr) == 2 && strings.HasPrefix(arr[0], "alinet_") {
			m[arr[0]] = arr[1]
		}
	}
}

func findBridgeIf() string {
	if iface, err := netlink.LinkByName("p0"); err == nil && iface != nil {
		return "p0"
	}

	if iface, err := netlink.LinkByName("docker0"); err == nil && iface != nil {
		return "docker0"
	}

	return "p0"
}

func prepareNetwork(ctx context.Context, requestedIP, defaultRoute, mask, nic string, networkMode string, EndpointsConfig map[string]*types.EndpointSettings, rawEnv []string) (nwName string, err error) {
	nwName = networkMode
	nwIf := nic

	if requestedIP == "" {
		if checkNatBridge() {
			return
		}
		return nwName, errors.Errorf("bridge network must set -e RequestedIP")
	}

	if nic == "bond0" || nic == "docker0" {
		nwIf = findBridgeIf()
		nwName = nwIf + "_" + defaultRoute
	} else if networkMode == "aisnet" {
		nwName = "aisnet_" + defaultRoute
	} else {
		nwName = nwName + "_" + defaultRoute
	}
	if getEnv(rawEnv, "OverlayNetwork") == optionOn {
		nwName = nwName + ".overlay"
	}
	log.With(ctx).Infof("create container network params %s %s %s %s %s", requestedIP, defaultRoute, mask, nic, networkMode)

	// 如果设置了网络四元组，同时网络是默认网络、bridge、自定义网络，则网络四元组才能生效
	if requestedIP != "" && defaultRoute != "" && mask != "" && nic != "" &&
		(networkMode == "default" || networkMode == "bridge" || networkMode == nwName) {
		//create network if not exist
		networkLock.Lock()
		defer networkLock.Unlock()
		nwArr, err := getAllNetwork()
		if err != nil {
			return "", err
		}

		var nw *types.NetworkResource
		for _, one := range nwArr {
			if one.Name == nwName {
				nw = &one
				break
			}
		}
		if nw == nil {
			//create network since it is not exist
			network := net.IPNet{IP: net.ParseIP(requestedIP).To4(), Mask: net.IPMask(net.ParseIP(mask).To4())}
			nc := types.NetworkCreate{
				Driver: "alinet",
				IPAM: &types.IPAM{
					Driver: "alinet",
					Config: []types.IPAMConfig{{Subnet: network.String(), IPRange: network.String(), Gateway: defaultRoute}},
				},
				Options: map[string]string{
					"nic": nwIf,
				},
			}
			arr := strings.Split(nwIf, ".")
			if len(arr) == 2 && arr[1] != "" {
				nc.Options["vlan-id"] = arr[1]
			}

			createNwReq := types.NetworkCreateConfig{Name: nwName, NetworkCreate: nc}
			addParamsForOverlay(nc.Options, rawEnv)
			err := CreateNetwork(ctx, &createNwReq)
			if err != nil {
				return "", err
			}
		}
	} else {
		nwName = networkMode
	}

	if defaultObj, exist := EndpointsConfig[nwName]; !exist || defaultObj.IPAMConfig == nil {
		EndpointsConfig[nwName] = &types.EndpointSettings{IPAMConfig: &types.EndpointIPAMConfig{}}
	}
	if EndpointsConfig[nwName].IPAMConfig.IPV4Address != requestedIP {
		EndpointsConfig[nwName].IPAMConfig.IPV4Address = requestedIP
	}

	log.With(ctx).Infof("create container network params from endpoint config %s %s %s %s %s", EndpointsConfig[nwName].IPAMConfig.IPV4Address, defaultRoute, mask, nic, nwName)

	return nwName, nil
}

func getAllNetwork() (nr []types.NetworkResource, err error) {
	resp, err := pouchClient.Get("http://127.0.0.1/v1.24/networks")
	if err != nil {
		return
	}
	defer resp.Body.Close()
	if err = json.NewDecoder(resp.Body).Decode(&nr); err != nil {
		return
	}
	return
}

func checkNatBridge() bool {
	content, err := ioutil.ReadFile("/etc/vlan.conf")
	if err != nil {
		return false
	}

	for _, line := range bytes.Split(content, []byte{'\n'}) {
		if bytes.Contains(line, []byte("nat")) {
			return true
		}
	}

	return false
}

// CreateNetwork create a network through pouch client
func CreateNetwork(ctx context.Context, c *types.NetworkCreateConfig) error {
	var rw bytes.Buffer
	err := json.NewEncoder(&rw).Encode(c)
	if err != nil {
		return err
	}
	resp, err := pouchClient.Post("http://127.0.0.1/v1.24/networks/create", "application/json", &rw)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, err := ioutil.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return err
	}
	log.With(ctx).Infof("create network return %s", string(b))
	if strings.Contains(string(b), "failed") {
		return fmt.Errorf(string(b))
	}
	return nil
}

func mustRequestedIP() bool {
	b, err := ioutil.ReadFile("/etc/sysconfig/pouch")
	if err != nil {
		return false
	}
	for _, line := range bytes.Split(b, []byte{'\n'}) {
		if bytes.Contains(line, []byte("--must-requested-ip")) && !bytes.HasPrefix(line, []byte("#")) {
			return true
		}
	}

	return false
}

func escapseLableToEnvName(k string) string {
	k = strings.Replace(k, "\\", "_", -1)
	k = strings.Replace(k, "$", "_", -1)
	k = strings.Replace(k, ".", "_", -1)
	k = strings.Replace(k, " ", "_", -1)
	k = strings.Replace(k, "\"", "_", -1)
	k = strings.Replace(k, "'", "_", -1)
	k = strings.Replace(k, ":", "_", -1)
	return fmt.Sprintf("label__%s", k)
}

// GenerateMACFromIP returns a locally administered MAC address where the 4 least
// significant bytes are derived from the IPv4 address.
func GenerateMACFromIP(ip net.IP) net.HardwareAddr {
	return genMAC(ip)
}

func genMAC(ip net.IP) net.HardwareAddr {
	hw := make(net.HardwareAddr, 6)
	// The first byte of the MAC address has to comply with these rules:
	// 1. Unicast: Set the least-significant bit to 0.
	// 2. Address is locally administered: Set the second-least-significant bit (U/L) to 1.
	hw[0] = 0x02
	// The first 24 bits of the MAC represent the Organizationally Unique Identifier (OUI).
	// Since this address is locally administered, we can do whatever we want as long as
	// it doesn't conflict with other addresses.
	hw[1] = 0x42
	// Fill the remaining 4 bytes based on the input
	if ip == nil {
		rand.Read(hw[2:])
	} else {
		copy(hw[2:], ip.To4())
	}
	return hw
}

// UniqueStringSlice removes the duplicate items.
func UniqueStringSlice(s []string) []string {
	h := make(map[string]struct{})
	for i := range s {
		h[s[i]] = struct{}{}
	}

	res := make([]string, 0, len(h))
	for key := range h {
		res = append(res, key)
	}
	return res
}

// addEnvironment add pouch environment pouch_container_image and pouch_container_id
func addEnvironment(image, id, runtime string, env []string) []string {
	var res []string
	for _, v := range env {
		if strings.Contains(v, "pouch_container_image=") ||
			strings.Contains(v, "pouch_container_id=") ||
			strings.Contains(v, "ali_runtime_type") {
			continue
		}
		res = append(res, v)
	}

	pouchContainerImage := fmt.Sprintf("pouch_container_image=%s", image)
	res = append(res, pouchContainerImage)
	pouchContainerID := fmt.Sprintf("pouch_container_id=%s", id)
	return append(res, pouchContainerID, fmt.Sprintf("ali_runtime_type=%s", runtime))
}

// isCopyPodHostsOn verify whether container is set copyPodHosts
func isCopyPodHostsOn(config *types.ContainerConfig, hostConfig *types.HostConfig) bool {
	if mgr.IsHost(hostConfig.NetworkMode) {
		return false
	}

	if !config.DisableNetworkFiles {
		return false
	}

	if config.Labels[copyPodHostsLabelKey] != optionOn {
		return false
	}

	return true
}

// updateContainerForPodHosts should be called in prestart hook if isCopyPodHostsOn returns true
// if set mounts for /etc/hosts /etc/hostname or /etc/resolv.conf, it will record their host paths
// in prestart hook args and remove these mounts. This function returns prestart priority and hook path.
func updateContainerForPodHosts(c *mgr.Container) (int, []string) {
	resolvConfPath := c.ResolvConfPath
	hostsPath := c.HostsPath
	hostnamePath := c.HostnamePath

	mounts := []*types.MountPoint{}

	for _, m := range c.Mounts {
		if m.Destination == "/etc/hosts" {
			hostsPath = m.Source
			continue
		}

		if m.Destination == "/etc/resolv.conf" {
			resolvConfPath = m.Source
			continue
		}

		if m.Destination == "/etc/hostname" {
			hostnamePath = m.Source
			continue
		}

		mounts = append(mounts, m)
	}

	c.Mounts = mounts

	// if resolvConfPath is nil, not set prestart hook
	if resolvConfPath == "" {
		return 0, nil
	}

	// set container HostnamePath to resolvConfPath
	c.ResolvConfPath = resolvConfPath

	args := []string{"/opt/ali-iaas/pouch/bin/prestart_hook_alipay", "CopyPodHosts", resolvConfPath}

	if hostsPath != "" {
		args = append(args, hostsPath)
		// set container HostsPath to hostsPath
		c.HostsPath = hostsPath
	} else {
		// if hostsPath is nil, set none to arg
		args = append(args, "none")
	}

	if hostnamePath != "" {
		args = append(args, hostnamePath)
		// set container HostnamePath to hostnamePath
		c.HostnamePath = hostnamePath
	} else {
		// if hostnamePath is nil, set none to arg
		args = append(args, "none")
	}

	// prestart_copy_pod_hosts_hook should execute after other prestart hooks
	return -200, args
}

// isCpusharePreload verify whether container is set prestart_cpushare_preload
func isCpusharePreload(config *types.ContainerConfig) bool {
	if getEnv(config.Env, "ALIPAY_SIGMA_CPUMODE") == "cpushare" {
		return true
	}

	return false
}

// adaptKataBlockfile provides block files for kata runtime, these block files will
// be sent to kata runtime as mount point, while kata will mount them as block device
// in kata container instead of 9pfs. Before the function called, Container Parameters:
//  {
//  	"Config": {
//  		"Env" : [
//				"pouch_kata_blockfile_host_container_path" : "raw.file:/home/t4;raw.file:/home/",
//				"pouch_kata_blockfile_fs_type" : "ext4",
//				...
//  				],
//  			},
//		"Mounts":[
//			{
//				"Source": "/tmp/blockdir1/",
//				"Destination": "/home/t4",
//			},
//			{
//				"Source": "/tmp/raw.file",
//				"Destination": "/home",
//			},
//			...
//		]
//	}
//
//	The env of "pouch_kata_blockfile_host_container_path" tells the block file mounts in Mounts, the env contains
//	array of "<relativePathInHostPath>:<containerPath>" which is split by ";". .As here, the Destination are
//	"/home/t4" and  "/home" in Mounts, and first part <relativePathInHostPath> will be joined the Source if the path
// is dir, in consideration of k8s CSI only set bind mount of dir path. The env of "pouch_kata_blockfile_fs_type" will
// set to mount type. After Calling function, Container Parameters:
//     {
//		"Mounts":[
//			{
//				"Source": "/tmp/blockdir1/raw.file",
//				"Destination": "/home/t4",,
//				"Type" : "ext4"
//			},
//			{
//				"Source": "/tmp/raw.file",
//				"Destination": "/home",
//				"Type": "ext4"
//			},
//			...
//		],
//		...
//   }
func adaptKataBlockfile(c *mgr.Container) error {
	var (
		blockfilePaths string
		fsType         string
	)

	if !(c.HostConfig.Runtime == kataRuntimeClass) {
		return nil
	}

	blockfilePaths = getEnv(c.Config.Env, blockFileEnvKey)
	if blockfilePaths == "" {
		return nil
	}

	dstSrcMap := map[string]string{}
	srcDstPaths := strings.Split(blockfilePaths, ";")
	for _, ph := range srcDstPaths {
		kvs := strings.Split(ph, ":")
		if len(kvs) != 2 {
			return fmt.Errorf("blockfilePaths is invaild: %s", blockfilePaths)
		}

		dstSrcMap[kvs[1]] = kvs[0]
	}

	fsType = getEnv(c.Config.Env, blockFileTypeEnvKey)
	if fsType == "" {
		fsType = "ext4"
	}

	if _, ok := blockFileTypeMap[fsType]; !ok {
		return fmt.Errorf("not support fs type %s for blockfile", fsType)
	}

	for _, mount := range c.Mounts {
		srcRelativePath, exist := dstSrcMap[mount.Destination]
		if !exist {
			continue
		}

		fi, err := os.Stat(mount.Source)
		if err != nil {
			return fmt.Errorf("mount src path stat error: %v", err)
		}

		// if src is dir, join srcRelativePath to the dir
		if fi.IsDir() {
			mount.Source = filepath.Join(mount.Source, srcRelativePath)
		}

		mount.Type = fsType

		log.With(nil).Infof("blockfile src path: %s, dest path: %s, fs type %s", mount.Source, mount.Destination, fsType)
	}

	return nil
}

// isOdpsHook verify whether container is run odps hook
func isOdpsHook(config types.ContainerConfig, hostConfig types.HostConfig) bool {
	if hostConfig.Runtime != kataRuntimeClass {
		return false
	}

	if getEnv(config.Env, "UNDERLAY_CHECK") != "self" {
		return false
	}

	// only adapt to alipay container
	if getEnv(config.Env, "ali_run_mode") != "alipay_container" {
		return false
	}

	return true
}
