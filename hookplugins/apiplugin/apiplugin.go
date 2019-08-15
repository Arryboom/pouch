package apiplugin

import (
	"context"
	"net/http"

	"github.com/alibaba/pouch/apis/server/types"
	"github.com/alibaba/pouch/hookplugins"
)

type apiPlugin struct{}

func init() {
	hookplugins.RegisterAPIPlugin(&apiPlugin{})
}

func (a *apiPlugin) UpdateHandler(ctx context.Context, handlers []*types.HandlerSpec) []*types.HandlerSpec {
	i := 0
	for i < len(handlers) {
		if removed := updateHandlerSpec(handlers[i]); removed {
			handlers = append(handlers[:i], handlers[i+1:]...)
		} else {
			i++
		}
	}

	extraHandlers := []*types.HandlerSpec{
		// host
		{Method: http.MethodGet, Path: "/host/exec/result", HandlerFunc: HostExecResultHandler},
		{Method: http.MethodPost, Path: "/host/exec", HandlerFunc: HostExecHandler},

		// network
		{Method: http.MethodPost, Path: "/networks/extend", HandlerFunc: NetworkExtendHandler},
	}

	handlers = append(handlers, extraHandlers...)

	return handlers
}

// updateHandlerSpec update the handler or just remove it.
func updateHandlerSpec(spec *types.HandlerSpec) (removed bool) {
	if spec == nil {
		return true
	}

	if isContainerRequest(spec.Path) {
		spec.HandlerFunc = containerRequestWrapper(spec.HandlerFunc)
	}

	switch spec.Path {
	case "/version":
		spec.HandlerFunc = getVersionHandler(spec.HandlerFunc)
	case "/containers/create":
		spec.HandlerFunc = containerCreateWrapper(spec.HandlerFunc)
	case "/containers/{name:.*}/json":
		spec.HandlerFunc = containerInspectWrapper(spec.HandlerFunc)
	case "/containers/{name:.*}/update":
		spec.HandlerFunc = containerUpdateWrapper(spec.HandlerFunc)
	}

	return false
}
