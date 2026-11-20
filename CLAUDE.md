# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Working on cafard

A message hub for people and agents on different machines. People use a chat
UI, agents an MCP server, and machines that are neither use `curl`. Go, no
CGO; the UI is built into the binary with `go:embed`. It follows
claudication's way of working (`~/misc/claudication`); when in doubt about a
convention, do what claudication does.

**Status: design only, no code yet.** [`docs/design.md`](docs/design.md) is the
design: the model, the access rules, the surfaces, the MCP tools, the order to
build in, and what is left for later. Read it before writing the first package.
As code lands, each section moves into the package comment it describes, and
the design doc keeps only what no single package owns.

The UI references are two clickable mockups: the chat app,
<https://claude.ai/artifact/GzqmfVgDd9RioxZDzBx6S8>, and the admin UI,
<https://claude.ai/artifact/Qk2QyvmSvcYV2SfRo3tUch>.

## The rules that are easy to break

1. **Access is decided in one place.** `internal/service/access` is the only
   package that answers "may this member do this", and project membership is
   the only permission. Every API route and every MCP tool asks it; none
   checks membership itself.
2. **Attention is decided in one place.** `internal/service/attention` is the
   only package that decides what a session is shown: what is addressed to
   it, never "something new in the project".
3. **The access rules have tests**: no agent or device can change access;
   only people add members; agents reach only members they share a project
   with; removing a member keeps their history.
4. **The public listener creates no identity and no project.** People,
   agents, devices and projects are created only through `admin-listen`; the
   public listener answers 404 to every `/admin/…` path. The chat's only
   access changes are made by a person, within what they already have:
   adding an existing member to their project, an upload link into their
   conversation (labelled, 24 hours at most), signing in their own phone.
5. **A token or join link is never posted in a conversation.** Every member,
   agents included, reads what is there.
6. **`/api/v1` is the only implementation.** The chat app, `curl` and the MCP
   tools all call it; the MCP server adds no ability of its own.
7. **The MCP API makes the order impossible to get wrong.** Every tool but
   `select_project` refuses until a project is selected, and every refusal
   names the next step. After that, no tool takes a project. There is no tool
   that lists every conversation. A change to a tool's description is checked
   with `scripts/probe-agent` against a real model.
8. **No tool returns a whole file**, images are fitted before a model sees
   them, and uploads never pass through a model (`upload_link`, then `curl`).
9. **Everything the MCP server returns says where it came from**, as
   untrusted content. A log line can carry instructions.
10. **Files are never served as active content**: attachment by default,
    `CSP: sandbox`, `nosniff`.
11. **The chat app has three things on screen** (projects, conversations,
    members) and no settings but your own profile. A feature that needs a
    fourth word, or more than three taps on a phone, goes to desktop or to
    admin, or is cut. A signed-in browser is a *sign-in*, never a *device*:
    that word is the send-only identity.

## Where code goes

`go doc ./internal/arch` will be the map, as in claudication: every package on
a layer, importing only from layers below, and a test that fails the build
when an import crosses a layer or a new package is missing from the map. The
first draft of the layers is in the design doc.

Copy these from claudication rather than rewriting them: `internal/mcp`,
`internal/live`, `internal/handover`, `internal/testenv`, `internal/arch`,
`internal/secret`, `internal/logging`, `scripts/install.sh`, and the image
fitting. Adapt their comments to cafard; keep the measurements that still
apply.

What a screen shows reaches it through `internal/live`: every write marks a
topic, and a tab's one event stream tells its panels to read again. MCP's
`wait` and `ask` listen on the same topics.

## Commands

Planned, mirroring claudication's Makefile:

    make check        # fmt-check, vet, test, typecheck, test-web, web build — what CI runs
    make build        # binary at dist/cafard, UI embedded
    make test         # Go tests, with -race
    make test-web     # the UI's pure modules
    make dev-api      # the server with debug logging
    make dev          # Vite on :5173 proxying the API

Subcommands: `serve`, `passwd`, `backup`, `restore`, `vacuum`.

The tests need no credentials and no network. A package whose tests build an
HTTP client calls `testenv.Offline` from its `TestMain`. Anything that talks to
a live server is a script under `scripts/`, run by hand, and says so.

## Conventions

- Comments say *why*, and record what was measured. Keep them accurate or
  delete them.
- Commit messages are imperative and explain the reasoning, not the diff.
  Commits are authored by the repository owner and carry no co-author or
  generated-by trailers.
- The UI is Tailwind 4 with claudication's MD3 token set. Use the tokens, not
  raw colours.
- The UI's words are the ones in the design doc's glossary: person, agent,
  device, member, session, project, conversation, message, file. Not channel,
  server, workspace or topic.

## Deploying

Like claudication: `scripts/install.sh` installs or updates a systemd or
OpenRC service and writes `/etc/cafard/config.yaml` on first run. `listen` is
published in the DMZ; `admin-listen` stays on the internal network. An update
is a handover, not a stop. State (the database, the blobs, the sealing key)
lives in the state directory and never in config. `cafard backup FILE` takes a
consistent snapshot without stopping the service, and is as sensitive as the
state directory.
