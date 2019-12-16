package mgr

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"syscall"

	"github.com/alibaba/pouch/apis/types"
	"github.com/alibaba/pouch/pkg/log"
	"github.com/pkg/errors"

	specs "github.com/opencontainers/runtime-spec/specs-go"
)

const (
	// RPrivatePropagationMode represents mount propagation rprivate.
	RPrivatePropagationMode = "rprivate"
	// PrivatePropagationMode represents mount propagation private.
	PrivatePropagationMode = "private"
	// RSharedPropagationMode represents mount propagation rshared.
	RSharedPropagationMode = "rshared"
	// SharedPropagationMode represents mount propagation shared.
	SharedPropagationMode = "shared"
	// RSlavePropagationMode represents mount propagation rslave.
	RSlavePropagationMode = "rslave"
	// SlavePropagationMode represents mount propagation slave.
	SlavePropagationMode = "slave"
)

func clearReadonly(m *specs.Mount) {
	var opts []string
	for _, o := range m.Options {
		if o != "ro" {
			opts = append(opts, o)
		}
	}
	m.Options = opts
}

func overrideDefaultMount(mounts []specs.Mount, c *Container, s *specs.Spec) ([]specs.Mount, error) {
	for _, sm := range s.Mounts {
		dup := false
		for _, cm := range c.Mounts {
			if sm.Destination == cm.Destination {
				dup = true
				break
			}
		}
		if dup {
			continue
		}

		mounts = append(mounts, sm)
	}

	return mounts, nil
}

func mergeContainerMount(mounts []specs.Mount, c *Container, s *specs.Spec) ([]specs.Mount, error) {
	for _, mp := range c.Mounts {
		if trySetupNetworkMount(mp, c) {
			// ignore the network mount, we will handle it later.
			continue
		}

		// check duplicate mountpoint
		for _, sm := range mounts {
			if sm.Destination == mp.Destination {
				return nil, fmt.Errorf("duplicate mount point: %s", mp.Destination)
			}
		}

		pg := mp.Propagation
		rootfspg := s.Linux.RootfsPropagation
		// Set rootfs propagation, default setting is private.
		switch pg {
		case SharedPropagationMode, RSharedPropagationMode:
			if rootfspg != SharedPropagationMode && rootfspg != RSharedPropagationMode {
				s.Linux.RootfsPropagation = SharedPropagationMode
			}
		case SlavePropagationMode, RSlavePropagationMode:
			if rootfspg != SharedPropagationMode && rootfspg != RSharedPropagationMode &&
				rootfspg != SlavePropagationMode && rootfspg != RSlavePropagationMode {
				s.Linux.RootfsPropagation = RSlavePropagationMode
			}
		}

		mountType := "bind"
		opts := []string{}

		// if mount type is set to ext4 ext3 or xfs, set it to spec mount type
		if mp.Type == "ext4" || mp.Type == "ext3" || mp.Type == "xfs" {
			mountType = mp.Type
		} else {
			opts = append(opts, "rbind")
		}

		if !mp.RW {
			opts = append(opts, "ro")
		}

		// set rprivate propagation to bind mount if pg is ""
		if pg == "" {
			pg = RPrivatePropagationMode
		}
		opts = append(opts, pg)

		// TODO: support copy data.

		mounts = append(mounts, specs.Mount{
			Source:      mp.Source,
			Destination: mp.Destination,
			Type:        mountType,
			Options:     opts,
		})
	}

	// if disable hostfiles, we will not mount the hosts files into container.
	if !c.Config.DisableNetworkFiles {
		mounts = append(mounts, generateNetworkMounts(c)...)
	}

	return mounts, nil
}

// setupMounts create mount spec.
func setupMounts(ctx context.Context, c *Container, specWrapper *SpecWrapper) error {
	var (
		mounts []specs.Mount
		err    error
		s      = specWrapper.s
	)

	// Override the default mounts which are duplicate with user defined ones.
	mounts, err = overrideDefaultMount(mounts, c, s)
	if err != nil {
		return errors.Wrap(err, "failed to override default spec mounts")
	}

	if !IsInMount(c, "/dev/shm") {
		// setup container shm mount
		log.With(ctx).Infof("setup %s shm path", c.ID)
		if err := setupIpcShmPath(ctx, c, specWrapper); err != nil {
			return errors.Wrap(err, "failed to setup ipc shm path")
		}

		if c.ShmPath == "" {
			// consider for compatible, if shared ipc container created before shm patch, shm path was empty, should should only occur in --ipc=container: case
			log.With(ctx).Warnf("container %s shm path is empty, it was an old container or a kata container", c.ID)

			shmSize := DefaultSHMSize
			if c.HostConfig.ShmSize != nil && *c.HostConfig.ShmSize != 0 {
				shmSize = *c.HostConfig.ShmSize
			}

			mounts = append(mounts, specs.Mount{
				Source:      "shm",
				Destination: "/dev/shm",
				Type:        "tmpfs",
				Options:     []string{"nosuid", "noexec", "nodev", "mode=1777", "size=" + strconv.FormatInt(shmSize, 10)},
			})
		} else {
			mounts = append(mounts, specs.Mount{
				Source:      c.ShmPath,
				Destination: "/dev/shm",
				Type:        "bind",
				Options:     []string{"rbind", "rprivate"},
			})
		}
	}

	// user defined mount
	mounts, err = mergeContainerMount(mounts, c, s)
	if err != nil {
		return errors.Wrap(err, "failed to merge container mounts")
	}

	// modify share memory size, and change rw mode for privileged mode.
	for i := range mounts {
		if c.HostConfig.Privileged {
			// Clear readonly for /sys.
			if mounts[i].Destination == "/sys" && !s.Root.Readonly {
				clearReadonly(&mounts[i])
			}

			// Clear readonly for cgroup
			if mounts[i].Type == "cgroup" {
				clearReadonly(&mounts[i])
			}
		}
	}

	s.Mounts = sortMounts(mounts)
	return nil
}

// generateNetworkMounts will generate network mounts.
func generateNetworkMounts(c *Container) []specs.Mount {
	mounts := make([]specs.Mount, 0)

	fileBinds := []struct {
		Name   string
		Source string
		Dest   string
	}{
		{"HostnamePath", c.HostnamePath, "/etc/hostname"},
		{"HostsPath", c.HostsPath, "/etc/hosts"},
		{"ResolvConfPath", c.ResolvConfPath, "/etc/resolv.conf"},
	}

	for _, bind := range fileBinds {
		if bind.Source != "" {
			_, err := os.Stat(bind.Source)
			if err != nil {
				log.With(nil).Warnf("%s set to %s, but stat error: %v, skip it", bind.Name, bind.Source, err)
			} else {
				mounts = append(mounts, specs.Mount{
					Source:      bind.Source,
					Destination: bind.Dest,
					Type:        "bind",
					Options:     []string{"rbind", "rprivate"},
				})
			}
		}
	}

	return mounts
}

// trySetupNetworkMount will try to set network mount.
func trySetupNetworkMount(mount *types.MountPoint, c *Container) bool {
	if mount.Destination == "/etc/hostname" {
		c.HostnamePath = mount.Source
		return true
	}

	if mount.Destination == "/etc/hosts" {
		c.HostsPath = mount.Source
		return true
	}

	if mount.Destination == "/etc/resolv.conf" {
		c.ResolvConfPath = mount.Source
		return true
	}

	return false
}

// mounts defines how to sort specs.Mount.
type mounts []specs.Mount

// Len returns the number of mounts.
func (m mounts) Len() int {
	return len(m)
}

// Less returns true if the destination of mount i < destination of mount j
// in lexicographic order.
func (m mounts) Less(i, j int) bool {
	return filepath.Clean(m[i].Destination) < filepath.Clean(m[j].Destination)
}

// Swap swaps two items in an array of mounts.
func (m mounts) Swap(i, j int) {
	m[i], m[j] = m[j], m[i]
}

// sortMounts sorts an array of mounts in lexicographic order. This ensure that
// the mount like /etc/resolv.conf will not mount before /etc, so /etc will
// not shadow /etc/resolv.conf
func sortMounts(m []specs.Mount) []specs.Mount {
	sort.Stable(mounts(m))
	return m
}

// setupIpcShmPath
func setupIpcShmPath(ctx context.Context, c *Container, specWrapper *SpecWrapper) error {

	ipcMode := c.HostConfig.IpcMode

	if isContainer(ipcMode) {
		pc, err := getIpcContainer(ctx, specWrapper.ctrMgr, connectedContainer(ipcMode))
		if err != nil {
			return fmt.Errorf("run a container with --ipc=container, can not find ipc container: %s", err)
		}
		// if container created without this patch, container shm path is empty
		c.ShmPath = pc.ShmPath
	} else if isHost(ipcMode) {
		if _, err := os.Stat("/dev/shm"); err != nil {
			return fmt.Errorf("run a container with --ipc=host, but /dev/shm is not mount in host")
		}
		c.ShmPath = "/dev/shm"
	} else {
		if !IsInMount(c, "/dev/shm") {
			var shmSize int64
			// ShmPath has been set in start container process
			shmPath := c.ShmPath
			if shmPath == "" {
				// this should not be empty
				return nil
			}
			if err := os.MkdirAll(shmPath, 0700); err != nil && !os.IsExist(err) {
				return fmt.Errorf("failed to create shm path for %s: %s", c.ID, err)
			}
			if c.HostConfig.ShmSize != nil {
				shmSize = *c.HostConfig.ShmSize
			}

			if shmSize == 0 {
				shmSize = DefaultSHMSize
			}

			if err := syscall.Mount("shm", shmPath, "tmpfs", uintptr(syscall.MS_NOEXEC|syscall.MS_NOSUID|syscall.MS_NODEV), "mode=1777,size="+strconv.FormatInt(shmSize, 10)); err != nil {
				return fmt.Errorf("failed to mount shm for container %s: %s", c.ID, err)

			}
		}

	}

	return nil
}

// IsInMount check if destPath has been a bind mount in container
func IsInMount(c *Container, destPath string) bool {
	for _, m := range c.Mounts {
		if filepath.Clean(m.Destination) == filepath.Clean(destPath) {
			return true
		}
	}

	return false
}
