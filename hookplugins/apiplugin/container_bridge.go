package apiplugin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"reflect"
	"runtime"
	"strconv"
	"strings"

	"github.com/alibaba/pouch/apis/opts"
	"github.com/alibaba/pouch/apis/server"
	serverTypes "github.com/alibaba/pouch/apis/server/types"
	"github.com/alibaba/pouch/apis/types"
	hp "github.com/alibaba/pouch/hookplugins"
	"github.com/alibaba/pouch/pkg/httputils"
	"github.com/alibaba/pouch/pkg/kernel"
	"github.com/alibaba/pouch/pkg/log"
	"github.com/alibaba/pouch/pkg/utils"
	"github.com/alibaba/pouch/version"

	"github.com/gorilla/mux"
	"github.com/pkg/errors"
)

func getVersionHandler(_ serverTypes.Handler) serverTypes.Handler {
	return func(ctx context.Context, w http.ResponseWriter, req *http.Request) (err error) {
		kernelVersion := "<unknown>"
		if kv, err := kernel.GetKernelVersion(); err != nil {
			log.With(ctx).Warnf("Could not get kernel version: %v", err)
		} else {
			kernelVersion = kv.String()
		}

		v := types.SystemVersion{
			APIVersion:    version.APIVersion,
			Arch:          runtime.GOARCH,
			BuildTime:     version.BuildTime,
			GitCommit:     version.GitCommit,
			GoVersion:     runtime.Version(),
			KernelVersion: kernelVersion,
			Os:            runtime.GOOS,
			Version:       version.Version,
		}

		if utils.IsStale(ctx, req) {
			v.Version = "1.12.6"
		}

		return server.EncodeResponse(w, http.StatusOK, v)
	}
}

func containerRequestWrapper(h serverTypes.Handler) serverTypes.Handler {
	return func(ctx context.Context, rw http.ResponseWriter, req *http.Request) error {
		if utils.IsStale(ctx, req) {
			//trim the heading slash in parameter name
			nameInPath := mux.Vars(req)["name"]
			if strings.HasPrefix(nameInPath, "/") {
				nameInPath = strings.TrimPrefix(nameInPath, "/")
				mux.Vars(req)["name"] = nameInPath
			}

			nameInForm := req.FormValue("name")
			if strings.HasPrefix(nameInForm, "/") {
				nameInForm = strings.TrimPrefix(nameInForm, "/")
				req.Form.Set("name", nameInForm)
			}
		}

		return h(ctx, rw, req)
	}
}

// ResourcesWrapper contains container's resources for alidocker-1.12.6 (cgroups config, ulimits...)
type ResourcesWrapper struct {
	// Applicable to UNIX platforms

	// cpuset.trick_cpus: 2-4,7-9
	CpusetTrickCpus string
	// cpuset.trick_tasks: java,nginx
	CpusetTrickTasks string
	// cpuset.trick_exempt_tasks: top
	CpusetTrickExemptTasks string
	// support cpu.bvt_warp_ns cgroup
	CPUBvtWarpNs int64
	// enable scheduler latency count in cpuacct
	ScheLatSwitch int64

	// An integer value representing this container's memory
	// low water mark percentage. The value of memory low water mark is memory.
	// limit_in_bytes * MemoryWmarkRatio
	MemoryWmarkRatio int
	// An integer value representing this container's memory high water mark percentage
	MemoryExtra int64
	// MemoryForceEmptyCtl represents whether to reclaim page cache
	// when deleting the cgroup of container.
	MemoryForceEmptyCtl int
	// [0-12], priority of OOM, lower priority would be OOM kill first
	MemoryPriority *int
	// 0 or 1, 1 represents use Priority OOM
	MemoryUsePriorityOOM *int
	// Wether to kill all process of container's cgroup
	MemoryKillAll *int
	// Enable gc cold memory, 0 means disable, 1 means enable
	MemoryDroppable int

	// set intel rdt l3 cbm
	IntelRdtL3Cbm string
	// set intel rdt group
	IntelRdtGroup string
	// set intel rdt mba
	IntelRdtMba string

	// enable file level limit
	BlkFileLevelSwitch int
	// limit write buffer io, bytes per sec
	BlkBufferWriteBps int
	// limit write metadate
	BlkMetaWriteTps int
	// limit file level io throttle
	BlkFileThrottlePath []string
	// limit write buffer io switch
	BlkBufferWriteSwitch int
	// limit write buffer io for device, bytes per second
	BlkDeviceBufferWriteBps []*types.ThrottleDevice
	// device idle time, not support parition
	BlkDeviceIdleTime []*types.ThrottleDevice
	// allowed latency increment, not support parition
	BlkDeviceLatencyTarget []*types.ThrottleDevice

	// IO read rate low limit per cgroup per device, bytes per second
	BlkioDeviceReadLowBps []*types.ThrottleDevice
	// IO read rate low limit per cgroup per device, IO per second
	BlkioDeviceReadLowIOps []*types.ThrottleDevice
	// IO write rate low limit per cgroup per device, bytes per second
	BlkioDeviceWriteLowBps []*types.ThrottleDevice
	// IO write rate low limit per cgroup per device, IO per second
	BlkioDeviceWriteLowIOps []*types.ThrottleDevice

	// set net cgroup rate
	NetCgroupRate string
	// set net cgroup ceil
	NetCgroupCeil string
}

// HostConfigWrapper defines the alidocker's HostConfig
type HostConfigWrapper struct {
	ResourcesWrapper
}

// ContainerCreateConfigWrapper defines the alidocker's ContainerCreateConfig
type ContainerCreateConfigWrapper struct {
	// host config
	HostConfig *HostConfigWrapper `json:"HostConfig,omitempty"`
}

func containerCreateWrapper(h serverTypes.Handler) serverTypes.Handler {
	return func(ctx context.Context, rw http.ResponseWriter, req *http.Request) error {
		log.With(ctx).Infof("container create wrapper method called")

		buffer, _ := ioutil.ReadAll(req.Body)
		log.With(ctx).Infof("create container with body %s", string(buffer))

		// decode container config by alidocker-1.12.6 struct
		configWrapper := &ContainerCreateConfigWrapper{}
		if err := json.NewDecoder(bytes.NewReader(buffer)).Decode(configWrapper); err != nil {
			return httputils.NewHTTPError(err, http.StatusBadRequest)
		}

		if configWrapper.HostConfig == nil {
			req.Body = ioutil.NopCloser(bytes.NewBuffer(buffer))
			return h(ctx, rw, req)
		}

		// decode container config by pouch struct
		config := &types.ContainerCreateConfig{}
		if err := json.NewDecoder(bytes.NewReader(buffer)).Decode(config); err != nil {
			return httputils.NewHTTPError(err, http.StatusBadRequest)
		}

		if config.ContainerConfig.SpecAnnotation == nil {
			config.ContainerConfig.SpecAnnotation = make(map[string]string)
		}
		specAnnotation := config.ContainerConfig.SpecAnnotation
		if specAnnotation == nil {
			specAnnotation = make(map[string]string)
		}

		resourceWrapper := configWrapper.HostConfig.ResourcesWrapper

		convertResourceWrapToAnnotation(&resourceWrapper, specAnnotation)

		config.ContainerConfig.SpecAnnotation = specAnnotation

		// marshal it as stream and return to the caller
		var out bytes.Buffer
		if err := json.NewEncoder(&out).Encode(config); err != nil {
			return err
		}
		log.With(ctx).Infof("after process create container body is %s", string(out.Bytes()))

		req.Body = ioutil.NopCloser(&out)

		return h(ctx, rw, req)
	}
}

func convertResourceWrapToAnnotation(resourceWrapper *ResourcesWrapper, specAnnotation map[string]string) {
	// set cpu cgroup, cpu/cpuset.trick_cpus, cpu/cpu.bvt_warp_ns, cpuacct.sche_lat_switch
	if resourceWrapper.CpusetTrickCpus != "" {
		specAnnotation[hp.SpecCpusetTrickCpus] = resourceWrapper.CpusetTrickCpus
	}
	if resourceWrapper.CpusetTrickTasks != "" {
		specAnnotation[hp.SpecCpusetTrickTasks] = resourceWrapper.CpusetTrickTasks
	}
	if resourceWrapper.CpusetTrickExemptTasks != "" {
		specAnnotation[hp.SpecCpusetTrickExemptTasks] = resourceWrapper.CpusetTrickExemptTasks
	}
	if resourceWrapper.CPUBvtWarpNs != 0 {
		specAnnotation[hp.SpecCPUBvtWarpNs] = strconv.FormatInt(resourceWrapper.CPUBvtWarpNs, 10)
	}
	if resourceWrapper.ScheLatSwitch != 0 {
		specAnnotation[hp.SpecCpuacctSchedLatSwitch] = strconv.FormatInt(resourceWrapper.ScheLatSwitch, 10)
	}

	// set memory cgroup
	if resourceWrapper.MemoryWmarkRatio != 0 {
		specAnnotation[hp.SpecMemoryWmarkRatio] = strconv.Itoa(resourceWrapper.MemoryWmarkRatio)
	}
	if resourceWrapper.MemoryExtra != 0 {
		specAnnotation[hp.SpecMemoryExtraInBytes] = strconv.FormatInt(resourceWrapper.MemoryExtra, 10)
	}
	if resourceWrapper.MemoryForceEmptyCtl != 0 {
		specAnnotation[hp.SpecMemoryForceEmptyCtl] = strconv.Itoa(resourceWrapper.MemoryForceEmptyCtl)
	}
	if resourceWrapper.MemoryPriority != nil && *resourceWrapper.MemoryPriority != 0 {
		specAnnotation[hp.SpecMemoryPriority] = strconv.Itoa(*resourceWrapper.MemoryPriority)
	}
	if resourceWrapper.MemoryUsePriorityOOM != nil && *resourceWrapper.MemoryUsePriorityOOM != 0 {
		specAnnotation[hp.SpecMemoryUsePriorityOOM] = strconv.Itoa(*resourceWrapper.MemoryUsePriorityOOM)
	}
	if resourceWrapper.MemoryKillAll != nil && *resourceWrapper.MemoryKillAll != 0 {
		specAnnotation[hp.SpecMemoryOOMKillAll] = strconv.Itoa(*resourceWrapper.MemoryKillAll)
	}
	if resourceWrapper.MemoryDroppable != 0 {
		specAnnotation[hp.SpecMemoryDroppable] = strconv.Itoa(resourceWrapper.MemoryDroppable)
	}

	// set intel rdt
	if resourceWrapper.IntelRdtL3Cbm != "" {
		specAnnotation[hp.SpecIntelRdtL3Cbm] = resourceWrapper.IntelRdtL3Cbm
	}
	if resourceWrapper.IntelRdtGroup != "" {
		specAnnotation[hp.SpecIntelRdtGroup] = resourceWrapper.IntelRdtGroup
	}
	if resourceWrapper.IntelRdtMba != "" {
		specAnnotation[hp.SpecIntelRdtMba] = resourceWrapper.IntelRdtMba
	}

	// set blkio cgroup
	if resourceWrapper.BlkFileLevelSwitch != 0 {
		specAnnotation[hp.SpecBlkioFileLevelSwitch] = strconv.Itoa(resourceWrapper.BlkFileLevelSwitch)
	}
	if resourceWrapper.BlkBufferWriteBps != 0 {
		specAnnotation[hp.SpecBlkioBufferWriteBps] = strconv.Itoa(resourceWrapper.BlkBufferWriteBps)
	}
	if resourceWrapper.BlkMetaWriteTps != 0 {
		specAnnotation[hp.SpecBlkioMetaWriteTps] = strconv.Itoa(resourceWrapper.BlkMetaWriteTps)
	}
	if len(resourceWrapper.BlkFileThrottlePath) != 0 {
		specAnnotation[hp.SpecBlkioFileThrottlePath] = strings.Join(resourceWrapper.BlkFileThrottlePath, " ")
	}
	if resourceWrapper.BlkBufferWriteSwitch != 0 {
		specAnnotation[hp.SpecBlkioBufferWriteSwitch] = strconv.Itoa(resourceWrapper.BlkBufferWriteSwitch)
	}
	if len(resourceWrapper.BlkDeviceBufferWriteBps) != 0 {
		specAnnotation[hp.SpecBlkioDeviceBufferWriteBps] = sliceThrottleDeviceString(resourceWrapper.BlkDeviceBufferWriteBps)
	}
	if len(resourceWrapper.BlkDeviceIdleTime) != 0 {
		specAnnotation[hp.SpecBlkioDeviceIdleTime] = sliceThrottleDeviceString(resourceWrapper.BlkDeviceIdleTime)
	}
	if len(resourceWrapper.BlkDeviceLatencyTarget) != 0 {
		specAnnotation[hp.SpecBlkioDeviceLatencyTarget] = sliceThrottleDeviceString(resourceWrapper.BlkDeviceLatencyTarget)
	}
	if len(resourceWrapper.BlkioDeviceReadLowBps) != 0 {
		specAnnotation[hp.SpecBlkioDeviceReadLowBps] = sliceThrottleDeviceString(resourceWrapper.BlkioDeviceReadLowBps)
	}
	if len(resourceWrapper.BlkioDeviceReadLowIOps) != 0 {
		specAnnotation[hp.SpecBlkioDeviceReadLowIOps] = sliceThrottleDeviceString(resourceWrapper.BlkioDeviceReadLowIOps)
	}
	if len(resourceWrapper.BlkioDeviceWriteLowBps) != 0 {
		specAnnotation[hp.SpecBlkioDeviceWriteLowBps] = sliceThrottleDeviceString(resourceWrapper.BlkioDeviceWriteLowBps)
	}
	if len(resourceWrapper.BlkioDeviceWriteLowIOps) != 0 {
		specAnnotation[hp.SpecBlkioDeviceWriteLowIOps] = sliceThrottleDeviceString(resourceWrapper.BlkioDeviceWriteLowIOps)
	}

	// set net cgroup
	if resourceWrapper.NetCgroupRate != "" {
		specAnnotation[hp.SpecNetCgroupRate] = resourceWrapper.NetCgroupRate
	}
	if resourceWrapper.NetCgroupCeil != "" {
		specAnnotation[hp.SpecNetCgroupCeil] = resourceWrapper.NetCgroupCeil
	}
}

func containerInspectWrapper(h serverTypes.Handler) serverTypes.Handler {
	return func(ctx context.Context, rw http.ResponseWriter, req *http.Request) error {
		if !utils.IsStale(ctx, req) {
			return h(ctx, rw, req)
		}

		wrapRW := NewWrapResponseWriter()
		err := h(ctx, wrapRW, req)
		if err != nil {
			return err
		}

		data, err := ioutil.ReadAll(wrapRW)
		if err != nil {
			return httputils.NewHTTPError(fmt.Errorf("failed to decode response body: %v", err), http.StatusInternalServerError)
		}

		resp := &InnerContainerJSON{}
		err = json.Unmarshal(data, resp)
		if err != nil {
			return httputils.NewHTTPError(fmt.Errorf("failed to decode response body: %v", err), http.StatusInternalServerError)
		}

		err = convertAnnotationToDockerHostConfig(resp.Config.SpecAnnotation, &resp.HostConfig.InnerResources.ResourcesWrapper)
		if err != nil {
			return httputils.NewHTTPError(fmt.Errorf("failed to convert annotation to docker host config: %v", err), http.StatusInternalServerError)
		}

		return server.EncodeResponse(rw, http.StatusOK, resp)
	}
}

func convertAnnotationToDockerHostConfig(specAnnotation map[string]string, resource *ResourcesWrapper) error {
	if len(specAnnotation) == 0 {
		return nil
	}

	reflectMap := resourceWrapReflectMap()

	rElem := reflect.ValueOf(resource).Elem()

	for annotationKey, data := range specAnnotation {
		// get covertInfo by annotation key
		info, exist := reflectMap[annotationKey]
		if !exist {
			continue
		}

		// convert string to target value by covertFunc
		value, err := info.convertFunc(data)
		if err != nil {
			return fmt.Errorf("failed to convert annotation %s to hostconfig field %s: %v", annotationKey, info.fieldName, err)
		}

		// set value to reflect value
		i := reflect.ValueOf(value)
		if !i.IsValid() {
			return fmt.Errorf("failed to reflect value %v, while field is %s and annotation is %s", value, info.fieldName, data)
		}

		// get struct field by field name
		field := rElem.FieldByName(info.fieldName)
		if !field.IsValid() {
			return fmt.Errorf("field %s is not defined", info.fieldName)
		}

		// set value i to struct field
		field.Set(i)
	}

	return nil
}

// UpdateConfigWrapper defines the alidocker's UpdateConfig
type UpdateConfigWrapper struct {
	InnerResources

	RestartPolicy  types.RestartPolicy
	ImageID        string
	Env            []string
	Label          []string
	DiskQuota      string
	Network        string
	SpecAnnotation map[string]string `json:"SpecAnnotation,omitempty"`
}

func containerUpdateWrapper(h serverTypes.Handler) serverTypes.Handler {
	return func(ctx context.Context, rw http.ResponseWriter, req *http.Request) error {
		if !utils.IsStale(ctx, req) {
			return h(ctx, rw, req)
		}

		log.With(ctx).Infof("container update wrapper method called")

		buffer, err := ioutil.ReadAll(req.Body)
		if err != nil {
			return err
		}
		log.With(ctx).Infof("update container with body %s", string(buffer))

		// decode container config by alidocker-1.12.6 struct
		updateConfigWrap := &UpdateConfigWrapper{}
		if err := json.NewDecoder(bytes.NewReader(buffer)).Decode(updateConfigWrap); err != nil {
			// if can't decode by UpdateConfigWrapper,
			// we just pass it into pouch daemon to deal with it.
			log.With(ctx).Warn("failed to decode update body, pass it into pouch daemon")
			req.Body = ioutil.NopCloser(bytes.NewReader(buffer))
			return h(ctx, rw, req)
		}

		var diskQuota map[string]string
		if updateConfigWrap.DiskQuota != "" {
			// Notes: compatible with alidocker, if DiskQuota is not empty,
			// should also update the DiskQuota Label
			labelDiskQuota := fmt.Sprintf("DiskQuota=%s", updateConfigWrap.DiskQuota)
			if !utils.StringInSlice(updateConfigWrap.Label, labelDiskQuota) {
				updateConfigWrap.Label = append(updateConfigWrap.Label, labelDiskQuota)
			}

			diskQuota, err = opts.ParseDiskQuota(strings.Split(updateConfigWrap.DiskQuota, ";"))
			if err != nil {
				log.With(ctx).Errorf("failed to parse update disk quota: %s, err: %v", updateConfigWrap.DiskQuota, err)
				return errors.Wrapf(err, "failed to parse update disk quota(%s)", updateConfigWrap.DiskQuota)
			}
		}

		var resource types.Resources
		data, err := json.Marshal(updateConfigWrap.InnerResources)
		if err != nil {
			return errors.Wrapf(err, "failed to marshal json with InnerResources, [%v]", updateConfigWrap.InnerResources)
		}
		err = json.Unmarshal(data, &resource)
		if err != nil {
			return errors.Wrapf(err, "failed to unmarshal json with Resources")
		}

		updateConfig := types.UpdateConfig{
			Resources:      resource,
			DiskQuota:      diskQuota,
			Env:            updateConfigWrap.Env,
			Label:          updateConfigWrap.Label,
			RestartPolicy:  &updateConfigWrap.RestartPolicy,
			SpecAnnotation: updateConfigWrap.SpecAnnotation,
		}

		specAnnotation := updateConfig.SpecAnnotation
		if specAnnotation == nil {
			specAnnotation = make(map[string]string)
		}

		convertResourceWrapToAnnotation(&updateConfigWrap.ResourcesWrapper, specAnnotation)

		updateConfig.SpecAnnotation = specAnnotation

		// marshal it as stream and return to the caller
		var out bytes.Buffer
		err = json.NewEncoder(&out).Encode(updateConfig)
		if err != nil {
			return errors.Wrapf(err, "failed to encode json(%v)", updateConfig)
		}

		log.With(ctx).Infof("after process update container body is %s", string(out.Bytes()))

		req.Body = ioutil.NopCloser(&out)

		return h(ctx, rw, req)
	}
}
