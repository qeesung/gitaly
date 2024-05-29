// Package bundleuri is used to enable the use [Bundle-URI] when the client
// clones/fetches from the repository.
//
// Bundle-URI is a concept in Git that allows the server to send one or more
// URIs where [git bundles] are available. The client can download such bundles
// to pre-populate the repository before it starts the object negotiation with
// the server. This reduces the CPU load on the server, and the amount of
// traffic that has to travel directly from server to client.
//
// [Bundle-URI]: https://git-scm.com/docs/bundle-uri
// [git bundles]: https://git-scm.com/docs/git-bundle
package bundleuri
