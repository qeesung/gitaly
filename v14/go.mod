module gitlab.com/gitlab-org/gitaly/v14

require (
	github.com/grpc-ecosystem/go-grpc-middleware v1.3.0
	github.com/opentracing/opentracing-go v1.2.0
	github.com/prometheus/client_golang v1.12.1
	github.com/sirupsen/logrus v1.8.1
	github.com/stretchr/testify v1.7.1
	github.com/uber/jaeger-client-go v2.30.0+incompatible
	gitlab.com/gitlab-org/gitaly v1.68.0
	gitlab.com/gitlab-org/labkit v1.14.0
	google.golang.org/grpc v1.46.0
	google.golang.org/protobuf v1.28.0
)

replace gitlab.com/gitlab-org/gitaly => ../

go 1.16
