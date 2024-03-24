module gitlab.com/gitlab-org/gitaly/tools/protolint

go 1.21

toolchain go1.21.0

require github.com/yoheimuta/protolint v0.49.3

require (
	github.com/BurntSushi/toml v1.3.2 // indirect
	github.com/chavacava/garif v0.1.0 // indirect
	github.com/fatih/color v1.16.0 // indirect
	github.com/gertd/go-pluralize v0.2.1 // indirect
	github.com/golang/protobuf v1.5.3 // indirect
	github.com/hashicorp/go-hclog v1.6.2 // indirect
	github.com/hashicorp/go-plugin v1.6.0 // indirect
	github.com/hashicorp/yamux v0.1.1 // indirect
	github.com/kr/text v0.2.0 // indirect
	github.com/mattn/go-colorable v0.1.13 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/mitchellh/go-testing-interface v1.14.1 // indirect
	github.com/niemeyer/pretty v0.0.0-20200227124842-a10e7caefd8e // indirect
	github.com/oklog/run v1.1.0 // indirect
	github.com/yoheimuta/go-protoparser/v4 v4.9.0 // indirect
	golang.org/x/net v0.20.0 // indirect
	golang.org/x/sys v0.16.0 // indirect
	golang.org/x/text v0.14.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20240123012728-ef4313101c80 // indirect
	google.golang.org/grpc v1.60.1 // indirect
	google.golang.org/protobuf v1.32.0 // indirect
	gopkg.in/check.v1 v1.0.0-20200227125254-8fa46927fb4f // indirect
	gopkg.in/yaml.v2 v2.4.0 // indirect
)

exclude (
	// GO-2022-1059, GO-2021-0113, GO-2020-0015
	golang.org/x/text v0.3.0
	golang.org/x/text v0.3.3
	golang.org/x/text v0.3.5
	golang.org/x/text v0.3.7
)
