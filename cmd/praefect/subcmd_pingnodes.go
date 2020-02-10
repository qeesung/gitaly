package main

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	gitalyauth "gitlab.com/gitlab-org/gitaly/auth"
	"gitlab.com/gitlab-org/gitaly/client"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/config"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health/grpc_health_v1"
)

type nodePing struct {
	address   string
	storages  map[string]struct{} // set of storages this node hosts
	vStorages map[string]struct{} // set of virtual storages node belongs to
	token     string              // auth token
	err       error               // any error during dial/ping
}

func flattenNodes(conf config.Config) map[string]*nodePing {
	nodeByAddress := map[string]*nodePing{} // key is address

	// flatten nodes between virtual storages
	for _, vs := range conf.VirtualStorages {
		for _, node := range vs.Nodes {
			n, ok := nodeByAddress[node.Address]
			if !ok {
				n = &nodePing{
					storages:  map[string]struct{}{},
					vStorages: map[string]struct{}{},
				}
			}
			n.address = node.Address
			n.storages[node.Storage] = struct{}{}
			n.vStorages[vs.Name] = struct{}{}
			n.token = node.Token
			nodeByAddress[node.Address] = n
		}
	}
	return nodeByAddress
}

func dialNodes(conf config.Config) int {
	nodes := flattenNodes(conf)

	var wg sync.WaitGroup
	for _, n := range nodes {
		wg.Add(1)
		go func(n *nodePing) {
			defer wg.Done()
			n.checkNode()
		}(n)
	}
	wg.Wait()

	exitCode := 0
	for _, n := range nodes {
		if n.err != nil {
			exitCode = 1
		}
	}

	return exitCode
}

func (npr *nodePing) dial() (*grpc.ClientConn, error) {
	opts := []grpc.DialOption{
		grpc.WithBlock(),
		grpc.WithTimeout(30 * time.Second),
	}

	if len(npr.token) > 0 {
		opts = append(opts,
			grpc.WithPerRPCCredentials(
				gitalyauth.RPCCredentialsV2(npr.token),
			),
		)
	}

	return client.Dial(npr.address, opts)
}

func (npr *nodePing) healthCheck(cc *grpc.ClientConn) (grpc_health_v1.HealthCheckResponse_ServingStatus, error) {
	hClient := grpc_health_v1.NewHealthClient(cc)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := hClient.Check(ctx, &grpc_health_v1.HealthCheckRequest{})
	if err != nil {
		return 0, err
	}

	return resp.GetStatus(), nil
}

func (npr *nodePing) log(msg string, args ...interface{}) {
	log.Printf("[%s]: %s", npr.address, fmt.Sprintf(msg, args...))
}

func (npr *nodePing) checkNode() {
	npr.log("dialing...")
	cc, err := npr.dial()
	if err != nil {
		npr.log("ERROR: dialing failed: %v", err)
		npr.err = err
		return
	}
	defer cc.Close()
	npr.log("dialed successfully!")

	npr.log("checking health...")
	health, err := npr.healthCheck(cc)
	if err != nil {
		npr.log("ERROR: unable to request health check: %v", err)
		npr.err = err
		return
	}

	if health != grpc_health_v1.HealthCheckResponse_SERVING {
		npr.err = fmt.Errorf(
			"health check did not report serving, instead reported: %s",
			health.String())
		npr.log("ERROR: %v", npr.err)
		return
	}
	npr.log("SUCCESS: node is healthy!")
}
