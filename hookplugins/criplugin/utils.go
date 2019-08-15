package criplugin

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	apitypes "github.com/alibaba/pouch/apis/types"
	runtime "github.com/alibaba/pouch/cri/apis/v1alpha2"
	critype "github.com/alibaba/pouch/cri/v1alpha2/types"
	"github.com/alibaba/pouch/pkg/log"

	"github.com/pkg/errors"
)

const (
	// annContainerRootFSWritableLayer annotation applies to the pod that need put
	// all its containers' rootfs writable layer into a kubernetes volume. Its value
	// is a convention hostpath, eg: /mnt/storage-backend/volume-name/.rootDir
	annContainerRootFSWritableLayer = "alibabacloud.com/rootfs-writable-layer"
)

var (
	envDiskQuota = "io.alibaba.pouch.vm.env.diskquota"
)

func updateNetworkEnv(ctx context.Context, createConfig *apitypes.ContainerCreateConfig, meta *critype.SandboxMeta) error {
	// TODO: only support ipv4
	netNSPath := meta.NetNS

	// skip kata container and nanovisor
	// There will be more than one kata runtime, such as kata-runtime, kata-shim-v2, kata-windows.
	if strings.HasPrefix(meta.Runtime, "kata-") || meta.Runtime == "runsc" {
		return nil
	}

	// skip nil network mode
	if createConfig.HostConfig.NetworkMode == "" {
		return nil
	}

	// skip sandbox pod is host mode.
	nsOpts := meta.Config.GetLinux().GetSecurityContext().GetNamespaceOptions()
	hostNet := nsOpts.GetNetwork() == runtime.NamespaceMode_NODE
	if hostNet {
		return nil
	}

	// get ip and mask
	ip, mask, err := getContainerIPAndMask(netNSPath, "eth0", "-4")
	if err != nil {
		return errors.Wrapf(err, "failed to get container's ip and mask, NetNSPath: (%s)", netNSPath)
	}
	log.With(ctx).Debugf("update network env, ip: (%s), mask: (%s)", ip, mask)
	createConfig.Env = setEnv(createConfig.Env, "RequestedIP", ip)
	createConfig.Env = setEnv(createConfig.Env, "DefaultMask", mask)

	// get gateway
	gateway, err := getContainerGateway(netNSPath, "eth0")
	if err != nil {
		return errors.Wrapf(err, "failed to get container's gateway, NetNSPath: (%s)", netNSPath)
	}
	log.With(ctx).Debugf("update network env, gateway: (%s)", gateway)
	createConfig.Env = setEnv(createConfig.Env, "DefaultRoute", gateway)

	return nil
}

func setEnv(env []string, key, value string) []string {
	index := -1
	for i, pair := range env {
		if strings.Split(pair, "=")[0] == key {
			index = i
			break
		}
	}

	newEnv := fmt.Sprintf("%s=%s", key, value)
	if index == -1 {
		env = append(env, newEnv)
	} else {
		env[index] = newEnv
	}

	return env
}

func getContainerIPAndMask(netnsPath, interfaceName, addrType string) (string, string, error) {
	nsenterPath, err := exec.LookPath("nsenter")
	if err != nil {
		return "", "", err
	}

	// Try to retrieve ip inside container network namespace
	output, err := exec.Command(nsenterPath, fmt.Sprintf("--net=%s", netnsPath), "-F", "--",
		"ip", "-o", addrType, "addr", "show", "dev", interfaceName, "scope", "global").CombinedOutput()
	if err != nil {
		return "", "", fmt.Errorf("Unexpected command output %s with error: %v", output, err)
	}

	lines := strings.Split(string(output), "\n")
	if len(lines) < 1 {
		return "", "", fmt.Errorf("Unexpected command output %s", output)
	}
	fields := strings.Fields(lines[0])
	if len(fields) < 4 {
		return "", "", fmt.Errorf("Unexpected address output %s ", lines[0])
	}

	// just support ipv4
	ip, ipNet, err := net.ParseCIDR(fields[3])
	if err != nil {
		return "", "", fmt.Errorf("CNI failed to parse ip from output %s due to %v", output, err)
	}

	maskLen, _ := ipNet.Mask.Size()
	mask := GetIPV4NetworkMaskBySize(maskLen)

	return ip.String(), mask, nil
}

func getContainerGateway(netnsPath, interfaceName string) (string, error) {
	nsenterPath, err := exec.LookPath("nsenter")
	if err != nil {
		return "", err
	}

	// Try to retrieve ip inside container network namespace
	output, err := exec.Command(nsenterPath, fmt.Sprintf("--net=%s", netnsPath), "-F", "--",
		"ip", "route", "show", "dev", interfaceName, "scope", "global").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("Unexpected command output %s with error: %v", output, err)
	}

	lines := strings.Split(string(output), "\n")
	if len(lines) < 1 {
		return "", fmt.Errorf("Unexpected command output %s", output)
	}
	fields := strings.Fields(lines[0])
	if len(fields) < 3 {
		return "", fmt.Errorf("Unexpected address output %s ", lines[0])
	}

	ip := net.ParseIP(fields[2])
	if ip == nil {
		return "", fmt.Errorf("CNI failed to parse route from output %s", output)
	}

	return ip.String(), nil
}

// GetIPV4NetworkMaskBySize gets IPv4 network mask by size
func GetIPV4NetworkMaskBySize(size int) string {
	// Java makes the sign bit sticky on a shift
	shft := 0xffffffff << uint(32-size)

	oct1 := ((shft & 0xff000000) >> 24) & 0xff
	oct2 := (shft & 0x00ff0000) >> 16
	oct3 := (shft & 0x0000ff00) >> 8
	oct4 := shft & 0x000000ff
	return strconv.Itoa(oct1) + "." + strconv.Itoa(oct2) + "." + strconv.Itoa(oct3) + "." + strconv.Itoa(oct4)
}

// setup DiskQuota(or others) for edas, since they won't modify kubelet code,
// it can be removed until DiskQuota move into cri interface.
func setupDiskQuota(createConfig *apitypes.ContainerCreateConfig) {
	for _, e := range createConfig.Env {
		splits := strings.SplitN(e, "=", 2)
		if len(splits) != 2 || splits[0] != envDiskQuota {
			continue
		}
		if createConfig.DiskQuota == nil {
			createConfig.DiskQuota = make(map[string]string)
		}
		createConfig.DiskQuota[".*"] = splits[1]
		createConfig.QuotaID = "-1"
	}
}

// setRootFSWritableLayerHomeDir sets container's rootfs writable layer to the specified path
// if the pod which the container is belong to has annContainerRootFSWritableLayer annotation
func setRootFSWritableLayerHomeDir(createConfig *apitypes.ContainerCreateConfig, annotations map[string]string) error {
	if annotations == nil || annotations[annContainerRootFSWritableLayer] == "" {
		return nil
	}

	homeDir := filepath.Clean(annotations[annContainerRootFSWritableLayer])
	volumeMountPoint := filepath.Dir(homeDir)
	if _, err := os.Stat(volumeMountPoint); err != nil {
		return errors.Wrapf(err, "volume mount point %v status not ready", volumeMountPoint)
	}

	createConfig.Home = homeDir
	return nil
}
