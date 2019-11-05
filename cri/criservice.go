package cri

import (
	"fmt"

	"github.com/alibaba/pouch/cri/stream"
	criv1alpha2 "github.com/alibaba/pouch/cri/v1alpha2"
	"github.com/alibaba/pouch/daemon/config"
	"github.com/alibaba/pouch/daemon/mgr"
	"github.com/alibaba/pouch/hookplugins"
)

type CRIService interface {
	// Start always return non-nil error.
	Start(readyCh chan bool) error
	// Shutdown close the server socket.
	Shutdown() error
}

// RunCriService start cri service if pouchd is specified with --enable-cri.
// if stream.Router is not nil, pouch server should register this router.
func NewCriService(daemonconfig *config.Config, containerMgr mgr.ContainerMgr, imageMgr mgr.ImageMgr, volumeMgr mgr.VolumeMgr, criPlugin hookplugins.CriPlugin) (stream.Router, CRIService, error) {
	if !daemonconfig.IsCriEnabled {
		return nil, nil, nil
	}
	switch daemonconfig.CriConfig.CriVersion {
	case "v1alpha2":
		return newCRIServiceV1alpha2(daemonconfig, containerMgr, imageMgr, volumeMgr, criPlugin)
	default:
		return nil, nil, fmt.Errorf("invalid CRI version %s, expected to be v1alpha2", daemonconfig.CriConfig.CriVersion)
	}
}

// create CRI service with CRI version: v1alpha2
func newCRIServiceV1alpha2(daemonConfig *config.Config, containerMgr mgr.ContainerMgr, imageMgr mgr.ImageMgr, volumeMgr mgr.VolumeMgr, criPlugin hookplugins.CriPlugin) (stream.Router, CRIService, error) {
	criMgr, err := criv1alpha2.NewCriManager(daemonConfig, containerMgr, imageMgr, volumeMgr, criPlugin)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get CriManager with error: %v", err)
	}

	service, err := criv1alpha2.NewService(daemonConfig, criMgr)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to start CRI service with error: %v", err)
	}

	var streamRouter stream.Router
	// If the cri stream server share the port with pouchd,
	// export this router for pouch server to register it.
	if daemonConfig.CriConfig.StreamServerReusePort {
		streamRouter = criMgr.StreamRouter()
	}

	return streamRouter, service, nil
}
