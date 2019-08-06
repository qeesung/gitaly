mock/mock.pb.go: mock/mock.proto
	protoc --go_out=paths=source_relative,plugins=grpc:./mock -I$(shell pwd)/../../proto -I$(shell pwd)/mock $(shell pwd)/mock/*.proto
	goimports -w mock/mock.pb.go
