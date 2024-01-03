module gitlab.com/gitlab-org/gitaly/tools/protolint

go 1.21

toolchain go1.21.0

require github.com/yoheimuta/protolint v0.46.1

require (
	github.com/chavacava/garif v0.0.0-20230608123814-4bd63c2919ab // indirect
	github.com/fatih/color v1.13.0 // indirect
	github.com/gertd/go-pluralize v0.2.0 // indirect
	github.com/golang/protobuf v1.5.2 // indirect
	github.com/google/go-cmp v0.5.9 // indirect
	github.com/hashicorp/go-hclog v1.2.0 // indirect
	github.com/hashicorp/go-plugin v1.4.3 // indirect
	github.com/hashicorp/yamux v0.0.0-20180604194846-3520598351bb // indirect
	github.com/kr/text v0.2.0 // indirect
	github.com/mattn/go-colorable v0.1.13 // indirect
	github.com/mattn/go-isatty v0.0.16 // indirect
	github.com/mitchellh/go-testing-interface v0.0.0-20171004221916-a61a99592b77 // indirect
	github.com/niemeyer/pretty v0.0.0-20200227124842-a10e7caefd8e // indirect
	github.com/oklog/run v1.0.0 // indirect
	github.com/yoheimuta/go-protoparser/v4 v4.7.0 // indirect
	golang.org/x/net v0.0.0-20220722155237-a158d28d115b // indirect
	golang.org/x/sys v0.5.0 // indirect
	golang.org/x/text v0.14.0 // indirect
	google.golang.org/genproto v0.0.0-20220519153652-3a47de7e79bd // indirect
	google.golang.org/grpc v1.46.2 // indirect
	google.golang.org/protobuf v1.28.0 // indirect
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
