
all: test_go

test_go: test.proto
	../../../../_build/protoc/bin/protoc \
	--plugin=../../../../_build/bin/protoc-gen-go \
	--go_out=plugins=grpc:. \
	test.proto
