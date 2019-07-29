package containerplugin

import (
	"net"
	"strings"
	"time"

	"github.com/alibaba/pouch/daemon/mgr"
	networktypes "github.com/alibaba/pouch/network/types"

	"github.com/sirupsen/logrus"
)

// PreStart returns an array of priority and args which will pass to runc, the every priority
// used to sort the pre start array that pass to runc, network plugin hook always has priority value 0.
// Prestart copy files to container rootfs
func (c *contPlugin) PreStart(config interface{}) ([]int, [][]string, error) {
	var (
		retPriority  = []int{-100}
		retHookPaths = [][]string{{"/opt/ali-iaas/pouch/bin/prestart_hook"}}
	)

	logrus.Infof("pre start method called")

	container, ok := config.(*mgr.Container)
	if !ok {
		// invoke script at /opt/ali-iaas/pouch/bin/start_hook.sh
		// copy file into the ns, put entrypoint in container. like the function of pouch_container_create.sh in old version
		return retPriority, retHookPaths, nil
	}

	//add pouch environment pouch_container_image and pouch_container_id
	container.Config.Env = addEnvironment(container.Config.Image, container.ID, container.HostConfig.Runtime, container.Config.Env)

	// if copyPodHosts is set, update config and add prestart hook
	if isCopyPodHostsOn(container.Config, container.HostConfig) {
		pri, prestartArgs := updateContainerForPodHosts(container)
		if len(prestartArgs) > 0 {
			retPriority = append(retPriority, pri)
			retHookPaths = append(retHookPaths, prestartArgs)
		}
	}

	// if cpushare preload set, add prestart hook prestart_cpushare_preload
	if isCpusharePreload(container.Config) {
		retPriority = append(retPriority, -101)
		retHookPaths = append(retHookPaths, []string{"/opt/ali-iaas/pouch/bin/prestart_hook_alipay", "CpusharePreload"})
	}

	return retPriority, retHookPaths, nil
}

// PreCreateEndpoint accepts the container id and env of this container, to update the config of container's endpoint.
// 1. pass Overlay parameters to network plugin like alinet
// 2. generate mac address from ip address
// 3. generate priority for the network interface
func (c *contPlugin) PreCreateEndpoint(cid string, env []string, endpoint *networktypes.Endpoint) error {
	genericParam := make(map[string]interface{})
	if getEnv(env, "OverlayNetwork") == optionOn {
		genericParam["OverlayNetwork"] = optionOn
		genericParam["OverlayTunnelId"] = getEnv(env, "OverlayTunnelId")
		genericParam["OverlayGwIp"] = getEnv(env, "OverlayGwIp")
	}

	if getEnv(env, "VpcECS") == optionOn {
		genericParam["VpcECS"] = optionOn
	}

	for _, oneEnv := range env {
		arr := strings.SplitN(oneEnv, "=", 2)
		if len(arr) == 2 && strings.HasPrefix(arr[0], "alinet_") {
			genericParam[arr[0]] = arr[1]
		}
	}

	if ip := getEnv(env, "RequestedIP"); ip != "" {
		if strings.Contains(ip, ",") {
			ip = strings.Split(ip, ",")[0]
		}
		genericParam[macAddress] = GenerateMACFromIP(net.ParseIP(ip))
	}

	endpoint.Priority = int(finalPoint.Unix() - time.Now().Unix())
	endpoint.DisableResolver = true
	endpoint.GenericParams = genericParam

	return nil
}
