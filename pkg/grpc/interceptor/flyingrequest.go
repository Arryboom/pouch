package interceptor

import (
	"context"
	"sync/atomic"

	"google.golang.org/grpc"
)

// RequestCountDecider is a user-provided function for deciding whether count a request is flying.
type RequestCountDecider func(ctx context.Context, fullMethodName string, servingObject interface{}) bool

// FlyingRequestCountInterceptor returns a new unary server interceptors that counts the flying requests.
func FlyingRequestCountInterceptor(decider RequestCountDecider, counter *int32) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		if decider(ctx, info.FullMethod, info.Server) {
			atomic.AddInt32(counter, 1)
			defer atomic.AddInt32(counter, -1)
		}
		resp, err := handler(ctx, req)
		return resp, err
	}
}
