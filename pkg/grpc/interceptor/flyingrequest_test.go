package interceptor

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	context2 "golang.org/x/net/context"
	"google.golang.org/grpc"
)

func TestFlyingRequestCountInterceptor(t *testing.T) {
	tests := []struct {
		name        string
		shouldCount bool
		counter     int32
	}{
		{
			name:        "testingRequestShouldCount",
			shouldCount: true,
			counter:     0,
		},
		{
			name:        "testingRequestShouldNotCount",
			shouldCount: false,
			counter:     0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testFlyingRequest(tt.shouldCount, &tt.counter, t)
		})
	}
}

func testFlyingRequest(shouldCount bool, counter *int32, t *testing.T) {
	var (
		serviceName  = "Foo.UnaryMethod"
		theUnaryInfo = &grpc.UnaryServerInfo{FullMethod: serviceName}

		input  = "input"
		result = "result"
	)

	var decider RequestCountDecider = func(ctx context.Context, fullMethodName string, servingObject interface{}) bool {
		return shouldCount
	}

	var (
		continueCh = make(chan struct{})
		runningCh  = make(chan struct{})
		doneCh     = make(chan struct{})
	)

	var handler grpc.UnaryHandler = func(ctx context2.Context, req interface{}) (i interface{}, e error) {
		close(continueCh)
		<-runningCh
		return result, nil
	}

	go func() {
		output, err := chainUnaryServer(FlyingRequestCountInterceptor(decider, counter))(context.TODO(), input, theUnaryInfo, handler)
		close(doneCh)
		assert.Equal(t, err, nil)
		assert.Equal(t, output, result)
	}()

	<-continueCh
	var onFlying int32
	if shouldCount {
		onFlying = 1
	}
	assert.Equal(t, atomic.LoadInt32(counter), onFlying)

	close(runningCh)
	<-doneCh
	assert.Equal(t, atomic.LoadInt32(counter), int32(0))
}
