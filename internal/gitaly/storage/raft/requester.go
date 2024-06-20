package raft

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/lni/dragonboat/v4"
	"github.com/lni/dragonboat/v4/statemachine"
	"gitlab.com/gitlab-org/gitaly/v16/internal/log"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
)

// ErrTemporaryFailure is returned when a request fails temporarily and can be retried.
var ErrTemporaryFailure = errors.New("temporary request failure")

// anyProtoMarshal marshals msg as an `Any` protobuf message type.
func anyProtoMarshal(msg proto.Message) ([]byte, error) {
	if msg == nil {
		return nil, fmt.Errorf("marshaling any wrapper: empty response")
	}

	wrapper, err := anypb.New(msg)
	if err != nil {
		return nil, fmt.Errorf("marshaling any wrapper: %w", err)
	}
	return proto.Marshal(wrapper)
}

// anyProtoUnmarshal is the counterpart of wrappedMarshal function.
func anyProtoUnmarshal(msg []byte) (proto.Message, error) {
	var any anypb.Any
	if err := proto.Unmarshal(msg, &any); err != nil {
		return nil, fmt.Errorf("unmarshalling any wrapper: %w", err)
	}
	return any.UnmarshalNew()
}

type requestOption struct {
	timeout         time.Duration
	retry           int
	failureCallback func(error)
}

// requester handles Raft requests and responses. It wraps dragonboat's requests to the
// cluster and allows:
// - Automatic retries on temporary errors.
// - Using generics to declare request and response types.
// - Enforcing request types using protobuf messages.
// All interactions to a Raft cluster are encouraged to use this abstraction. The requester
// currently supports synchronous forms of requests. In the future, it will support more
// sophisticated requests that provide more control over the lifecycle of an operation.
type requester[reqT proto.Message, resT proto.Message] struct {
	nodeHost *dragonboat.NodeHost
	groupID  raftID
	logger   log.Logger
	option   requestOption
}

// NewRequester creates a new requester instance that can be used to send Raft requests.
func NewRequester[reqT proto.Message, resT proto.Message](nodeHost *dragonboat.NodeHost, groupID raftID, logger log.Logger, option requestOption) *requester[reqT, resT] {
	return &requester[reqT, resT]{
		nodeHost: nodeHost,
		groupID:  groupID,
		logger:   logger,
		option:   option,
	}
}

// SyncRead sends a linearizable read request to the Raft cluster and returns the response. To
// achieve linearizability, any pending log entries on the node are applied before the request is
// processed.
func (r *requester[reqT, resT]) SyncRead(ctx context.Context, req reqT) (resT, error) {
	var response resT

	result, err := r.withRetry(ctx, req, func(ctx context.Context) (any, error) {
		// SyncRead forces the current node to update index, then perform lookup in the state machine.
		return r.nodeHost.SyncRead(ctx, r.groupID.ToUint64(), req)
	})

	if (err != nil) || (result == nil) {
		return response, err
	}

	response, ok := result.(resT)
	if !ok {
		return response, fmt.Errorf(
			"casting SyncRead response to desired type, expected %s",
			response.ProtoReflect().Descriptor().Name(),
		)
	}
	return response, nil
}

// SyncWrite sends a linearizable write request to the Raft cluster and waits for the response. A
// request results in one log entry. This function returns when the entry is received by a quorum
// and the entry is applied to the node's local statemachine.
//
// You must only call this function when an operation is ready to be replicated to other nodes.
// You must also ensure the operation can be applied to the statemachine without error.
func (r *requester[reqT, resT]) SyncWrite(ctx context.Context, req reqT) (updateResult, resT, error) {
	var response resT

	// We wrap the request in an `Any` message as it also embeds the request's message type. This way,
	// each statemachine receiving the request can unpack it into the appropriate type.
	request, err := anyProtoMarshal(req)
	if err != nil {
		return 0, response, fmt.Errorf("marshaling SyncWrite request: %w", err)
	}

	// Send request with retry. The flow is as follows:
	// - The local node sends the log entry to all members of the Raft group. They acknowledge and persist
	//   the entry, but don't yet apply it.
	// - When the local node receives acknowledgment from the quorum, the log entry is applied to its
	//   statemachine. In the background, it continues replicating to pending members.
	// - If the local application is successful, the log entry is marked as `committed`. The result
	//   will then be returned from the local statemachine.
	// - Other members apply the log entry when they receive the next heartbeat from this node.
	result, err := r.withRetry(ctx, req, func(ctx context.Context) (any, error) {
		session := r.nodeHost.GetNoOPSession(r.groupID.ToUint64())
		return r.nodeHost.SyncPropose(ctx, session, request)
	})
	if err != nil {
		return 0, response, err
	}

	// Parse result. The result wrapper includes 2 parts: Value (integer result code) and Data.
	// The data field is wrapped in an `any` message.
	resultWrapper, ok := result.(statemachine.Result)
	if !ok {
		return 0, response, fmt.Errorf("casting SyncWrite response to statemachine.Result")
	}

	// Unwrap data from `any` wrapper.
	wrappedRes, err := anyProtoUnmarshal(resultWrapper.Data)
	if err != nil {
		return 0, response, fmt.Errorf("unmarshalling SyncWrite response: %w", err)
	}

	// Cast back any to the desired response type.
	response, ok = wrappedRes.(resT)
	if !ok {
		return 0, response, fmt.Errorf(
			"casting SyncWrite response to desired type, expected %s got %s",
			response.ProtoReflect().Descriptor().Name(),
			wrappedRes.ProtoReflect().Descriptor().Name(),
		)
	}

	return updateResult(resultWrapper.Value), response, nil
}

// withRetry is a helper function that retries a request up to the specified number of times
// in case of temporary failures.
func (r *requester[reqT, resT]) withRetry(ctx context.Context, request any, perform func(context.Context) (any, error)) (any, error) {
	attempt := 0
	for {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		attempt++
		ctx, cancel := context.WithTimeout(ctx, r.option.timeout)
		response, err := perform(ctx)
		cancel()
		if err == nil {
			return response, nil
		} else if r.option.failureCallback != nil {
			r.option.failureCallback(err)
		}

		if !dragonboat.IsTempError(err) {
			r.logger.WithError(err).WithFields(log.Fields{
				"attempt":  attempt,
				"request":  request,
				"response": response,
			}).Warn("request failed")
			return response, fmt.Errorf("failed to send request: %w", err)
		} else if r.option.retry == 0 {
			return response, ErrTemporaryFailure
		} else if attempt > r.option.retry {
			return response, fmt.Errorf("retry exhausted: %w", err)
		}
	}
}

// GetLeaderState returns the leader state of a particular group, from the point of view of the current
// node. It does not perform any external request.
func GetLeaderState(nodeHost *dragonboat.NodeHost, groupID raftID) (*gitalypb.LeaderState, error) {
	leaderID, term, valid, err := nodeHost.GetLeaderID(groupID.ToUint64())
	if err != nil {
		return nil, err
	}
	return &gitalypb.LeaderState{
		GroupId:  groupID.ToUint64(),
		LeaderId: leaderID,
		Term:     term,
		Valid:    valid,
	}, nil
}
