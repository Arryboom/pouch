package criplugin

import (
	"context"
	"fmt"

	"github.com/alibaba/pouch/apis/types"
	critype "github.com/alibaba/pouch/cri/v1alpha2/types"
	"github.com/alibaba/pouch/hookplugins"
	"github.com/alibaba/pouch/pkg/log"

	"github.com/pkg/errors"
)

type criPlugin struct{}

func init() {
	hookplugins.RegisterCriPlugin(&criPlugin{})
}

// PreCreateContainer defines plugin point where receives a container create request, in this plugin point user
// could the container's config in cri interface.
func (c *criPlugin) PreCreateContainer(ctx context.Context, createConfig *types.ContainerCreateConfig, res interface{}) error {
	sandboxMeta, ok := res.(*critype.SandboxMeta)
	if !ok {
		return fmt.Errorf("invalid object, is not 'SandboxMeta' struct")
	}

	if err := updateNetworkEnv(ctx, createConfig, sandboxMeta); err != nil {
		return errors.Wrapf(err, "failed to update sandbox: (%s) cni network information to container env", sandboxMeta.ID)
	}
	log.With(ctx).Debugf("update network env: (%v)", createConfig.Env)

	// setup DiskQuota(or others) for edas, since they won't modify kubelet code,
	// it can be removed until DiskQuota move into cri interface.
	setupDiskQuota(createConfig)

	if err := setRootFSWritableLayerHomeDir(createConfig, sandboxMeta.Config.GetAnnotations()); err != nil {
		return errors.Wrapf(err, "failed to set rootfs writable layer for container of the sandbox %v", sandboxMeta.ID)
	}

	return nil
}
