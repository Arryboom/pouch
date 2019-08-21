package containerplugin

import (
	"time"
)

const (
	prefix     = "com.docker.network"
	macAddress = prefix + ".endpoint.macaddress"
	// optionOn is the value of an environment variable, means it is on
	optionOn = "true"
	// copyPodHostsLabelKey is the key of an label variable, means the container
	// will copy pod host files to container rootfs before start at first time
	copyPodHostsLabelKey = "pouch.CopyPodHosts"

	// env value: "raw.file:/home/t4;raw.file:/home/"
	blockFileEnvKey     = "pouch_kata_blockfile_host_container_path"
	blockFileTypeEnvKey = "pouch_kata_blockfile_fs_type"
)

var (
	blockFileTypeMap = map[string]struct{}{
		"ext4": {},
		"ext3": {},
		"xfs":  {},
	}
)

var finalPoint, _ = time.Parse("2006-01-02T15:04:05.000Z", "2099-01-01T00:00:00.000Z")
