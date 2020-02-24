# Gitaly authentication middleware for Go

This package contains code that plugs into
github.com/grpc-ecosystem/go-grpc-middleware/auth to provide client
and server authentication middleware for Gitaly.

Gitaly has two authentication schemes.

## V1 authentication (deprecated)

This scheme uses a shared secret. The shared secret is base64-encoded
and passed by the client as a bearer token.

## V2 authentication

This scheme uses a time limited token derived from a shared secret.

The client creates a timestamp and computes the SHA256 HMAC signature
for that timestamp, treating the timestamp as the message. The shared
secret is used as the key for the HMAC. The client then sends both the
message and the signature to the server as a bearer token.

The server takes the message and computes the signature. If the
client-provided signature matches the computed signature the message is
accepted. Next, the server checks if its current time is no more than
30 seconds ahead or behind the timestamp. If the timestamp is too old
or too new the request is denied. Otherwise it goes ahead.
