package sentryhandler

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func Test_generateSentryEvent(t *testing.T) {
	tests := []struct {
		name        string
		method      string
		sinceStart  time.Duration
		wantNil     bool
		err         error
		wantCode    codes.Code
		wantMessage string
		wantCulprit string
	}{
		{
			name:        "internal error",
			method:      "/gitaly.SSHService/SSHUploadPack",
			sinceStart:  500 * time.Millisecond,
			err:         fmt.Errorf("Internal"),
			wantCode:    codes.Unknown,
			wantMessage: "Internal",
			wantCulprit: "SSHService::SSHUploadPack",
		},
		{
			name:        "GRPC error",
			method:      "/gitaly.RepoService/RepoExists",
			sinceStart:  500 * time.Millisecond,
			err:         status.Errorf(codes.NotFound, "Something failed"),
			wantCode:    codes.NotFound,
			wantMessage: "rpc error: code = NotFound desc = Something failed",
			wantCulprit: "RepoService::RepoExists",
		},
		{
			name:       "nil",
			method:     "/gitaly.RepoService/RepoExists",
			sinceStart: 500 * time.Millisecond,
			err:        nil,
			wantNil:    true,
		},
		{
			name:       "Canceled",
			method:     "/gitaly.RepoService/RepoExists",
			sinceStart: 500 * time.Millisecond,
			err:        status.Errorf(codes.Canceled, "Something failed"),
			wantNil:    true,
		},
		{
			name:       "DeadlineExceeded",
			method:     "/gitaly.RepoService/RepoExists",
			sinceStart: 500 * time.Millisecond,
			err:        status.Errorf(codes.DeadlineExceeded, "Something failed"),
			wantNil:    true,
		},
		{
			name:       "FailedPrecondition",
			method:     "/gitaly.RepoService/RepoExists",
			sinceStart: 500 * time.Millisecond,
			err:        status.Errorf(codes.FailedPrecondition, "Something failed"),
			wantNil:    true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start := time.Now().Add(-tt.sinceStart)
			event := generateSentryEvent(context.Background(), tt.method, start, tt.err)

			if tt.wantNil {
				assert.Nil(t, event)
				return
			}

			assert.Equal(t, tt.wantCulprit, event.Transaction)
			assert.Equal(t, tt.wantMessage, event.Message)
			assert.Equal(t, event.Tags["system"], "grpc")
			assert.NotEmpty(t, event.Tags["grpc.time_ms"])
			assert.Equal(t, tt.method, event.Tags["grpc.method"])
			assert.Equal(t, tt.wantCode.String(), event.Tags["grpc.code"])
			assert.Equal(t, []string{"grpc", tt.wantCulprit, tt.wantCode.String()}, event.Fingerprint)
		})
	}
}
