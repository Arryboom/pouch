package containerplugin

import (
	"context"
	"fmt"
	"net"
	"os/exec"
	"strconv"
	"strings"

	"github.com/alibaba/pouch/apis/types"
	"github.com/alibaba/pouch/hookplugins"
	"github.com/alibaba/pouch/pkg/log"

	"github.com/docker/docker/daemon/caps"
	"github.com/pkg/errors"
)

var extendCapabilities = []string{
	"SYS_RESOURCE",
	"SYS_MODULE",
	"SYS_PTRACE",
	"SYS_PACCT",
	"NET_ADMIN",
	"SYS_ADMIN",
}

type contPlugin struct{}

func init() {
	hookplugins.RegisterContainerPlugin(&contPlugin{})
}

// PreCreate defines plugin point where receives a container create request, in this plugin point user
// could change the container create body passed-in by http request body
// 1. change NetworkMode to bridge if not set
// 2. create network if network doesn't exist
// 3. generate admin uid if env ali_admin_uid=0 exist
// 4. set user to root if running in rich container mode
// 5. convert label DiskQuota to DiskQuota in ContainerConfig parameter
// 6. in rich container mode, change ali_run_mode=common_vm to ali_run_mode=vm
// 7. in rich container mode, convert 3 labels to env
// 8. in rich container mode, add some capabilities by default
// 9. in rich container mode, don't bind /etc/hosts /etc/hostname /etc/resolv.conf files into container
// 10. in rich container mode, set ShmSize to half of the limit of memory
// 11. generate quota id as needed
// 12. set HOSTNAME env if HostName specified
// 13. if VolumesFrom specified and the container name has a prefix of slash, trim it
// 14. add net-priority into spec-annotations
// 15. add annotations with prefix 'annotation.' into spec-annotations, for edas serverless
// 16. convert label pouch.SupportCgroup to env pouchSupportCgroup. Runc will clear cgroup readonly with this env
func (c *contPlugin) PreCreate(ctx context.Context, createConfig *types.ContainerCreateConfig) error {
	log.With(ctx).Infof("pre create method called")

	if createConfig.HostConfig == nil {
		createConfig.HostConfig = &types.HostConfig{}
	}
	env := createConfig.Env
	requestedIP := getEnv(env, "RequestedIP")
	if createConfig.HostConfig.NetworkMode == "default" || createConfig.HostConfig.NetworkMode == "" {
		createConfig.HostConfig.NetworkMode = "bridge"
	}
	networkMode := createConfig.HostConfig.NetworkMode

	//setup network just in case, skip container/host/none/netns mode network.
	if !strings.HasPrefix(networkMode, "container:") &&
		!strings.HasPrefix(networkMode, "netns:") &&
		networkMode != "host" && networkMode != "none" {
		// set ipv4 into env
		if requestedIP == "" && createConfig.NetworkingConfig != nil &&
			createConfig.NetworkingConfig.EndpointsConfig[networkMode] != nil &&
			createConfig.NetworkingConfig.EndpointsConfig[networkMode].IPAMConfig != nil {
			requestedIP = createConfig.NetworkingConfig.EndpointsConfig[networkMode].IPAMConfig.IPV4Address
			if requestedIP != "" {
				createConfig.Env = append(createConfig.Env, fmt.Sprintf("RequestedIP=%s", requestedIP))
			}
		}

		// check --must-requested-ip in config file: /etc/sysconfig/pouch
		if mustRequestedIP() {
			if len(requestedIP) == 0 {
				return fmt.Errorf("-e RequestedIP not set")
			}
			for _, oneIP := range strings.Split(requestedIP, ",") {
				if net.ParseIP(oneIP) == nil {
					return fmt.Errorf("-e RequestedIP=%s is invalid", requestedIP)
				}
			}
		}

		defaultRoute := getEnv(env, "DefaultRoute")
		mask := getEnv(env, "DefaultMask")
		nic := getEnv(env, "DefaultNic")

		if createConfig.NetworkingConfig == nil {
			createConfig.NetworkingConfig = &types.NetworkingConfig{}
		}
		if createConfig.NetworkingConfig.EndpointsConfig == nil {
			createConfig.NetworkingConfig.EndpointsConfig = make(map[string]*types.EndpointSettings)
		}

		nwName, err := prepareNetwork(ctx, requestedIP, defaultRoute, mask, nic, networkMode,
			createConfig.NetworkingConfig.EndpointsConfig, env)
		if err != nil {
			return err
		} else if nwName != networkMode {
			createConfig.HostConfig.NetworkMode = nwName
		}
	}

	// generate admin uid
	if getEnv(createConfig.Env, "ali_admin_uid") == "0" && requestedIP != "" {
		b, ex := exec.Command("/opt/ali-iaas/pouch/bin/get_admin_uid.sh", requestedIP).CombinedOutput()
		if ex != nil {
			log.With(ctx).Errorf("get admin uid error, ip is %s, error is %v", requestedIP, ex)
			return ex
		}
		uid, ex := strconv.Atoi(strings.TrimSpace(string(b)))
		if ex != nil {
			log.With(ctx).Errorf("get admin uid error, ip is %s, error is %v", requestedIP, ex)
			return ex
		}
		for i, oneEnv := range createConfig.Env {
			arr := strings.SplitN(oneEnv, "=", 2)
			if len(arr) == 2 && arr[0] == "ali_admin_uid" {
				createConfig.Env[i] = fmt.Sprintf("%s=%d", arr[0], uid)
				break
			}
		}
	}

	// common vm must run as root
	mode := getEnv(createConfig.Env, "ali_run_mode")
	if ("common_vm" == mode || "vm" == mode) && createConfig.User != "root" {
		log.With(ctx).Infof("in common_vm mode, use root user to start container.")
		createConfig.User = "root"
	}

	// setup disk quota
	diskQuota := createConfig.Labels["DiskQuota"]
	if diskQuota != "" && len(createConfig.DiskQuota) == 0 {
		if createConfig.DiskQuota == nil {
			createConfig.DiskQuota = make(map[string]string)
		}
		for _, kv := range strings.Split(diskQuota, ";") {
			kv = strings.TrimSpace(kv)
			if kv == "" {
				continue
			}
			arr := strings.SplitN(kv, "=", 2)
			var k, v string
			if len(arr) == 2 {
				k, v = arr[0], arr[1]
			} else {
				k = ".*"
				v = arr[0]
			}
			createConfig.DiskQuota[k] = v
		}
	}

	// -e ali_jvm_cgroup=true allow container get all capabilities
	if getEnv(env, "ali_jvm_cgroup") == optionOn {
		allCaps := caps.GetAllCapabilities()
		createConfig.HostConfig.CapAdd = make([]string, 0, 64)
		for _, v := range allCaps {
			createConfig.HostConfig.CapAdd = append(createConfig.HostConfig.CapAdd, strings.TrimPrefix(v, "CAP_"))
		}
	} else {
		// add extend capabilities for common_vm mode container
		extendCaps := make([]string, 0, len(extendCapabilities))
		for _, cap := range extendCapabilities {
			if !inSlice(createConfig.HostConfig.CapDrop, cap) {
				extendCaps = append(extendCaps, cap)
			}
		}
		createConfig.HostConfig.CapAdd = append(createConfig.HostConfig.CapAdd, extendCaps...)
	}

	createConfig.HostConfig.CapAdd = UniqueStringSlice(createConfig.HostConfig.CapAdd)

	// match label ali.host.dns and com.alipay.acs.container.server_type
	keySet := map[string]struct{}{
		"ali.host.dns":                         {},
		"ali_host_dns":                         {},
		"com_alipay_acs_container_server_type": {},
		"com.alipay.acs.container.server_type": {},
		"ali_call_scm":                         {},
		"ali.call.scm":                         {},
	}

	// convert label to env
	for k, v := range createConfig.Labels {
		lowerKey := strings.ToLower(k)
		if _, ok := keySet[lowerKey]; !ok {
			continue
		}
		createConfig.Env = append(createConfig.Env, fmt.Sprintf("%s=%s", escapseLableToEnvName(k), v))
	}

	if createConfig.Labels["ali.host.dns"] == "true" || createConfig.Labels["com.alipay.acs.container.server_type"] == "DOCKER_VM" {
		// don't bind /etc/hosts /etc/hostname /etc/resolv.conf files into container
		createConfig.DisableNetworkFiles = true
	}

	// common vm use rich container which introduced by pouch
	if getEnv(env, "ali_run_mode") == "vm" || getEnv(env, "ali_run_mode") == "common_vm" {
		// change common_vm to vm
		for i, line := range createConfig.Env {
			if line == "ali_run_mode=common_vm" {
				createConfig.Env[i] = "ali_run_mode=vm"
			}
		}

		//don't bind /etc/hosts /etc/hostname /etc/resolv.conf files into container
		createConfig.DisableNetworkFiles = true

		if (createConfig.HostConfig.ShmSize == nil || *createConfig.HostConfig.ShmSize == 0) &&
			createConfig.HostConfig.Memory > 0 {
			partOfMemSize := createConfig.HostConfig.Memory / 2
			createConfig.HostConfig.ShmSize = &partOfMemSize
		}
	}

	// generate quota id as needed
	labelQuotaID := createConfig.Labels["QuotaId"]
	if !isSetQuotaID(createConfig.QuotaID) {
		if isSetQuotaID(labelQuotaID) {
			createConfig.QuotaID = labelQuotaID
		} else if createConfig.Labels["AutoQuotaId"] == optionOn ||
			(diskQuota != "" &&
				!strings.Contains(diskQuota, ";") &&
				!strings.Contains(diskQuota, "=") &&
				!strings.Contains(diskQuota, "&")) {
			// if set AutoQuotaId=true or the format: DiskQuota=60G,
			// it need set QuotaID=-1
			createConfig.QuotaID = "-1"
		}
	}

	// set hostname to env
	if getEnv(env, "HOSTNAME") == "" && createConfig.Hostname != "" {
		found := false
		for i, line := range createConfig.Env {
			if strings.HasPrefix(line, "HOSTNAME=") {
				createConfig.Env[i] = fmt.Sprintf("HOSTNAME=%s", createConfig.Hostname)
				found = true
				break
			}
		}
		if !found {
			createConfig.Env = append(createConfig.Env, fmt.Sprintf("HOSTNAME=%s", createConfig.Hostname))
		}
	}

	if len(createConfig.HostConfig.VolumesFrom) > 0 {
		for i, one := range createConfig.HostConfig.VolumesFrom {
			createConfig.HostConfig.VolumesFrom[i] = strings.TrimPrefix(one, "/")
		}
	}

	// convert label pouch.SupportCgroup to env pouchSupportCgroup. Runc will clear cgroup readonly with this env
	supportCgroup := createConfig.Labels["pouch.SupportCgroup"]
	if supportCgroup == optionOn {
		createConfig.Env = append(createConfig.Env, "pouchSupportCgroup=true")
	}

	// set snapshotter to support multiple snapshotter
	// now, only for edas, if runtime is runc and snapshotter
	// is qcow2, set snapshotter to overlayfs
	if createConfig.HostConfig.Runtime == "runc" &&
		createConfig.Snapshotter == "qcow2" {
		createConfig.Snapshotter = "overlayfs"
	}

	if createConfig.SpecAnnotation == nil {
		createConfig.SpecAnnotation = make(map[string]string)
	}
	// add annotations with prefix 'annotation.' into spec-annotations,
	// for edas serverless.
	for k, v := range createConfig.Labels {
		if strings.HasPrefix(k, "annotation.") {
			createConfig.SpecAnnotation[strings.TrimPrefix(k, "annotation.")] = v
		}
	}

	if err := validateSpecAnnotation(createConfig); err != nil {
		return errors.Wrap(err, "failed to validate spec annotation")
	}

	// add net-priority into spec-annotations
	// compatible with net-priority passed by --annotation customization.net_priority
	// first use --annotation customization.net_priority, then --net-priority
	if v, exist := createConfig.SpecAnnotation["customization.net_priority"]; exist {
		if _, err := strconv.Atoi(v); err != nil {
			return fmt.Errorf("customization.net_priority should be integer, actual %s", v)
		}
		createConfig.SpecAnnotation["net-priority"] = v
	} else if createConfig.NetPriority != 0 {
		createConfig.SpecAnnotation["net-priority"] = strconv.FormatInt(createConfig.NetPriority, 10)
	}

	return nil
}

func validateSpecAnnotation(createConfig *types.ContainerCreateConfig) error {
	specAnnotation := createConfig.SpecAnnotation
	for key := range specAnnotation {
		if !strings.HasPrefix(key, hookplugins.AnnotationPrefix) {
			continue
		}

		if _, exist := hookplugins.SupportAnnotation[key]; !exist {
			return errors.Errorf("failed to validate spec annotation, invalid key(%s)", key)
		}
	}

	if err := validateCpusetTrick(specAnnotation, createConfig.HostConfig.CpusetCpus); err != nil {
		return errors.Wrap(err, "failed to validate cpuset trick")
	}

	return nil
}

func validateCpusetTrick(specAnnotation map[string]string, cpusetCpus string) error {
	cpusetTrickCpus := specAnnotation[hookplugins.SpecCpusetTrickCpus]
	cpusetTrickTasks := specAnnotation[hookplugins.SpecCpusetTrickTasks]
	cpusetTrickExemptTasks := specAnnotation[hookplugins.SpecCpusetTrickExemptTasks]

	if cpusetTrickCpus != "" && cpusetCpus == "" {
		return errors.Errorf("conflict cpuset.trick_cpus is %s with cpuset.cpus is nil",
			cpusetTrickCpus)
	}

	if cpusetTrickCpus != "" && (cpusetTrickTasks == "" && cpusetTrickExemptTasks == "") {
		return errors.Errorf("conflict cpuset.trick_cpus is %s with"+
			" CpusetTrickTasks and CpusetTrickexemptTasks are nil", cpusetTrickCpus)
	}

	if cpusetTrickCpus == "" && (cpusetTrickTasks != "" || cpusetTrickExemptTasks != "") {
		return errors.Errorf("conflict cpuset.trick_cpus is nil, "+
			"but CpusetTrickTasks is %s or CpusetTrickexemptTasks is %s",
			cpusetTrickTasks, cpusetTrickExemptTasks)
	}

	return nil
}

func inSlice(slice []string, s string) bool {
	for _, ss := range slice {
		if strings.ToLower(s) == strings.ToLower(ss) {
			return true
		}
	}
	return false
}

func isSetQuotaID(id string) bool {
	if id == "" || id == "0" {
		return false
	}
	return true
}
