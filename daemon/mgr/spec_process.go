package mgr

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"strings"
	"time"

	"github.com/alibaba/pouch/apis/types"
	"github.com/alibaba/pouch/pkg/log"
	"github.com/alibaba/pouch/pkg/user"

	"github.com/containerd/containerd/mount"
	"github.com/docker/docker/daemon/caps"
	specs "github.com/opencontainers/runtime-spec/specs-go"
)

// setupProcess setups spec process.
func setupProcess(ctx context.Context, c *Container, s *specs.Spec) error {
	if s.Process == nil {
		s.Process = &specs.Process{}
	}
	config := c.Config

	cwd := config.WorkingDir
	if cwd == "" {
		cwd = "/"
	}

	s.Process.Args = append(config.Entrypoint, config.Cmd...)
	s.Process.Env = append(s.Process.Env, createEnvironment(c)...)
	s.Process.Cwd = cwd
	s.Process.Terminal = config.Tty

	if s.Process.Terminal {
		s.Process.Env = append(s.Process.Env, "TERM=xterm")
	}

	if !c.HostConfig.Privileged {
		s.Process.SelinuxLabel = c.ProcessLabel
		s.Process.NoNewPrivileges = c.NoNewPrivileges
	}

	if err := setupUser(ctx, c, s); err != nil {
		return err
	}

	if c.HostConfig.OomScoreAdj != 0 {
		v := int(c.HostConfig.OomScoreAdj)
		s.Process.OOMScoreAdj = &v
	}

	if err := setupCapabilities(ctx, c.HostConfig, s); err != nil {
		return err
	}

	if err := setupRlimits(ctx, c.HostConfig, s); err != nil {
		return err
	}

	if err := setupAppArmor(ctx, c, s); err != nil {
		return err
	}

	return setupNvidiaEnv(ctx, c, s)
}

func createEnvironment(c *Container) []string {
	env := c.Config.Env
	env = append(env, richContainerModeEnv(c)...)

	return env
}

func setupUser(ctx context.Context, c *Container, s *specs.Spec) (err error) {
	// if use block graphdriver, can not find file in host, need to mount block
	// to a target
	passwdPath := c.GetSpecificBasePath("", user.PasswdFile)
	groupPath := c.GetSpecificBasePath("", user.GroupFile)

	tmpMount := func(target string) error {
		if target == "" {
			return fmt.Errorf("mount target can not be empty")
		}
		if len(c.SnapshotMounts) == 0 {
			return fmt.Errorf("container snapshot mount can not be empty")
		}
		err = os.MkdirAll(target, 0755)
		if err != nil && !os.IsExist(err) {
			return err
		}
		for _, m := range c.SnapshotMounts {
			if err := m.Mount(target); err != nil {
				os.RemoveAll(target)
				return err
			}
		}
		return nil
	}

	tmpUmount := func(target string) {
		for i := 0; i < 10; i++ {
			if err := mount.Unmount(target, 0); err != nil {
				log.With(nil).Warnf("failed to umount mountfs(%s) in %d times: %s", target, i+1, err)
			}
			time.Sleep(50 * time.Millisecond)
		}

		if err := os.RemoveAll(target); err != nil {
			log.With(nil).Warnf("failed to remove target %s: %s", target, err)
		}
	}

	if passwdPath == "" || groupPath == "" {
		target, _ := ioutil.TempDir("", "pouch-user")
		if em := tmpMount(target); em == nil {
			defer tmpUmount(target)
			passwdPath = c.GetSpecificBasePath(target, user.PasswdFile)
			groupPath = c.GetSpecificBasePath(target, user.GroupFile)
			log.With(nil).Infof("graphdriver is block, mount to (%s) get image content", target)
		}
	}

	uid, gid, additionalGids, err := user.Get(passwdPath, groupPath, c.Config.User, c.HostConfig.GroupAdd)
	if err != nil {
		return err
	}

	s.Process.User = specs.User{
		UID:            uid,
		GID:            gid,
		AdditionalGids: additionalGids,
	}
	return nil
}

func setupCapabilities(ctx context.Context, hostConfig *types.HostConfig, s *specs.Spec) error {
	var caplist []string
	var err error

	if s.Process.Capabilities == nil {
		s.Process.Capabilities = &specs.LinuxCapabilities{}
	}
	capabilities := s.Process.Capabilities

	if hostConfig.Privileged {
		caplist = caps.GetAllCapabilities()
	} else if caplist, err = caps.TweakCapabilities(capabilities.Effective, hostConfig.CapAdd, hostConfig.CapDrop); err != nil {
		return err
	}
	capabilities.Effective = caplist
	capabilities.Bounding = caplist
	capabilities.Permitted = caplist
	capabilities.Inheritable = caplist

	s.Process.Capabilities = capabilities
	return nil
}

func setupRlimits(ctx context.Context, hostConfig *types.HostConfig, s *specs.Spec) error {
	var rlimits []specs.POSIXRlimit
	for _, ul := range hostConfig.Ulimits {
		rlimits = append(rlimits, specs.POSIXRlimit{
			Type: "RLIMIT_" + strings.ToUpper(ul.Name),
			Hard: uint64(ul.Hard),
			Soft: uint64(ul.Soft),
		})
	}

	s.Process.Rlimits = rlimits
	return nil
}

func setupNvidiaEnv(ctx context.Context, c *Container, s *specs.Spec) error {
	n := c.HostConfig.NvidiaConfig
	if n == nil {
		return nil
	}
	s.Process.Env = append(s.Process.Env, fmt.Sprintf("NVIDIA_DRIVER_CAPABILITIES=%s", n.NvidiaDriverCapabilities))
	s.Process.Env = append(s.Process.Env, fmt.Sprintf("NVIDIA_VISIBLE_DEVICES=%s", n.NvidiaVisibleDevices))
	return nil
}
