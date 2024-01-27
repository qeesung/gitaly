package requestinfohandler

import (
	"context"
	"strings"

	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/logging"
	grpcprometheus "github.com/grpc-ecosystem/go-grpc-prometheus"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	gitalyauth "gitlab.com/gitlab-org/gitaly/v16/auth"
	"gitlab.com/gitlab-org/gitaly/v16/internal/grpc/protoregistry"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"gitlab.com/gitlab-org/labkit/correlation"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

var requests = promauto.NewCounterVec(
	prometheus.CounterOpts{
		Name: "gitaly_service_client_requests_total",
		Help: "Counter of client requests received by client, call_site, auth version, response code and deadline_type",
	},
	[]string{
		"client_name",
		"grpc_service",
		"grpc_method",
		"call_site",
		"auth_version",
		"grpc_code",
		"deadline_type",
		"method_operation",
		"method_scope",
	},
)

// RequestInfo contains information about the current RPC call. Its main purpose is to be used in generic code such
// that we can obtain RPC-call specific information.
type RequestInfo struct {
	correlationID   string
	FullMethod      string
	methodType      string
	clientName      string
	remoteIP        string
	userID          string
	userName        string
	callSite        string
	authVersion     string
	deadlineType    string
	methodOperation string
	methodScope     string

	Repository  *gitalypb.Repository
	objectPool  *gitalypb.ObjectPool
	storageName string
}

type requestInfoKey struct{}

// Extract extracts the RequestInfo from the context. Returns `nil` if there is on RequestInfo injected into the
// context.
func Extract(ctx context.Context) *RequestInfo {
	info, ok := ctx.Value(requestInfoKey{}).(*RequestInfo)
	if !ok {
		return nil
	}

	return info
}

// Unknown client and feature. Matches the prometheus grpc unknown value
const unknownValue = "unknown"

func getFromMD(md metadata.MD, header string) string {
	values := md[header]
	if len(values) != 1 {
		return ""
	}

	return values[0]
}

// newRequestInfo extracts metadata from the connection headers and add it to the
// ctx_tags, if it is set. Returns values appropriate for use with prometheus labels,
// using `unknown` if a value is not set
func newRequestInfo(ctx context.Context, fullMethod, grpcMethodType string) *RequestInfo {
	info := &RequestInfo{
		FullMethod:      fullMethod,
		methodType:      grpcMethodType,
		clientName:      unknownValue,
		callSite:        unknownValue,
		authVersion:     unknownValue,
		deadlineType:    unknownValue,
		methodOperation: unknownValue,
		methodScope:     unknownValue,
	}

	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return info
	}

	if methodInfo, err := protoregistry.GitalyProtoPreregistered.LookupMethod(fullMethod); err == nil {
		var operation string
		switch methodInfo.Operation {
		case protoregistry.OpAccessor:
			operation = "accessor"
		case protoregistry.OpMutator:
			operation = "mutator"
		case protoregistry.OpMaintenance:
			operation = "maintenance"
		default:
			operation = unknownValue
		}

		info.methodOperation = operation

		var scope string
		switch methodInfo.Scope {
		case protoregistry.ScopeRepository:
			scope = "repository"
		case protoregistry.ScopeStorage:
			scope = "storage"
		default:
			scope = unknownValue
		}

		info.methodScope = scope
	}

	if callSite := getFromMD(md, "call_site"); callSite != "" {
		info.callSite = callSite
	}

	if _, deadlineSet := ctx.Deadline(); !deadlineSet {
		info.deadlineType = "none"
	} else if deadlineType := getFromMD(md, "deadline_type"); deadlineType != "" {
		info.deadlineType = deadlineType
	}

	if clientName := correlation.ExtractClientNameFromContext(ctx); clientName != "" {
		info.clientName = clientName
	} else if clientName := getFromMD(md, "client_name"); clientName != "" {
		info.clientName = clientName
	}

	if authInfo, _ := gitalyauth.ExtractAuthInfo(ctx); authInfo != nil {
		info.authVersion = authInfo.Version
	}

	if remoteIP := getFromMD(md, "remote_ip"); remoteIP != "" {
		info.remoteIP = remoteIP
	}

	if userID := getFromMD(md, "user_id"); userID != "" {
		info.userID = userID
	}

	if userName := getFromMD(md, "username"); userName != "" {
		info.userName = userName
	}

	// This is a stop-gap approach to logging correlation_ids
	if correlationID := correlation.ExtractFromContext(ctx); correlationID != "" {
		info.correlationID = correlationID
	}

	return info
}

func (i *RequestInfo) extractRequestInfo(request any) {
	type repoScopedRequest interface {
		GetRepository() *gitalypb.Repository
	}

	type poolScopedRequest interface {
		GetObjectPool() *gitalypb.ObjectPool
	}

	type storageScopedRequest interface {
		GetStorageName() string
	}

	if repoScoped, ok := request.(repoScopedRequest); ok {
		i.Repository = repoScoped.GetRepository()
	}

	if poolScoped, ok := request.(poolScopedRequest); ok {
		i.objectPool = poolScoped.GetObjectPool()
	}

	if storageScoped, ok := request.(storageScopedRequest); ok {
		i.storageName = storageScoped.GetStorageName()
	}
}

func (i *RequestInfo) injectTags(ctx context.Context) context.Context {
	for key, value := range i.Tags() {
		ctx = logging.InjectLogField(ctx, key, value)
		// tags.Set(key, value)
	}
	return ctx
}

// Tags returns all tags recorded by this request info.
func (i *RequestInfo) Tags() map[string]string {
	tags := map[string]string{}

	for key, value := range map[string]string{
		"grpc.meta.call_site":        i.callSite,
		"grpc.meta.client_name":      i.clientName,
		"grpc.meta.auth_version":     i.authVersion,
		"grpc.meta.deadline_type":    i.deadlineType,
		"grpc.meta.method_type":      i.methodType,
		"grpc.meta.method_operation": i.methodOperation,
		"grpc.meta.method_scope":     i.methodScope,
		"grpc.request.fullMethod":    i.FullMethod,
		"grpc.request.StorageName":   i.storageName,
		"remote_ip":                  i.remoteIP,
		"user_id":                    i.userID,
		"username":                   i.userName,
		"correlation_id":             i.correlationID,
	} {
		if value == "" || value == unknownValue {
			continue
		}

		tags[key] = value
	}

	// We handle the repository-related fields separately such that all fields will be set unconditionally,
	// regardless of whether they are empty or not. This is done to retain all fields even if their values
	// are empty.
	if repo := i.Repository; repo != nil {
		for key, value := range map[string]string{
			"grpc.request.repoStorage":   repo.GetStorageName(),
			"grpc.request.repoPath":      repo.GetRelativePath(),
			"grpc.request.glRepository":  repo.GetGlRepository(),
			"grpc.request.glProjectPath": repo.GetGlProjectPath(),
		} {
			tags[key] = value
		}
	}

	// Same for the object pool repository.
	if pool := i.objectPool.GetRepository(); pool != nil {
		for key, value := range map[string]string{
			"grpc.request.pool.storage":           pool.GetStorageName(),
			"grpc.request.pool.relativePath":      pool.GetRelativePath(),
			"grpc.request.pool.sourceProjectPath": pool.GetGlProjectPath(),
		} {
			tags[key] = value
		}
	}

	return tags
}

func (i *RequestInfo) reportPrometheusMetrics(err error) {
	grpcCode := structerr.GRPCCode(err)
	serviceName, methodName := i.ExtractServiceAndMethodName()

	requests.WithLabelValues(
		i.clientName,      // client_name
		serviceName,       // grpc_service
		methodName,        // grpc_method
		i.callSite,        // call_site
		i.authVersion,     // auth_version
		grpcCode.String(), // grpc_code
		i.deadlineType,    // deadline_type
		i.methodOperation,
		i.methodScope,
	).Inc()
	grpcprometheus.WithConstLabels(prometheus.Labels{"deadline_type": i.deadlineType})
}

// ExtractServiceAndMethodName converts the full method name of the request into a server and method part.
// Returns "unknown" in case they cannot be extracted.
func (i *RequestInfo) ExtractServiceAndMethodName() (string, string) {
	fullMethodName := strings.TrimPrefix(i.FullMethod, "/") // remove leading slash
	service, method, ok := strings.Cut(fullMethodName, "/")
	if !ok {
		return unknownValue, unknownValue
	}
	return service, method
}

// UnaryInterceptor returns a Unary Interceptor
func UnaryInterceptor(ctx context.Context, req interface{}, serverInfo *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
	info := newRequestInfo(ctx, serverInfo.FullMethod, "unary")
	info.extractRequestInfo(req)

	ctx = context.WithValue(ctx, requestInfoKey{}, info)

	ctx = info.injectTags(ctx)
	res, err := handler(ctx, req)
	info.reportPrometheusMetrics(err)

	return res, err
}

// StreamInterceptor returns a Stream Interceptor
func StreamInterceptor(srv interface{}, stream grpc.ServerStream, serverInfo *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
	ctx := stream.Context()
	info := newRequestInfo(ctx, serverInfo.FullMethod, streamRPCType(serverInfo))

	ctx = context.WithValue(ctx, requestInfoKey{}, info)

	// Even though we don't yet have all information set up we already inject the tags here. This is done such that
	// log messages will at least have the metadata set up correctly in case there is no first request.
	ctx = info.injectTags(ctx)
	err := handler(srv, &wrappedServerStream{
		ServerStream: stream,
		ctx:          ctx,
		info:         info,
		initial:      true,
	})
	info.reportPrometheusMetrics(err)

	return err
}

func streamRPCType(info *grpc.StreamServerInfo) string {
	if info.IsClientStream && !info.IsServerStream {
		return "client_stream"
	} else if !info.IsClientStream && info.IsServerStream {
		return "server_stream"
	}
	return "bidi_stream"
}

// wrappedServerStream wraps a grpc.ServerStream such that we can intercept and extract info from the first gRPC request
// on that stream.
type wrappedServerStream struct {
	grpc.ServerStream
	ctx     context.Context
	info    *RequestInfo
	initial bool
}

// Context overrides the context of the ServerStream with our own context that has the tags set up.
func (w *wrappedServerStream) Context() context.Context {
	return w.ctx
}

// RecvMsg receives a message from the underlying server stream. The initial received message will be used to extract
// request information and inject it into the context.
func (w *wrappedServerStream) RecvMsg(req interface{}) error {
	err := w.ServerStream.RecvMsg(req)

	if w.initial {
		w.initial = false

		w.info.extractRequestInfo(req)
		// Re-inject the tags a second time here.
		w.ctx = w.info.injectTags(w.ctx)
	}

	return err
}
