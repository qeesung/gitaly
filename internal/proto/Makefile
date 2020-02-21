.DEFAULT_GOAL := internal.pb.go

%.pb.go: %.proto
	protoc --go_out=paths=source_relative,plugins=grpc:. -I${shell pwd}/../../proto/ -I$(shell pwd) $(shell pwd)/*.proto
