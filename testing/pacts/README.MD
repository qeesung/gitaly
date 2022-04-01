We can use the [grpc-gateway](https://grpc-ecosystem.github.io/grpc-gateway/) which is a reverse-proxy to translate HTTP API into gRPC.
That would give use a HTTP endpoint that would be more suited to use with the curent tooling available in Pact.

To generate HTTP endpoints run the following command from the `proto` directory

```
protoc -I . --grpc-gateway_out go/gitalypb \
    --grpc-gateway_opt logtostderr=true \
    --grpc-gateway_opt paths=source_relative \
    --grpc-gateway_opt generate_unbound_methods=true \
    *.proto
```


When running thest tests, you must have a praefect server running.
For initial simplicity, we can use the GDK on own localhost. This can be improved later.

- Configure praefect to run on localhost using `listen_addr` as opposed to `socket_path` in {GITALY_DIR}/praefect.config.toml, and restart GDK
```
# # TCP address to listen on
listen_addr = "192.168.178.33:2305"

# # Praefect can listen on a socket when placed on the same machine as all clients
# socket_path = "/Users/john/dev/gdk/praefect.socket"
```

- Modify `testing/pacts/server.go  grpcServerEndpoint` accordingly to ensure you point at your praefect endpoint.

- Run the tests
    ```
    cd {GITALY_DIR}/testing/pacts
    go test -v
    ```