# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Working on cafard

A message hub for people and agents on different machines. People use a chat
UI, agents an MCP server, and machines that are neither use `curl`. Go, no
CGO; the UI is built into the binary with `go:embed`. It follows
claudication's way of working (`~/misc/claudication`); when in doubt about a
convention, do what claudication does.

**Status: steps 1 to 3 of the build order are in, and step 4's back end** —
the store, the files, the access rules, `cafard serve` with both listeners,
`/api/v1` behind agent and device tokens and people's chat sign-in (send
with `curl`, live logs included; read by page; conversations started from
their first message; members, reads, file deletion, the profile), live
notices through `internal/live` and `/api/v1/events`, `cafard admin` on the
box, and the admin UI. The chat app itself (step 4c) is next.
[`docs/design.md`](docs/design.md)
is the design: the model, the access rules, the surfaces, the MCP tools, the
order to build in, and what is left for later. As code lands, each section
moves into the package comment it describes, and the design doc keeps only
what no single package owns. The previous implementation is on the `legacy`
branch; nothing here comes from it.

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

`go doc ./internal/arch` is the map, as in claudication: every package on a
layer, importing only from layers below, and a test that fails the build when
an import crosses a layer or a new package is missing from the map. Its test
also fails when a package other than `service/access` reads memberships to
decide something.

Writes that change access take a `store.AuditEntry` and record it in the same
transaction; add a new one the same way.

Still to copy from claudication rather than rewrite, as the steps need them:
`internal/mcp`, `internal/live`, `internal/handover`, `internal/secret`,
`scripts/install.sh`, and the image fitting. Adapt their comments to cafard;
keep the measurements that still apply.

What a screen shows reaches it through `internal/live`: every write marks a
topic, and a tab's one event stream tells its panels to read again. MCP's
`wait` and `ask` listen on the same topics.

## Commands

    make check        # fmt-check, vet, tidy-check, typecheck, test-web, test with -race: what CI runs
    make build        # static binary at dist/cafard, web UIs embedded (runs make web)
    make web          # build the web UIs into internal/webui/dist, where go:embed takes them
    make typecheck    # tsc over web/: Vite strips types without checking them
    make test-web     # the web UIs' unit tests (vitest, under node)
    make test         # Go tests with -race (needs cgo)
    make test-norace  # the same without cgo

Git holds `internal/webui/dist` empty; a bare `go build` serves the APIs and
says the UI is missing. For UI work, `cd web && npm run dev:admin` runs Vite
proxying `/admin/api` to a local `cafard serve`.

One test: `go test ./internal/service/access -run TestName -race -count=1`.

The Makefile pins `GOTOOLCHAIN` to the `go` directive in `go.mod` (1.26.0),
so a plain `go` command may resolve a different compiler than `make` does;
`web/package-lock.json` pins the web compilers the same way.

Subcommands today: `serve`, `config` (check and print the configuration),
`admin` (people, agents, devices, projects, tokens; `cafard admin -h`) and
`version`. `backup`, `restore` and `vacuum` come with the steps that need
them.

`go doc ./internal/httpapi/api`, `./internal/httpapi/admin` and
`./internal/httpapi/join` list the routes. To try a deployed box:
`scripts/devdeploy.sh` puts the pushed branch in a Proxmox container;
`cafard admin create person NAME` inside it makes the first admin and prints
their join link with a QR code, and `cafard admin link NAME` makes a new one.
The admin UI is then at the admin listener's address.

The tests need no credentials and no network. A package whose tests build an
HTTP client calls `testenv.Offline` from its `TestMain`. Anything that talks to
a live server is a script under `scripts/`, run by hand, and says so.

## Conventions

- Comments say *why*, and record what was measured. Keep them accurate or
  delete them.
- Commit messages are imperative and explain the reasoning, not the diff.
  Commits are authored by the repository owner and carry no co-author or
  generated-by trailers.
- **The web UIs are TypeScript, written as classes** (the owner's rule):
  `web/src/admin` and, from step 4c, `web/src/chat`, sharing
  `web/src/shared`. No framework. A piece of page is a `Component` subclass
  that owns one element, returns its markup from `view()`, and declares its
  behaviour in `this.actions`, keyed by `data-act`. Logic that needs no page
  lives in its own class (`PickerState`, `Time`) with a test under
  `web/test`. Markup is built with the `html` tag, which escapes every value
  it is given; `Html.raw` is for markup written in this repo only.
- The pages' Content-Security-Policy allows no inline script and no `style`
  attribute; set data-driven sizes through `element.style` after rendering
  (`Component.sizeBars`). Colours are claudication's MD3 tokens as CSS
  variables: use the tokens, not raw colours.
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
