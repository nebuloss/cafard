// Package arch is cafard's layering, written down where a test can hold the
// code to it.
//
// Every package sits on a layer, and imports only from layers below its own.
// Lowest first:
//
//	0  leaves       version logging testenv arch config blobs
//	1  records      store
//	2  rules        service/access
//	3  HTTP         httpapi
//	4  binary       cmd/cafard
//
// Read upward, that is the path of a request: the binary starts the
// composition root in httpapi, which asks service/access whether the caller
// may do what it asks, and service/access reads and writes through the store.
//
// The map grows with the code. docs/design.md has where it is going:
// service/files, service/chat and service/attention between the rules and
// HTTP, and one package per HTTP surface under httpapi.
//
// Two rules that layering alone does not say:
//
//   - service/access is the only package that decides whether a member may
//     do something. Every route and every MCP tool asks it; none reads
//     memberships from the store to decide for itself.
//   - Writes that change access go through the store's methods that take an
//     audit entry, so the change and its line in the audit log are one
//     transaction. There is no way to make one without the other.
//
// A package that is not on the map fails the test. Placing a new package is
// the moment to decide what it may depend on; finding out later is how the
// layering wears away.
package arch
