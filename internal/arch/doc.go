// Package arch is cafard's layering, written down where a test can hold the
// code to it.
//
// Every package sits on a layer, and imports only from layers below its own.
// Lowest first:
//
//	0  leaves         version logging testenv arch config blobs ratelimit webui live
//	                  mcp imagefit uploads install i18n
//	1  records        store
//	2  rules          service/access
//	3  services       service/chat service/attention
//	4  HTTP plumbing  httpapi/httpx
//	5  HTTP surfaces  httpapi/api httpapi/admin httpapi/join httpapi/agent
//	6  composition    httpapi
//	7  binary         cmd/cafard
//
// The surfaces do not import one another; the composition root hands across
// what one needs from another, and httpx holds what they share (the client's
// address, the cross-site check, the chat sign-in cookie). The store tells
// live what each write changed; /api/v1/events listens. /api/v1 and /join are on the public listener,
// the admin UI and its API on the admin listener.
//
// Read upward, that is the path of a request: the binary starts the
// composition root in httpapi, which hands each surface the services it
// uses; /api/v1 (httpapi/api) passes a request to service/chat, which asks
// service/access whether the caller may, then reads and writes through the
// store and the blob store.
//
// What people are shown is said in their language at the surface that
// answers them (i18n); a refusal carries its English format and arguments
// up to there. The MCP server, the scripts and the log stay English.
//
// The MCP server (httpapi/agent) is a surface like /api/v1: its tools call
// service/chat, and service/attention decides what an agent is offered.
// mcp is the protocol alone and knows nothing of cafard.
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
