package v1alpha2

import (
	"context"
	"net"
	"path"
	"sync/atomic"
	"time"

	runtime "github.com/alibaba/pouch/cri/apis/v1alpha2"
	"github.com/alibaba/pouch/cri/metrics"
	"github.com/alibaba/pouch/daemon/config"
	"github.com/alibaba/pouch/pkg/grpc/interceptor"
	"github.com/alibaba/pouch/pkg/log"
	"github.com/alibaba/pouch/pkg/netutils"

	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
)

// Service serves the kubelet runtime grpc api which will be consumed by kubelet.
type Service struct {
	config    *config.Config
	criMgr    CriMgr
	server    *grpc.Server
	listener  net.Listener
	flyingReq int32
}

// NewService creates a brand new cri service.
func NewService(cfg *config.Config, criMgr CriMgr) (*Service, error) {
	s := &Service{
		config: cfg,
		criMgr: criMgr,
	}
	s.server = grpc.NewServer(
		grpc.StreamInterceptor(metrics.GRPCMetrics.StreamServerInterceptor()),
		interceptor.WithUnaryServerChain(
			metrics.GRPCMetrics.UnaryServerInterceptor(),
			interceptor.FlyingRequestCountInterceptor(flyingReqCountDecider, &s.flyingReq),
			interceptor.PayloadUnaryServerInterceptor(criLogLevelDecider),
		),
	)

	runtime.RegisterRuntimeServiceServer(s.server, criMgr)
	runtime.RegisterImageServiceServer(s.server, criMgr)
	runtime.RegisterVolumeServiceServer(s.server, criMgr)

	// EnableHandlingTimeHistogram turns on recording of handling time
	// of RPCs. Histogram metrics can be very expensive for Prometheus
	// to retain and query.
	metrics.GRPCMetrics.EnableHandlingTimeHistogram()
	// Initialize all metrics.
	metrics.GRPCMetrics.InitializeMetrics(s.server)

	return s, nil
}

// Start start the grpc server and stream server. It always return non-nil error.
func (s *Service) Start(readyCh chan bool) error {
	errCh := make(chan error)

	l, err := netutils.GetListener(s.config.CriConfig.Listen, nil)
	if err != nil {
		readyCh <- false
		return err
	}
	s.listener = l

	go func() {
		defer log.With(nil).Infof("CRI server exited")
		errCh <- s.server.Serve(l)
	}()

	// If the cri stream server don't share the port with pouchd, launch it.
	if !s.config.CriConfig.StreamServerReusePort {
		go func() {
			defer log.With(nil).Infof("CRI stream server exited")
			errCh <- s.criMgr.StreamServerStart()
		}()
	}

	readyCh <- true

	return <-errCh
}

// Shutdown close the server socket.
func (s *Service) Shutdown() error {
	if err := s.listener.Close(); err != nil {
		log.With(nil).Warningf("CRI server socket close: %v", err)
	}

	if !s.config.CriConfig.StreamServerReusePort {
		if err := s.criMgr.StreamServerShutdown(); err != nil {
			log.With(nil).Warningf("CRI stream server shutdown: %v", err)
		} else {
			log.With(nil).Infof("CRI stream server has shutdown")
		}
	}

	// drain all requests on going
	drain := make(chan struct{})
	go func() {
		for {
			if atomic.LoadInt32(&s.flyingReq) == 0 {
				close(drain)
				return
			}
			time.Sleep(time.Microsecond * 50)
		}
	}()

	select {
	case <-drain:
		log.With(nil).Infof("CRI server has shutdown")
	case <-time.After(60 * time.Second):
		log.With(nil).Errorf("stop CRI server after waited 60 seconds, on going request %d", atomic.LoadInt32(&s.flyingReq))
	}

	return nil
}

func criLogLevelDecider(ctx context.Context, fullMethodName string, servingObject interface{}) logrus.Level {
	// extract methodName from fullMethodName
	// eg. extract 'StartContainer' from '/runtime.v1alpha2.RuntimeService/StartContainer'
	methodName := path.Base(fullMethodName)

	// method->logLevel map
	switch methodName {
	case // readonly methods
		"Version",
		"PodSandboxStatus",
		"ListPodSandbox",
		"ListContainers",
		"ContainerStatus",
		"ContainerStats",
		"ListContainerStats",
		"Status",
		"ListImages",
		"ImageStatus",
		"ImageFsInfo":
		return logrus.DebugLevel
	default:
		return logrus.InfoLevel
	}
}

func flyingReqCountDecider(ctx context.Context, fullMethodName string, servingObject interface{}) bool {
	methodName := path.Base(fullMethodName)

	switch methodName {
	case
		"RunPodSandbox",
		"StartPodSandbox",
		"StopPodSandbox",
		"RemovePodSandbox",
		"CreateContainer",
		"StartContainer",
		"StopContainer",
		"RemoveContainer",
		"PauseContainer",
		"UnpauseContainer":
		return true
	default:
		return false
	}
}
