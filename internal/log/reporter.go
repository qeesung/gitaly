package log

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors"
	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/logging"
	"google.golang.org/grpc/peer"
	"google.golang.org/protobuf/proto"
)

// reporter implements v2 interceptors.Reporter interface see
// https://github.com/grpc-ecosystem/go-grpc-middleware/blob/main/interceptors/reporter.go.
//
// It is used in interceptor servers to add/extract/modified information from/into the grpc call.
// Refer to the interface implementation for more details.
type reporter struct {
	interceptors.CallMeta

	ctx             context.Context
	kind            string
	startCallLogged bool

	opts   *options
	fields logging.Fields
	logger logging.Logger
}

// PostCall is called by logging interceptors after a request finishes (Unary) or when a stream handler exits.
//
// Internally, PostCall is called during the inPayload RPC stats in stats.Handler interface's HandleRPC method.
// More details can be found here
// at https://github.com/grpc-ecosystem/go-grpc-middleware/blob/main/interceptors/server.go and
// https://github.com/grpc-ecosystem/go-grpc-middleware/blob/main/interceptors/client.go
func (c *reporter) PostCall(err error, duration time.Duration) {
	if !hasEvent(c.opts.loggableEvents, logging.FinishCall) {
		return
	}
	if err == io.EOF {
		err = nil
	}

	code := c.opts.codeFunc(err)
	fields := c.fields.WithUnique(logging.ExtractFields(c.ctx))
	fields = fields.AppendUnique(logging.Fields{"grpc.code", code.String()})
	if err != nil {
		fields = fields.AppendUnique(logging.Fields{"grpc.error", fmt.Sprintf("%v", err)})
	}

	// Appending fields from fields producers, this is our customer logic versus
	// what is defined originally in the go-grpc-middleware v2 reporter struct in logging package.
	for _, fieldsProducer := range c.opts.filedProducers {
		for key, val := range fieldsProducer(c.ctx, err) {
			fields.Delete(key)
			fields = fields.AppendUnique(logging.Fields{key, val})
		}
	}

	msg := fmt.Sprintf("finished %s call with code %s", c.CallMeta.Typ, code.String())

	c.logger.Log(c.ctx, c.opts.levelFunc(code), msg, fields.AppendUnique(c.opts.durationFieldFunc(duration))...)
}

// PostMsgSend is called during the inPayload RPC stats in stats.Handler interface's HandleRPC method.
// It is the method called after a response is sent in a server interceptor or
// a request is sent in a client interceptor.
//
// This implementation is from on the go-grpc-middleware v2 reporter struct in logging package.
// Because logging's reporter is not exported, we have to copy the implementation here.
// More details can be found here at
// https://github.com/grpc-ecosystem/go-grpc-middleware/blob/47ca7d64b840248d6d2ea5a24af6496712396438/interceptors/logging/interceptors.go#L47
func (c *reporter) PostMsgSend(payload any, err error, duration time.Duration) {
	logLvl := c.opts.levelFunc(c.opts.codeFunc(err))
	fields := c.fields.WithUnique(logging.ExtractFields(c.ctx))
	if err != nil {
		fields = fields.AppendUnique(logging.Fields{"grpc.error", fmt.Sprintf("%v", err)})
	}
	if !c.startCallLogged && hasEvent(c.opts.loggableEvents, logging.StartCall) {
		c.startCallLogged = true
		c.logger.Log(c.ctx, logLvl, "started call", fields.AppendUnique(c.opts.durationFieldFunc(duration))...)
	}

	if err != nil || !hasEvent(c.opts.loggableEvents, logging.PayloadSent) {
		return
	}
	if c.CallMeta.IsClient {
		p, ok := payload.(proto.Message)
		if !ok {
			c.logger.Log(
				c.ctx,
				logging.LevelError,
				"payload is not a google.golang.org/protobuf/proto.Message; programmatic error?",
				fields.AppendUnique(logging.Fields{"grpc.request.type", fmt.Sprintf("%T", payload)})...,
			)
			return
		}

		fields = fields.AppendUnique(logging.Fields{"grpc.send.duration", duration.String(), "grpc.request.content", p})
		c.logger.Log(c.ctx, logLvl, "request sent", fields...)
	} else {
		p, ok := payload.(proto.Message)
		if !ok {
			c.logger.Log(
				c.ctx,
				logging.LevelError,
				"payload is not a google.golang.org/protobuf/proto.Message; programmatic error?",
				fields.AppendUnique(logging.Fields{"grpc.response.type", fmt.Sprintf("%T", payload)})...,
			)
			return
		}

		fields = fields.AppendUnique(logging.Fields{"grpc.send.duration", duration.String(), "grpc.response.content", p})
		c.logger.Log(c.ctx, logLvl, "response sent", fields...)
	}
}

// PostMsgReceive is called during the inPayload RPC stats in stats.Handler interface's HandleRPC method.
// It is the method called after a request is received in a server interceptor or
// a response is received in a client interceptor.
//
// This implementation is from on the go-grpc-middleware v2 reporter struct in logging package.
// Because logging's reporter is not exported, we have to copy the implementation here.
// More details can be found here at
// https://github.com/grpc-ecosystem/go-grpc-middleware/blob/47ca7d64b840248d6d2ea5a24af6496712396438/interceptors/logging/interceptors.go#L92
func (c *reporter) PostMsgReceive(payload any, err error, duration time.Duration) {
	logLvl := c.opts.levelFunc(c.opts.codeFunc(err))
	fields := c.fields.WithUnique(logging.ExtractFields(c.ctx))
	if err != nil {
		fields = fields.AppendUnique(logging.Fields{"grpc.error", fmt.Sprintf("%v", err)})
	}
	if !c.startCallLogged && hasEvent(c.opts.loggableEvents, logging.StartCall) {
		c.startCallLogged = true
		c.logger.Log(c.ctx, logLvl, "started call", fields.AppendUnique(c.opts.durationFieldFunc(duration))...)
	}

	if err != nil || !hasEvent(c.opts.loggableEvents, logging.PayloadReceived) {
		return
	}
	if !c.CallMeta.IsClient {
		p, ok := payload.(proto.Message)
		if !ok {
			c.logger.Log(
				c.ctx,
				logging.LevelError,
				"payload is not a google.golang.org/protobuf/proto.Message; programmatic error?",
				fields.AppendUnique(logging.Fields{"grpc.request.type", fmt.Sprintf("%T", payload)})...,
			)
			return
		}

		fields = fields.AppendUnique(logging.Fields{"grpc.recv.duration", duration.String(), "grpc.request.content", p})
		c.logger.Log(c.ctx, logLvl, "request received", fields...)
	} else {
		p, ok := payload.(proto.Message)
		if !ok {
			c.logger.Log(
				c.ctx,
				logging.LevelError,
				"payload is not a google.golang.org/protobuf/proto.Message; programmatic error?",
				fields.AppendUnique(logging.Fields{"grpc.response.type", fmt.Sprintf("%T", payload)})...,
			)
			return
		}

		fields = fields.AppendUnique(logging.Fields{"grpc.recv.duration", duration.String(), "grpc.response.content", p})
		c.logger.Log(c.ctx, logLvl, "response received", fields...)
	}
}

func reportable(logger logging.Logger, opts *options) interceptors.CommonReportableFunc {
	return func(ctx context.Context, c interceptors.CallMeta) (interceptors.Reporter, context.Context) {
		fields := logging.Fields{}
		kind := logging.KindServerFieldValue
		if c.IsClient {
			kind = logging.KindClientFieldValue
		}

		fields = fields.WithUnique(logging.ExtractFields(ctx))

		// Appending fields from fields producers
		for _, fieldsProducer := range opts.filedProducers {
			for key, val := range fieldsProducer(ctx, nil) {
				fields.AppendUnique(logging.Fields{key, val})
			}
		}

		if !c.IsClient {
			if peer, ok := peer.FromContext(ctx); ok {
				fields = append(fields, "peer.address", peer.Addr.String())
			}
		}

		singleUseFields := logging.Fields{"grpc.start_time", time.Now().Format(opts.timestampFormat)}
		if d, ok := ctx.Deadline(); ok {
			singleUseFields = singleUseFields.AppendUnique(logging.Fields{"grpc.request.deadline", d.Format(opts.timestampFormat)})
		}
		return &reporter{
			CallMeta:        c,
			ctx:             ctx,
			startCallLogged: false,
			opts:            opts,
			fields:          fields.WithUnique(singleUseFields),
			logger:          logger,
			kind:            kind,
		}, logging.InjectFields(ctx, fields)
	}
}
