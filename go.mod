module gitlab.com/gitlab-org/gitaly/v14

exclude (
	// grpc-go version v1.34.0 and v1.35.0-dev have a bug that affects unix domain docket
	// dialing. It should be avoided until upgraded to a newer fixed
	// version. More details:
	// https://github.com/grpc/grpc-go/issues/3990
	github.com/grpc/grpc-go v1.34.0
	github.com/grpc/grpc-go v1.35.0-dev
)

require (
	github.com/cloudflare/tableflip v1.2.2
	github.com/containerd/cgroups v0.0.0-20201118023556-2819c83ced99
	github.com/getsentry/sentry-go v0.10.0
	github.com/gin-gonic/gin v1.7.3 // indirect
	github.com/git-lfs/git-lfs v1.5.1-0.20210304194248-2e1d981afbe3
	github.com/go-playground/validator/v10 v10.9.0 // indirect
	github.com/golang/protobuf v1.5.2
	github.com/google/go-cmp v0.5.5
	github.com/google/uuid v1.2.0
	github.com/grpc-ecosystem/go-grpc-middleware v1.3.0
	github.com/grpc-ecosystem/go-grpc-prometheus v1.2.0
	github.com/hashicorp/golang-lru v0.5.4
	github.com/hashicorp/yamux v0.0.0-20210316155119-a95892c5f864
	github.com/json-iterator/go v1.1.11 // indirect
	github.com/kelseyhightower/envconfig v1.3.0
	github.com/kr/pretty v0.3.0 // indirect
	github.com/lib/pq v1.10.1
	github.com/libgit2/git2go/v31 v31.4.12
	github.com/mattn/go-isatty v0.0.13 // indirect
	github.com/olekukonko/tablewriter v0.0.2
	github.com/opencontainers/runtime-spec v1.0.2
	github.com/opentracing/opentracing-go v1.2.0
	github.com/pelletier/go-toml v1.8.1
	github.com/prometheus/client_golang v1.10.0
	github.com/rogpeppe/go-internal v1.8.0 // indirect
	github.com/rubenv/sql-migrate v0.0.0-20191213152630-06338513c237
	github.com/sirupsen/logrus v1.8.1
	github.com/stretchr/testify v1.7.0
	github.com/uber/jaeger-client-go v2.27.0+incompatible
	github.com/ugorji/go v1.2.6 // indirect
	gitlab.com/gitlab-org/gitlab-shell v1.9.8-0.20210720163109-50da611814d2
	gitlab.com/gitlab-org/labkit v1.5.0
	go.uber.org/goleak v1.1.10
	gocloud.dev v0.23.0
	golang.org/x/crypto v0.0.0-20210711020723-a769d52b0f97 // indirect
	golang.org/x/sync v0.0.0-20210220032951-036812b2e83c
	golang.org/x/sys v0.0.0-20210809222454-d867a43fc93e
	golang.org/x/text v0.3.7 // indirect
	google.golang.org/grpc v1.38.0
	google.golang.org/protobuf v1.27.1
	gopkg.in/check.v1 v1.0.0-20201130134442-10cb98267c6c // indirect
	gopkg.in/yaml.v3 v3.0.0-20210107192922-496545a6307b // indirect
)

go 1.15
