package imageplugin

import (
	"context"

	"github.com/alibaba/pouch/hookplugins"
	"github.com/alibaba/pouch/pkg/log"

	"github.com/containerd/containerd"
)

type imagePlugin struct{}

func init() {
	hookplugins.RegisterImagePlugin(&imagePlugin{})
}

// PostPull is called after pull image
func (i *imagePlugin) PostPull(ctx context.Context, snapshotter string, image containerd.Image) error {
	// XXX: deal with qcow2 snapshotter, since qcow2 only suit for
	// kata, but edas need image like pause to run runc/kata container.
	if snapshotter != "qcow2" {
		return nil
	}

	// unpack overlayfs in a goroutine, or time use will be more than twice
	go func() {
		// use a new context, or ctx will be canceled after image pull return
		ctx = context.TODO()
		if err := image.Unpack(ctx, "overlayfs"); err != nil {
			log.With(ctx).Errorf("in post pull hook, failed to unpack image %s to overlayfs: %s", image.Name(), err)
		} else {
			log.With(ctx).Infof("in post pull hook, unpack image %s to overlayfs successful", image.Name())
		}
	}()

	return nil
}
