# Decisions

Deviations from `PLAN.md` and answers to its open questions, newest first.
Each entry gives the date, the decision and the reason.

## 2026-11-20: private channels

Asked for by the user: the direct messages as a personal "server", with
private channels of kind chat or gallery made from a "+" with a kind, a
name and members, and the same "+" with a kind on a project's Chats and
Galleries. PLAN 4 and 9.6.3 and CONTRACTS 1.10, 2.1, 2.3, 3.3, 5 and 6
describe it.

- **A hidden personal project per principal, `~<owner>`.** A private
  channel is an ordinary channel in it, so the timeline, galleries, read
  state, follows, members panel, SSE, MCP tools and retention all work
  unchanged, and access stays one mechanism: the owner gets read and
  message on the whole space (`EnsurePersonal`), each member read and
  message on the channel they were added to. `~` is outside the project
  name rule, so no admin-made project can collide, and `chan:~alice/x`
  is a valid stream id. The space has no `#general` (the create hooks
  are skipped) and no settings page.
- **The admin session does not reach other people's spaces.** "Private"
  should mean it: the admin's blanket access stops at personal spaces it
  has no grant in (`Access.open`), and every read goes through
  `Access.Scopes`, so the admin's lists, counts, feeds, inbox, members
  panel and pages leave them out (a test pins each). The admin still
  reaches its own space and channels it was added to. Direct messages
  keep their old rule.
- **Never listed as a project.** `list_projects`, `/api/projects`, the
  left bar's projects and the admin's `/projects` skip personal spaces;
  their channels show under the reader's Direct messages and in
  `list_channels`. A bare channel name prefers a project's channel over a
  private one, and `create_channel` without a project never picks a
  personal space, so existing agents' calls behave as before.
- **Members are people the owner may reach** (a shared project, the rule
  of `send(to=)`). An unknown name and an unreachable one get the same
  `not_found`, so the form cannot probe for principals. Members follow
  the channel (agents too, who otherwise follow nothing by default), so
  it shows in their lists and inbox at once. Being in a private channel
  together makes members reachable to each other (they share the space),
  which they are anyway inside the channel.
- **No grant spec names a personal space**: `cafard token create
  --grant` and pairing links refuse `~...`, since members are the
  owner's to add.
- **The "+" is one plain form, `/channels/new`,** for a project's chat or
  gallery (`?project=&kind=`) and a private one (with member
  checkboxes). It replaces the bundle-only "New channel" dialog: a page
  works without the bundle, gives room to the members list on a phone,
  and leaves one code path. Adding and removing members after creation
  is not built yet.

## 2026-11-20: a Discord-like left bar

Asked for by the user after the projects-first deploy: closer to Discord,
three columns on a desktop. PLAN 9.6.1 to 9.6.3 and CONTRACTS 2.1, 2.3,
3.3 and 6 describe it. This replaces the side list, the project switcher,
the bottom bar and rail (`partials/nav.html`) and home's project cards.

- **One left bar for everything**, on every signed-in page: Home, Direct
  messages, then each project as a `<details>` tree that unfolds in place
  on its chats, galleries, logs and (admin) settings, and the reader's
  corner (Admin, theme, sign out) at the bottom. One place to go anywhere
  is what Discord gets right; the switcher hid the other projects behind a
  click, and the bottom bar duplicated three entries of it. On a phone the
  same bar is the drawer, so there is one navigation to learn.
- **Trees, not server icons.** The user preferred the expanded tree to a
  column of round icons: the icon (the project's initials) and the title
  sit on one row, so a project with few chats needs no second column.
  The page's project is open and marked (squared icon, a pill on the
  edge, `aria-current`); several may be open. With the bundle the folds
  are remembered in `localStorage` (`cafard.sideFolds`, the
  `KeyValueStore` pattern of the logs pane's width), but the page's own
  project always opens: folding the place you are in and then landing on
  a page with no visible row is confusing. Only the reader's clicks are
  recorded, not the page opening its tree, or every project visited
  would stay open.
- **Home is "Needs you", a welcome and the recent activity.** The cards
  only led to the projects, which the bar now lists with the same badges.
  With no project the welcome says what to do (the admin: make one;
  others: ask for a grant).
- **`/dm` stays** as a page, and the Direct messages tree has an "All
  conversations" link to it: it is the no-bundle way to reach every
  conversation on a phone and an address people had.
- **The members panel** lists who can read a channel: a read grant on
  its project or on the channel, plus the admin, filtered by
  `list_principals`' rule (`projects.Reachable`, a shared project), with
  the viewer added. In practice everyone who reads a channel shares its
  project with the viewer, so the filter is a second lock, pinned by a
  test with principals of another project. People, agents, devices, each
  online first. "Online" is cheap: a principal holding an SSE feed open
  (`view.Presence`, counted in the events handler) or with a request in
  the last 5 minutes (`last_seen`); MCP sessions are not counted, an
  agent's calls already move its `last_seen`. The viewer is always
  online. A click opens the direct conversation when the viewer may have
  one (`can_dm`, the same shared-project rule as `send(to=)`).
- **Members on channels only.** Gallery and logs pages have no panel: a
  gallery is read, not talked in, and the logs need the width. The panel
  is a column from 840 px that the top bar's button hides and shows,
  remembered (`cafard.membersHidden`); below 840 px a side sheet from the
  right (`#members` `:target` without the bundle).
- **Live members by polling, not events.** The bundle asks
  `GET /api/channels/{name}/members` again every minute while the panel
  shows, and soon after someone shown offline writes in the channel. A
  presence event in SSE would mean fanning out every feed open and close
  to every page; a minute is good enough for "who is around".
- **The bound logs pane yields to the members.** Left bar (280 px) plus
  members (240 px) plus logs (420 px) leaves too little for the
  conversation at 1200 px, so the pane opens beside the conversation from
  1200 px only when the members are hidden, and from 1600 px always;
  otherwise the "Logs" toggle swaps it in, as on smaller screens.

## 2026-11-19: projects first

Asked for by the user: the UI "is not intuitive at all"; projects should
be the main view, Discord-like, and work on a phone first. PLAN 9.2 and
9.6 now describe it; CONTRACTS 2.1 and 6 the routes, pages and twins.

- **Addresses under the project.** `/projects/<p>` is the project view;
  its pages are `/c/<chat>`, `/g` and `/g/<gallery>[/<seq>]`, `/logs` and
  `/logs/<sink>`, and the admin's `/settings`. One prefix per project
  reads well and makes "where am I" a property of the address. `/p/` is
  pairing redemption, and `/projects` was already the admin's list, so
  the view goes one level down (`/projects/<p>`) and the admin page it
  replaces moves to `/settings`. The admin's form posts keep their
  addresses (`/projects/<p>/sinks` and so on): they are not pages, and
  scripts and tests post to them; they now come back to `/settings`.
- **The old addresses redirect** with 308 and their query (`/c/<p>/<chat>`,
  `/g/<p>`, `/g/<p>/<gallery>`, `/g/<p>/<gallery>/<seq>`), without an
  auth check of their own, since the target checks and the redirect
  tells nothing. Agents' messages, `cafard send` output from before and
  bookmarks carry them. Gallery links pasted in either form still become
  ref cards (`galleryLinkRE`), and `ChatHref`, `GalleryHref`, `ItemHref`
  and their TypeScript twins build the new form.
- **`/projects/<p>` answers 303 to the latest chat** (else `#general`,
  else a gallery, else the logs) instead of rendering it, so the address
  bar, the composer's redirects and the side list's highlight are the
  chat's. A project the viewer may not see, or that does not exist, is
  the same 404.
- **Home stays home with one project.** It holds "Needs you", the direct
  messages and the admin's New project and Admin; redirecting a single
  project away would hide them. The overview pane stays on wide screens.
- **A side list, not a second page of channels.** Every project page
  puts the same list first: a sidebar from 840 px, a drawer below. On a
  phone the project opens on a chat and the list is one tap away (the
  menu button), as Discord does; a list page of its own would add a step
  to every chat change. The direct conversations get the same list,
  which is the page itself at `/dm`.
- **The drawer works without the bundle** through `:target`: the menu
  button is a link to `#side`, the close button and the scrim links to
  `#main`. The bundle's `SideNav` turns the link into a toggle with
  `data-open` (no history entry), Esc and focus, so `.only-scripted` is
  not needed for it.
- **Sinks in the list carry one dot per stream** (a sink has up to five,
  one per transport) and the icon the worst state (down, then
  connected, then idle), with the time of the last line; the dots reuse
  `partials/stream_dot.html`, so `stream_status` updates them as it does
  a chat row's. Sinks are listed only to viewers with project-wide read,
  as `dto.Project.Sinks` already does.
- **Live and Sources stay, folded into projects.** Their addresses keep
  working for everyone (log attachment and event row links go to Live,
  grant-filtered as before), and they are in the Admin menu; inside a
  project its logs pages show the sources' cards and the Live view on
  `*:<p>/*` or `*:<p>/<sink>` with the Live filters but the stream
  picker, plus "Open in Live" for the rest. A stream chip of a chat
  header opens its sink's logs. The logs pages accept a sink name with no
  sink row (a deleted sink keeps its history) and show only what the
  viewer's grants let it read.
- **Navigation: Projects, Direct, Admin, More.** The Projects badge counts
  the open questions for the viewer and Direct the unread direct
  messages (the old Chats badge, split); the Admin item carries the
  "source down" dot. The admin's menu (`/admin`) lists Projects, Agents,
  sign-in links, Sources and Live, and the unrouted drops. Keys: `g p`,
  `g d`, `g l`, `g s`.
- **Track C's shell changed** (CONTRACTS 7.3 asks for a note here):
  `layout.html` gained the drawer's menu button and a kicker line (the
  project) above the title, `nav.html` the items above, and the sprite
  the `menu` and `expand_more` glyphs. The CSS keeps the tokens and
  themes; the project view is a section of its own at the end.
- **Project unread counts are summed from `counts.chats`** on both sides
  (`projectUnread`, `projectTotals`), so no new DTO or SSE event was
  needed; a muted chat already counts zero there.

## 2026-11-19: gallery artifacts in chat

PLAN section 6.4. Choices the brief left open:

- **Ref syntax `[[project/gallery#seq]]` and `[[project/gallery|section]]`.**
  Double brackets are rare in logs and code, so text that only looks like
  a ref is unlikely; `#seq` reads like the `#channel` and `seq` agents
  already use; `|` separates a free-form section name, which is why
  section names cannot hold `[`, `]` or `|`. The project is always
  written: refs only work inside one project, and a full name never
  becomes ambiguous when another project gets a gallery of the same name.
  Links to item and section pages count too, with any host, because
  people paste addresses; a pasted link that happens to match a real item
  of the sender's project becomes a card, which is what they meant.
- **Refs are resolved when read, and stored by name and seq** in
  `meta.refs`, so each reader sees what its grants allow, a removed item
  says so, and the gallery is known (for the access check) after its item
  is gone. `meta.refs` is dropped from every DTO: it would name galleries
  to readers who may not see them.
- **One refusal sentence** for every bad `gallery_refs` seq (missing,
  not an image, another project, no grant), so a probe learns nothing.
  Refs written in the text are kept or left as plain text silently: they
  are the sender's words, not a request.
- **The text a card replaces is taken out of what the web shows** (and of
  the chat list's preview), so a message reads "before and after:" above
  its cards, not the raw ref. Agents' tools keep the text as written; they
  get the refs listed after it.
- **Section refs show the newest four images and the count;** an item ref
  shows one. Unsorted items cannot be referred to as a section (they are
  not one batch).
- **The picker writes refs into the text** instead of a hidden field, so
  the person sees what is sent, can move or delete it, and a message
  waiting offline in the upload queue needs nothing new.

## 2026-11-19: gallery channels

Asked for by the user: agents that test a UI post screenshots in batches,
and a conversation buries them. PLAN section 6.3 is the model. Choices
the brief left open, with their reasons:

- **A kind on the channel, not a new kind of chat.** A gallery is a
  channel whose messages are shown as a grid, so grants, follows,
  retention, unread counts, search and the conversation view all apply
  unchanged, and turning a chat into a gallery (or back) rewrites
  nothing. The one rule that differs: a message to a gallery needs an
  image. Markers stay in the gallery's conversation and are not items.
- **An item is the first image of a message,** its title the first line of
  the text (140 characters, as previews). A message with several images
  is one item; the grid stays one tile per post, and agents are told to
  post one image per message.
- **The section is in `meta.section`,** not a column: it is a property of
  one message, queried with `json_extract` on one channel's rows, and the
  section list is a `GROUP BY` over them. Its name excludes control
  characters and `[`, `]`, `|`, kept for a short syntax that points at
  gallery items from message text.
- **`#general` stays a chat,** so every project keeps a place where text
  can go.
- **The chat list's row of a gallery leads to its grid;** the grid links to
  the conversation (markers, and the composer a human may use) and the
  conversation's header back to the grid.
- **The item page is the no-bundle viewer,** and the stable address of one
  item: `/g/<project>/<gallery>/<seq>`, with newer and older inside its
  section. The bundle's viewer shows the original with the title as a
  caption; previous and next also stay in the section.
- **Agents may make galleries** (`create_channel(kind=gallery)`) as they
  make channels, and change the kind with the binding-change right, so an
  agent with no admin at hand still has somewhere to put screenshots.

## 2026-11-19: projects, sinks and grants

Asked for by the user: a Discord-like organisation where the admin makes
projects, gives each its chats and log sinks, its retention, and decides
who sees what, so each project keeps the history of the data sent and of
the agents' replies. PLAN section 4 ("Projects, sinks and grants") is the
model; CONTRACTS sections 1.10, 2, 3.3 and 5 the interfaces. The choices
among the options the brief left open, with their reasons:

- **Stream ids embed the project after the kind, with `/`:**
  `chan:<project>/<channel>` and `<kind>:<project>/<sink>`. A glob star
  stops at `/` (the AGENTS gotcha), which here is what we want:
  `chan:lab/*` and `*:lab/*` are exactly one project's channels and log
  streams, so grants turn into globs the hub and the store already match.
  Patterns that meant every channel became `chan:*/*`. Another separator
  (`.`, `~`) would have let `chan:*` keep matching, but `.` is legal in
  names and `~` already joins the two names of a direct conversation.
  `dm:<a>~<b>` is unchanged and outside projects. A project is never
  renamed, since its name is in every id.
- **One stream per sink and transport,** no sub-streams: a sink `lab/dut1`
  holds `serial:lab/dut1`, `syslog:lab/dut1`, `netconsole:lab/dut1`,
  `push:lab/dut1` and `otlp:lab/dut1`. Per-host or per-service
  sub-streams (`syslog:lab/rack/host1`) would have needed a second
  separator that globs cross, or doubled every pattern (`*:lab/*` and
  `*:lab/*/*`); the host and the service stay in each entry's `host` and
  `app`, and a sink per host is one form away when someone wants them
  apart. Keeping the transport as the kind keeps the status (a serial port
  is down, a UDP source only idle) and the transport column of Sources.
- **Channel rows keep one key,** `channels.name` = `<project>/<name>`, so
  `'chan:' || name`, the bindings and the follows keep their joins.
  Every project gets `#general` when it is made; `general` and `dm` stay
  reserved. A `channel` argument (MCP, `POST /api/messages`) is
  `project/name`, or a bare name only one project visible to the caller
  has; `send` and `ask_human` default to `general` resolved the same way,
  and an ambiguous name is `unknown_channel` listing the qualified ones.
- **Grants, with credential scopes as a cap.** A token's, session's or
  pairing's scopes stay (they still shape `tools/list`), and everything
  else is their intersection with the grant of the thing touched; without
  a grant nothing is visible. This is the brief's recommendation and the
  smallest change to the auth code: scopes, `Require` and `AddTool`
  behave as before. `ingest` is a project-wide grant only (a channel holds
  no logs to write into). A channel grant covers the channel and the log
  streams its bindings match, so an agent given `#dut1` can still read
  `channel=dut1`; to keep that from widening itself, changing bindings and
  creating, renaming, archiving or deleting channels need `message` on the
  whole project. Bindings must stay inside the channel's project.
- **Direct messages** need the two principals to share a project (any
  grant in it), or one of them to be the admin; reading stays for the two
  parties. `list_principals` shows the principals sharing a project with
  the caller, and `admin`. Without this a principal with no grant could
  still reach everyone by DM.
- **Sink tokens** are a `sink` column on `tokens` (`ON DELETE CASCADE`, so
  deleting a sink revokes them), always kind `device` with `ingest` only.
  They write into their sink whatever `stream` the request names: one
  credential, one place. A principal with an `ingest` grant on a project
  may instead name the sink (`stream=dut1` or `lab/dut1`, kind prefix
  ignored; OTLP's `service.name`, else `host.name`). That second path gives
  the grant scope `ingest` a meaning, and is what keeps existing push
  clients and OTLP exporters working after the upgrade (below).
- **Unrouted logs are dropped and counted,** per transport (`syslog`,
  `netconsole`, `http`, `otlp`) in `status`, shown to the admin on Sources
  and in `GET /api/streams` (`unrouted`), with one debug line per minute
  per transport. `POST /api/log` answers 200 with `accepted: 0` and
  `dropped`, not an error: `cafard push` spools and retries on errors, and
  a line no sink wants would retry forever. OTLP answers the standard
  `partial_success` with the records rejected and a sentence saying why.
  Counting is per entry for HTTP and OTLP (the response's `dropped` and
  `rejectedLogRecords` count lines and records), per message for syslog
  and per datagram for netconsole, which drops an unknown sender before
  it costs reassembly state. A message stream named in `POST /api/log`
  (`chan:x`) stays a 400: that is a mistake, not a log for nobody.
- **Source addresses** are per sink, IPs or CIDRs, shared by syslog and
  netconsole. The most specific prefix wins, then the oldest sink, so a
  `/32` inside a sink's `/24` can go elsewhere. `syslog.allow` still
  filters first. The table is read into memory and swapped on change.
- **Serial ports belong to sinks,** at most one each (`sink_serial`). The
  port type and its validation moved from `internal/ingest/serial` to
  `internal/projects` (`SerialPort`; `serial.Port` is an alias), because
  serial imports deps, which holds the projects service. The manager
  reconciles on every sink change: a new port starts, a removed one stops
  with a "port removed" marker, a changed one restarts ("port settings
  changed"). The hardware fakes and tests are unchanged. `serial_write`
  and `serial_control` are now registered even when no sink has a port:
  tools cannot be added once `/mcp` is mounted, and a port may appear at
  run time.
- **Syslog over UDP and TCP share the sink's `syslog:` stream**, so the
  stream shows down once its last TCP connection closes even when UDP
  senders still write; the hostname in the message is the entry's host.
- **Retention per project.** Four nullable columns; a new project copies
  the config's `retention:` values, and NULL (only the migrated project)
  is filled from the config at the next start, since a SQL migration
  cannot read the file. `retention.streams` overrides still win for line
  age. The config limits stay the limits of what is outside projects:
  direct messages, their images, streams without a project. A project's
  `blobs_max_bytes` counts the blobs its messages reference and trims the
  oldest of them; a blob shared with another project goes with it.
- **Migration of an existing install** (`0070_projects.sql`, then the Go
  step `0071_projects_import` the store runs once): every channel and log
  stream moves into one project, `imported` (the brief allowed `default`
  or `imported`; one project means the yaml import below continues the
  same streams). Entries are re-pointed with `UPDATE entries SET stream`
  in the migration, before the writer starts; the text and the FTS index
  are untouched, and seq order is unchanged. This is the one write to
  entries outside the writer, like the FTS rebuild of 0050, and it costs a
  rewrite of the stream index: the first start of this version may take a
  few minutes on a large database before it listens.
  Bindings, follows, claims and the `stream`, `streams` and `channel` of
  log attachments are rewritten too. A sink is made for every old log
  stream name that is a valid sink name, with the address as its source
  when the name is an IP (syslog and netconsole used the IP when they had
  no name). Every existing principal but `admin` gets a grant on the whole
  of `imported` with the union of its tokens' and sessions' scopes (people
  with neither get `read message`), so agents and phones keep what they
  had. Syslog from hosts that named themselves, and so have no IP on
  record, is dropped until the admin adds their address to a sink.
- **The yaml import runs once per item:** at every start, each `serial`
  port and `netconsole.names` entry not recorded in `config_imports`
  becomes a sink of `imported` (made if missing), and a warning says to
  remove the keys from the file. Recording them means a sink the admin
  deleted does not come back. The keys still load for this version.
  `channels:` keeps working: a bare name is in `imported`, `project/name`
  elsewhere (the project is made if missing).
- **Access is cached** per principal and credential; every change through
  the projects service drops the cache, and entries expire after 5 s so a
  grant made by `cafard token create --grant` (another process) applies
  without a restart. Pairing redemption adds its grants in the same
  transaction as the session.
- **The admin UI** is two pages on the admin interface, `/projects` and
  `/projects/{name}`, with plain form posts (no JSON API for admin
  writes), as `/agents` and `/pair` are. Projects cannot be deleted yet.
  A sink token belongs to a device principal named `<project>-<sink>`
  unless the admin types another. The pages have no script of their own:
  they reuse `PairPage` for the shell and the countdown of sign-in codes.
  The chat list has one section per project (its sinks link to their
  logs on Live), Sources one group per project, and the channel pickers
  of Live and the quick drop one `<optgroup>` per project.
- **CLI:** `cafard token create --sink project/sink` and `--grant
  project[:channel]=scopes` (repeatable); `cafard pair --grant` the same.
  No `cafard project` command: projects are made on the admin interface.

## 2026-11-19: screenshots by path through `cafard mcp`, and `cafard send`

Asked for by the user: an agent's screenshot is a file on disk, and
writing megabytes of base64 into a tool call is impractical.

- **The stdio proxy reads the files, the server never does.** `cafard mcp`
  adds `image_paths` to the `send` tool's input schema when the remote's
  `send` takes `images`, and rewrites each call: the files become
  `images` entries, and the paths are removed before the call is
  forwarded. The remote's `/mcp` is unchanged and refuses the argument
  (its strict decoding), since it cannot read the agent's disk. Only
  images pass (the type is sniffed with the blob store's rules), so the
  argument cannot be used to ship other files off the agent's machine.
- **Limits are the server's inline ones, hard-coded:** 6 images, 10 MiB
  each (`messaging.MaxAgentImages` and the inline limit). The proxy cannot
  read the server's configuration; it checks the same numbers to fail
  before uploading, and the server still decides.
- **`cafard send` uses the HTTP API, not MCP.** It posts the multipart
  form of `POST /api/messages` with `Accept: application/json`, so files
  go through the same path as the web UI (10 files of 20 MiB by default)
  and need no base64. It prints `seq N in #chat` and the link
  `server + ChatHref(chat) + #mN`, built with the same helper as the
  pages so a change of chat URLs follows.
- **Image-only messages get a preview label.** A chat row showed
  `agent: ` and nothing; the preview text is now `[1 image]` or
  `[N images]` when the message has no text (server side and in the live
  update of the chat list). A message with text keeps its text alone, so
  the rendered fixtures do not change. Unread counts already counted
  messages whatever their text; there are no other notifications.

## 2026-11-17: admin password and split interfaces

Asked for by the user, so cafard can sit in a DMZ, and to make admin access
as simple as in the sibling project claudication. This replaces the admin
token, the token sign-in and the token counting of the exposure guard (PLAN
section 10, CONTRACTS section 2).

- **Two interfaces.** `http_listen` stays the public interface. A new root
  key `admin_listen` gives the admin routes an address of their own: the
  password sign-in (`POST /signin`), the password change (`POST /password`),
  `/agents`, `/tokens/...`, `/pair`, `/pair/{id}/qr.svg`, `/api/tokens` and
  `/api/pairings`. On `http_listen` they then answer 404 (`POST /signin`
  is registered as a 404 rather than left to the 405 its `GET` twin would
  earn), so publishing the public side cannot publish them, whatever the
  proxy does. Empty `admin_listen` serves both on one address, as before.
  Both addresses are bound before anything serves; a failure stops the
  start. They must differ (`cafard config` refuses the same value).
- **Two muxes instead of claudication's `routes(role)`.** cafard's
  `Register` functions add MCP tools and background work, so they cannot run
  once per listener. The public mux is built as before; `web.Register` also
  gets an admin mux for the admin-only routes, and the admin mux ends with
  `/` handing everything else to the public mux. Each mux is built once
  (no pattern registered twice). The admin listener, or the only listener,
  serves the admin mux wrapped in `auth.AdminInterface`, which marks the
  request context; `TestSplitListenersDoNotServeEachOther` pins the 404s.
- **One admin password.** Stored as `scrypt$N$salt$key` (N=32768, r=8, p=1,
  16-byte salt, 32-byte key, hex) in a one-row table `admin` (migration
  `0060_auth_admin.sql`); `golang.org/x/crypto/scrypt` is pure Go. At least
  8 characters. At most two key derivations run at once (each takes 32 MiB),
  and a wrong password, or a sign-in while none is set, costs the same
  derivation and the same message. Sign-in is limited to 10 a minute, burst
  10, per remote address, on the injected clock. The token bucket moved from
  `internal/messaging` to `internal/ratelimit` so both use it; it now drops
  refilled buckets past 4096 keys, since remote addresses are unbounded.
- **Admin sessions live in memory** (a restart signs the admin out), for 12
  hours fixed, in the cookie `cafard_admin` (HttpOnly, SameSite=Strict,
  Secure per `secure`). Only the admin interface reads that cookie; the
  public one ignores it, which matters because cookies do not tell ports
  apart. The session acts as the human principal `admin` with `ingest read
  message admin`, the old bootstrap token's set. Changing the password (on
  `/agents`, needs the current one) or `cafard passwd` ends every admin
  session of the process that did it; the CLI says that open sessions end
  at the next restart.
- **The admin scope exists only there.** Token, session and pairing
  creation refuse it (`auth.ErrAdminScope`); the token form no longer offers
  it. The migration removes it from every stored token, session and
  pairing, and deletes those left with no scope. An install's old
  first-start token keeps `ingest read message`.
- **No sign-in by token any more.** The public `/signin` page only says to
  ask the administrator for a sign-in link or QR code. People sign in with
  the existing pairing flow; `cafard pair --name NAME [--scopes]
  [--ttl 5m, at most 24h]` makes such a link from a shell and prints
  `<public_url>/p/<code>`. That code is not in the server's memory, so the
  Pair page lists it without its QR.
- **First start.** `Tokens.Bootstrap` is gone. With no password set, `serve`
  makes a random one (18 bytes, hex), stores it and prints it once to
  stdout with the admin sign-in URL (stdout only, never the log). The
  installer runs `cafard passwd --if-unset` from a pipe first, on a new
  install and on an upgrade from a version without passwords, so the
  service has nothing to print. `cafard passwd` sets the password without
  the old one (a shell on the server is the higher privilege), from a
  terminal without echo or from one piped line.
- **Exposure guard.** It now refuses a routable `http_listen` or
  `admin_listen` while no admin password is set. After the first-start step
  one always is; the guard stays for the case where setting it failed.

## 2026-11-16: trim pass

- **The chat list's long-press menu and the channel dialog's pattern preview wait on the injected `Clock`,** not the global timer, and the chat list cancels a pending long press when it is unmounted (before, a press could open a menu on a page already gone, and tests could not drive either delay).
- **Live Sources updates now carry a stream's labels.** The single-stream
  store read left them out, so every live update sent empty labels; it now
  shares the full stream list's query.

## 2026-11-16: track G polish (M5 Live and Sources, M6 search, stdio proxy, term)

- **The FTS index holds ANSI-stripped text** (migration
  `0050_g_fts_stripped.sql`). With raw text, `\x1b[31merror` tokenised as
  `31merror`, so a coloured line was not found by its words. The triggers
  now index `cafard_fts_text(text)`, a deterministic Go function the store
  registers on the SQLite driver in `init` (`store/search.go`), so every
  connection of every process has it (serve, `token`, tests). The
  migration drops the old triggers, empties the index (`delete-all`) and
  refills it from `entries`; deletes pass the same function so the
  external-content index stays consistent; an `UPDATE OF text` trigger
  was added for completeness (nothing updates text today). Inserts and
  deletes still happen only in the writer's transactions, so "only the
  writer writes entries and entries_fts" holds. Cost: a database written
  by this version cannot take inserts from a binary or a `sqlite3` shell
  without the function. To let the store import the stripper without a
  cycle (`textclean` imports `store`), `StripANSI` moved to a new leaf
  package `internal/ansi`; `textclean.StripANSI` wraps it.
- **`search`:** FTS5 over the log streams (never `chan:*`/`dm:*`
  without `channel=`); with `channel=`, also `chan:<name>`, each message
  checked with `Messaging.Visible` (a DM lives in `dm:*` and is never
  read). Results ascend by seq, `limit` 100 by default and 400 at most,
  formatted with `textclean.Format`; a new optional `since_seq` continues
  a search (the footer of `Format` already says "continue with
  since_seq"). `from`/`to` filter the receive time `ts` (what seq order
  follows), and take RFC 3339 (also without zone, read as UTC, or a bare
  date), `now`, or `-<n><unit>` with ms, s, m, h, d, w and combinations
  (`-1h30m`). **Sanitising:** `AND`, `OR`, `NOT`, `NEAR(...)`, `+`,
  parentheses, `"phrases"`, `prefix*` and `^initial` pass; any other word
  with characters FTS5 does not take in a bare word is quoted (a phrase
  of its tokens); a comma outside `NEAR(...)` separates words. What
  SQLite still refuses (an unclosed quote, unbalanced parentheses)
  becomes `bad_request` with its reason and how to fix the query.
- **`cafard mcp`** (`internal/mcpproxy`): one local SDK server mirroring
  the remote's tools, resources, templates, instructions and server info,
  each handler forwarding to one streamable HTTP client session with the
  bearer token. Progress notifications are mapped back by token (kept 2 s
  after the result, since the SDK may handle a notification just after
  the response); cancellation follows the context; subscriptions are
  forwarded and replayed after a reconnect. The protocol's own `_meta`
  keys (`io.modelcontextprotocol/...`, protocol version and client info)
  are not forwarded: each side negotiates its own version, and the remote
  refused a version it had not agreed to. **Reconnects:** a call refused
  with "session not found" is retried once on a new session (it never
  ran); a call that failed with "connection closed" is not retried (it
  may have run, `send` must not post twice), and a session that ends on
  its own is replaced in the background. The tool list is read once at
  start: a token whose scopes change needs the proxy restarted.
- **`cafard term`** (`internal/term`): the console is raw through
  `golang.org/x/term` (new dependency, v0.46.0, an extension of the x/
  family already used; it makes `golang.org/x/sys` direct, which the
  Windows code also uses to turn on `ENABLE_VIRTUAL_TERMINAL_PROCESSING`
  so a board's colours render in the classic console). Both cross-build
  with `CGO_ENABLED=0`. It is shipped for Linux and Windows; the Windows
  console is a manual check, and README keeps com0com + hub4com with
  `push --serial` as the fallback. The port opens first (a mistyped COM5
  fails before the console turns raw); a lost port is reopened with
  backoff (1 s to 30 s) with a note on screen, and keys typed meanwhile
  are dropped with one note. Everything read goes through a pipe to
  `push.Pusher.Reader`, with the spool of `cafard push`; on quit the
  pusher has 5 s to deliver, then what is left stays in the spool.
  `--stream` defaults to the port's lowercased name. Without `--server`
  it only relays. Logs go to stderr with `\r\n`, since raw mode turns
  off the terminal's newline translation.
- **Live renders messages** with `kinds=message`, on the server too: the
  message streams visible to the viewer (the channel's only with
  `channel=`, also in SSE now; `view.MessagePatterns`), each entry
  checked with `Visible`, merged by seq with the log lines and capped at
  500. A message is a `tpl-line` row with `data-kind="message"` (new
  settable attribute, CONTRACTS 6.2): the chat from the viewer's side in
  the stream column, `»` as its severity letter, "sender: text". Line
  actions on a message row are the copy ones only, and a range never
  includes messages. `kinds=message` alone skips the log read.
- **LogView paints once per animation frame** (`enqueue`, through an
  injected `FrameScheduler`; `append` stays synchronous): a frame renders
  only the newest 5000 entries of what arrived, in one fragment, then
  trims. **While paused, new entries stay out of the DOM** (at most 5000,
  counted in "N new") so the rows being read do not move; resuming shows
  them. This also applies to the chat's logs pane.
- **Live reuses `LogsPane`** (track D) with the window as its scroller:
  `LogsPane` gained options (scroller, frames), `enqueue`, a long press
  (500 ms, touch only; the phone's `contextmenu` on long press is
  ignored), copy-only menus for message rows, the stream of a marker read
  from its text, and a toast when a multi-stream range without a channel
  can only attach its first stream. The line menu became the partial
  `partials/line_menu.html` (server-only), shared by both. `/live`
  carries the quick drop sheet when the session has `message`; attaching
  puts the chip in it and opens it. The filter form gained a channel
  picker and the Messages kind when messaging runs.
- **Sources are live:** `status.Seen` now notifies on every entry
  (still throttled to one per 2 s per stream) instead of only on a change
  of state, and SSE fills `stream_status` with what status does not know:
  `last_line` (stripped), `last_seq`, `labels` from the store
  (`Store.StreamRow`) and `claim` (absent: nobody holds it) through an
  `ActiveClaims` assertion on messaging. Compact screens clip the time to
  `mm:ss` in CSS (monospace width and a negative indent), so server and
  bundle keep one markup.
- **Agents page:** the token snippets gained a stdio JSON block for
  `cafard mcp`.
- **Shared files touched:** `cmd/cafard/main.go` (the `term` and `mcp`
  entries that said "not yet" now point at the commands; `notYet` is
  gone), `clientcmds.go` (`openSpool` factored out for `term`),
  `main_test.go` (the "not implemented" test became an argument test),
  `internal/mcpserver/logtools.go` (one line), `internal/status`,
  `internal/web/sse/sse.go`, `view/filter.go`, `view/dto.go`,
  `pages/live.go`, `pages/views.go`, templates `live.html`, `line.html`,
  `filter_bar.html`, `logs_pane.html`, `web_test.go` (live twins),
  `app.css` (a section at the end), `src/components/LogView.ts`,
  `LogsPane.ts`, `src/pages/LivePage.ts`, `StreamsPage.ts`,
  `src/render/format.ts`, `LineRenderer.ts`, and `pages/chat.go`: the
  share target now joins title, text and url in that order whatever the
  order of the form's fields (track D's share test built its form from a
  map and failed at random). No config key, no DTO. Migration number
  used: 0050.

## 2026-11-16: track D chat UI

- **No migration.** Pins, mutes, follows and acks already have their
  tables (track A's 0010); the numbers 0012-0019 of A's range, which D
  builds on, are left unused.
- **The chat pages and API need `*messaging.Service`**, not the deps
  interface: `web.Register` type-asserts `d.Messaging` and only then
  mounts them (`Site.SetChats`, `API.MountChats`). Messaging gained, in
  files of D: `Chats`, `Overview`, `QuestionsFor`, `Counts` (`view.Counter`),
  `Present` (`view.Presenter`), `FilteredTimeline`, `ChatOfStream` and
  `WatchChats`, an in-process feed of `seen`, `answered` and `chat` events
  emitted by `Ack`, `SendMessage` (first human reply to a question), channel
  create, update, delete and auto-archive, `SetFollow` and `SetPrefs` (the
  last two for that principal only). A slow watcher loses events rather
  than blocking a send.
- **SSE:** `seen` goes to the page of that chat; `answered` to the chat
  and to home; `chat` to home and chat pages (a chat page whose bindings
  changed reconnects from its cursor). A deleted or renamed-away channel
  sends no `chat` event: there is no Chat to send. Any of the three marks
  the badges dirty, so `counts` follows within a second.
- **Counts:** `open_questions` is the questions of "Needs you" (open,
  addressed to the viewer by name, or to `@humans` for a human, in a chat
  the viewer follows and has not muted, or by name whatever the mute);
  `unread_direct` sums direct chats; a muted channel counts no unread.
  `chats` lists only the non-zero ones.
- **The chat list:** default is followed unarchived channels plus every
  direct conversation; `all=1` adds unfollowed channels, `archived=1`
  archived ones. Order: pinned channels, channels by last activity (a
  message, or the newest event row of a bound channel, which also becomes
  the preview), direct chats by last activity.
- **`PUT /api/chats/{chat}/prefs` with `followed`** is for channels only
  (400 for a direct chat). **`DELETE /api/channels/{name}`** needs
  `?confirm=<name>`. **`f=` filters** read back up to 20 pages of 200 rows
  to fill one page, so a rare kind still shows; "Older" may then show
  fewer rows than 50.
- **`POST /api/messages` takes sessions** (with `RequireSameOrigin`) and
  two new form fields: `back` (where a failed no-JS post returns, only
  its `share` parameter kept) and `next` (where a sent one goes; home's
  question cards return home). A failed form post's banner maps the error
  code to a sentence on the page; the code comes from the URL, so an
  unknown one is not echoed.
- **No-JS read state:** a chat page acks what it renders only when the
  binary has no bundle. With a bundle it cannot know whether scripts will
  run, so it carries `<noscript><img src="/seen?seqs=...">`: only a
  browser without scripts loads it. `/seen` (new route, CONTRACTS 2.1)
  refuses a `Sec-Fetch-Site` other than same-origin; the Lax session
  cookie is not sent with a cross-site image anyway.
- **Channel settings need the bundle** (the dialog uses the JSON API);
  without it the header's stream chips and the Chat/Logs links still
  work. Everything else of the chats works without JavaScript.
- **Message text:** `messaging.Segments` (track A) now drops escape
  sequences and control characters line by line, so a message cannot
  retitle a terminal or hide text, in pages and in the JSON alike.
- **Template contract additions** (CONTRACTS 6.2 updated): `tpl-image`
  may set `href` on its root (`/blob/` only); `tpl-question` sets `id` and
  its hidden inputs' values; `tpl-choice` sets `value`;
  `tpl-needs-you-question` has a `chat` slot with `href`; home lists
  `tpl-line` (log attachment previews). `partials/choice`,
  `partials/needs_you_question` and `partials/drop_form` are defined inside
  `question.html`, `needs_you.html` and `quick_drop_sheet.html`.
- **The share target:** staged shares live in memory (`view.Shares`, 1 h);
  their photos are visible to their sharer only, through a new
  `blob.Store.SetGrant` (no message references them yet); title, text and
  url are joined into the text. The service worker re-sends the shared
  form itself (a navigation's own request follows no redirect inside a
  worker), reads the id from the final URL, and answers 303 to
  `/?share=<id>`. It caches the shell files only (network first) and
  never a page or the API. Browsers allow service workers only on HTTPS
  or localhost.
- **Uploads:** one job at a time, in order; a network failure or a busy
  server (429/503, honouring `Retry-After`) holds the queue and retries
  with backoff (2 s to 60 s); a refusal (400, 403, 413) waits for the
  person and lets later messages go. Jobs and their photos are kept in
  IndexedDB until sent; photos are processed once. `ResizeToJpeg` always
  re-encodes (quality 0.85), which drops EXIF and its GPS position; GIFs
  and photos that do not decode are sent as they are. Pending uploads
  show in the composer's upload list, not as a count in the connection
  badge (PLAN 9.6.2), which is track C's component.
- **Keys and the service worker** are set up in `mountShell` (track C's
  file, two additions), so `g c`/`g l`/`g s` work on every page; `?`
  needs the shortcuts dialog the chat pages carry.
- **Layout is CSS only.** The one style the bundle sets is the logs
  pane's width (`--pane-logs` on the chat layout) while dragging its edge;
  the width is kept in `localStorage`. The textarea grows with
  `field-sizing: content` where browsers have it.
- **Shared files touched:** `internal/web/web.go` (chat wiring),
  `pages/site.go` (one field), `pages/home.go` (replaced), `sse/sse.go`
  (chat events), `api/messages.go` (session auth, `back`/`next`),
  `messaging` and `blob` as above, `static/icons.svg` (glyphs appended),
  `manifest.webmanifest` (share target), `src/App.ts` (three pages and the
  upload queue), `src/pages/Shell.ts`, `src/net/LiveFeed.ts` (three
  events), `src/net/CafardApi.ts`, `src/model/*`, `src/render/format.ts`.
  Tests of other tracks adjusted: the web tests start the real messaging
  service and their seed has chats (fixture pages regenerated); A's
  ask_human test follows the 303 to sign-in (chat pages need a session);
  C's App test registers its own page name instead of `home`, and the
  LivePage test reads `data-next-seq` from the fixture.
- **New DTOs** (`internal/dto/web_d.go`, fixtures in
  `fixtures_test.go`): `ChatPrefs`, `ChatRead`, `AckRequest`, `Acked`.
  No new dependency.

## 2026-11-16: track E2 serial control

- **`serial_write` and `serial_control` are added in `serial.Register`**
  (scope `serial_write`), only when at least one port is configured: with
  no port they could only fail. No shared file changed for them.
- **The claim gate asserts `deps.Messaging` to `serial.ClaimChecker`**
  (`Holds`), and to `mcpserver.ClaimSource` (`ActiveClaims`) to name the
  holder; serial never imports messaging and `internal/deps` is unchanged.
  Without messaging every write is refused.
- **Gate order and codes:** unknown stream `not_found` (lists the
  ports), not `writable` `forbidden` (names the config key), claim held by
  someone else `conflict` (names the holder and the expiry), no claim at
  all `forbidden` ("claim it first"). The scope is checked by `AddTool`
  and hides the tools. All three gates apply to every call, even an empty
  `serial_control` that only reads the line state, as PLAN 7 says "same
  three gates". Then: port down `busy` with `retry_after_s` 5; raw `tcp`
  or plain Telnet control `bad_request` with E1's reason; invalid
  arguments `bad_request`; a port that fails mid-way `busy`.
- **The marker is written before the bytes or the control action,**
  with `Writer.Commit`, so the board's echo and answer always come after
  it in seq order; the tools return its seq (`marker_seq`) as a cursor. If
  the action then fails, a second marker says so (`serial: write by
  agent-a failed: ...`, severity 4). Refusals (gates, unsupported
  transport, down port, bad arguments) are checked before the marker and
  leave none. Markers carry `sender` = the principal and
  `meta.serial` = `write` | `control` (`write_failed`, `control_failed`).
- **Marker texts:** a write is `<principal>> <data>` with the newline
  included, `\r \n \t \\` and other control bytes, invalid UTF-8,
  C1 and bidi controls escaped, cut after about 300 bytes with
  `... (N bytes in all)`. A control action is `serial: <principal>
  set 57600 baud, set DTR off, set RTS on, sent a 250 ms break, pulsed
  DTR for 100 ms` (the steps asked, in the order they run).
- **One `serial_write` sends at most 16 KiB**, newline included
  (`too_large` beyond). `newline` is a pointer so `""` (no newline) and
  absent (`\r`) differ. `pulse.line` is case-insensitive.
- **No test hook in the serial package:** the end-to-end test configures
  real ports against loopback TCP fakes (an RFC 2217 peer and raw TCP
  peers), so the server's own `Open` runs.

## 2026-11-16: merging tracks A, B, C, E1, F

- **Every DTO fixture is in the one list in `fixtures_test.go`** (the
  per-track files and their `init` hook were merged in the trim pass), so
  its stale-file check knows every fixture. Request bodies (`*_create`) are skipped by
  the TypeScript parse test: the client sends them, it never parses them.
- **`deps.Messaging.ResolveChannel` returns binding patterns,** as track A
  decided; the comment in `internal/deps` now says so.

## 2026-11-16: track C web foundation

- **Sessions and pairings** are migration `0020_c_sessions.sql`. Sessions
  are hashed like tokens (`cfs_` prefix, so a pasted cookie is not taken
  for a token), copy the scopes of the token or pairing that made them,
  and last 30 days, renewed when used with less than 15 days left. The
  cookie itself is HttpOnly, SameSite=Lax, `Secure` with `secure`, and
  kept 400 days; the server decides when a session ends. `pairings` has
  an `id` (for `/pair/{id}/qr.svg`) and a `created_by` beside the
  columns of PLAN 4.
- **The clear code of a pending pairing is kept in memory** for its 5
  minutes, so `/pair` can draw the QR again on reload; only its hash is
  stored. After a restart the page says so and the admin makes a new one.
  Redemption is one `UPDATE ... RETURNING`, so two phones scanning the same
  code cannot both win.
- **Signing in needs a token of a human principal** (the admin token is
  one); an agent or device token is refused with a sentence saying why.
- **`web.Register` builds its own `auth.Sessions`** (it is stateless but
  for a last-seen throttle), so the Register signature stays the
  contract's. `serve.go` builds another for `TokenAuthenticator.Sessions`
  and for the exposure guard counter (`Sessions.CountAdmin`, live admin
  sessions only).
- **`assets.Layout` gained the shell's fields** (theme, active nav item,
  user, badges, rendered twins, notice, error, back, path), and
  `assets.Register` serves `/manifest.webmanifest`, `/sw.js` and
  `/icons.svg` from `static/` when the file exists (CONTRACTS 2.2). Both
  are track 0's file; the shell is track C's, and needs them.
- **No template functions.** `assets_test` parses every template with an
  empty FuncMap, so templates only use fields and methods; wording and
  formatting are built in Go (`pages/views.go`). Each page file's body is
  its content: `pages.New` clones the set per page and defines `content`
  as that file. Twins are declared with `Site.AddTwin(id, partial, zero)`;
  track D declares its own the same way.
- **ANSI colours are rendered on the server too** (`view.ANSI`), into
  the same fixed classes as `render/AnsiParser.ts`, so no-JS and live
  lines look alike. Both are checked against
  `internal/web/testdata/ansi/vectors.json` (`go test ./internal/web/view
  -update`). Stream tag tones are FNV-1a mod 8 on both sides.
- **Log times are shown in UTC** with milliseconds, on the server and in
  the bundle, as agents see them; the Sources page says so.
- **Palette:** no Material tool runs offline, so `scripts/palette.py`
  ports the needed part of material-color-utilities (CAM16, HCT by tone,
  tonal spot, 2023 role tones). Its error role is M3's `#ba1a1a`. It also
  picks the ANSI and stream tag colours by contrast and writes the ratios.
- **Messaging extension points for track A** (optional interfaces, checked
  with a type assertion on `deps.Messaging`): `view.Counter` fills the chat
  badges of `counts`; `view.Presenter` turns a message into its full DTO
  for a viewer. Without them SSE sends `sources_down` only, and a message
  as one text segment with `chat = ChatOf(e)`. SSE checks
  `Messaging.Visible` on every message itself, whatever the hub lets
  through, and sends no message when `Messaging` is nil.
- **SSE details:** `scope=home` carries every message visible to the
  viewer until follows exist; `chat=` needs messaging (404 before);
  `since_seq` wins over `Last-Event-ID`; a replay past 5000 entries, or a
  lagged subscriber, gets `reset` and the stream ends. The bundle's
  `LiveFeed` replaces EventSource's own retry with a backoff (1 s to 30 s)
  and resumes with `since_seq`, since EventSource cannot set
  `Last-Event-ID` on a new connection.
- **New request DTOs without fixtures** (`dto.TokenCreate`,
  `PairingCreate`, `MarkCreate`, `MarkCreated` in `internal/dto/web_c.go`):
  the fixture test's stale-file check only knows `fixtures()`, so a
  second fixture list in a new test file would be reported stale.
  A one-line hook in `fixtures_test.go` would fix it; that file is track
  0's. The pairing fixture's `url`/`qr_url` are examples; real values are
  `<base>/p/<code>` and `/pair/<id>/qr.svg`.
- **`POST /api/marks` answers 201** with `{seq}`.
- **Pages carry a strict CSP** (`default-src 'self'`, no inline script or
  style) and `Cache-Control: no-store`.
- **New dependency:** `rsc.io/qr` v0.2.0, named in PLAN 2, pure Go.

## 2026-11-16: track A messaging, channels, claims, images

- **`ResolveChannel` returns the channel's binding patterns** (ids and
  globs), not the ids they match now. The log tools and the hub match
  patterns themselves, so a stream that appears during a `wait_for`
  (a glob binding) is still seen. `Service.BoundStreams` gives the
  matched ids; log attachments in channel form store those. CONTRACTS
  1.9 comment updated; the comment in `internal/deps` (track 0) still
  says "match now".
- **`follows` has a `followed` column** (migration 0010). A row with
  `followed = 0` is an explicit unfollow, so humans can follow every
  channel by default without a row per channel, and pin/mute survive an
  unfollow. Agents auto-follow (create, send, `channel=` read) only when
  no row exists, so an explicit unfollow sticks.
- **Three config sections** (`messaging:`, `images:`, `channels:`), as the
  task asked. `channels:` is `messaging.Channels`, a struct around the
  list with its own YAML (un)marshalling, because the loader only calls
  `Validate` on structs; unknown keys inside it are still errors.
- **One Register line, `api.RegisterMessages(mux, mcp, d, msg)`,** in
  `internal/web/api` (allowed to import messaging). It adds the MCP tools
  and resources, the janitor tasks and the routes, and returns the
  service's `Start` (auto-archive every hour, resource notifications).
  `messaging.New` runs before `mcpserver.New`, makes sure `general`
  exists (source `config`, so it is never renamed or deleted) and
  reapplies config channels; it writes no entry, as the writer is not
  running yet. A config channel dropped from the file becomes a runtime
  one.
- **`internal/mcpserver` extensions,** none changing an existing tool:
  a tool output implementing `ExtraContent` adds content after the text
  (the images of `get_message`); the principal is also put in the
  handler's context (`auth.WithPrincipal`); `ClaimSource` lets
  `list_streams` show claim holders; `AddResource`, `AddResourceTemplate`,
  `SetSubscriptions` and `ResourceUpdated` add resources. **SDK servers
  are now per principal** (key: name + scopes) instead of per scope set:
  `ResourceUpdated` on a shared server would tell every principal's
  sessions that "the inbox" changed, which leaks when someone else gets a
  direct message.
- **Subscriptions** work on `cafard://inbox` and `cafard://stream/{id}`,
  throttled to one notification per 2 s per URI and principal, with a
  trailing one. Templates use `{+id}` so ids with `:` match.
- **Renaming a channel moves its messages** to `chan:<new>` (an
  `UPDATE entries` run through `writer.W.Exec`, like deletes), and moves
  follows. Deleting runs through the writer too. Rename markers are
  written like binding markers.
- **Log attachments:** "expired" means every line of the reference is
  gone (no entry of its streams left in its seq range up to its send-time
  `last_ts`); partial loss is not flagged. A snapshot is read back by
  parsing the `Format` text into entries, so collapsed repeats come back
  as one line. If the blob size limit removed a snapshot, reads fall back
  to the stream.
- **Questions** carry `meta.choices` (an empty array for a free-text
  `ask_human`); that key is what makes a message a question. A reply's
  `choice` must be one of them; a choice-only reply gets the choice as its
  text.
- **`wait_message` acks what it returns** (`ack: false` to keep it), or
  a second call would return the same message forever. It returns stored
  unacked matches first, then waits.
- **Claims:** default TTL 15 min, max 24 h; claiming again renews (and
  writes a "renewed" marker). An admin may release someone else's claim.
  Expiry is found by the janitor or lazily by the next claim/release on
  that resource, whichever comes first; `DELETE ... RETURNING` makes sure
  only one writes the "expired" marker.
- **Blobs:** any unreferenced blob is kept for 1 hour (uploads in flight,
  staged shares), then deleted; the size limit deletes the oldest
  originals with their variants. Uploaded files are always sniffed and
  must be png, jpeg, webp, gif or heic. `/blob` also sends
  `Content-Security-Policy: default-src 'none'; sandbox`. Decoding runs
  in a pool of `NumCPU/2` workers (1 to 4).
- **Hooks for later tracks:** `Service.SetStagedLookup` (the share
  target's `staged` ids, track D), `Service.OnAck` (SSE `seen`, track C),
  `Service.Holds` (`serial_write`, track E2), `Service.EntryDTO`,
  `ChannelChat`, `DirectChat`, `Timeline`/`TimelineDTO`, `SetPrefs`,
  `AckUpTo` for the chat JSON API.
- **`POST /api/messages`** is bearer-only for now; the handler reads its
  principal from the context, answers JSON with `X-Cafard: 1` or
  `Accept: application/json`, else 303 to the chat (or back with
  `?error=<code>`). It also takes a urlencoded form. Files are streamed
  part by part into the blob store, not buffered by `ParseMultipartForm`.
- **New dependency:** `golang.org/x/image` v0.46.0 (scaling, webp), pure
  Go, named in the task.

## 2026-11-16: track E1 serial ingest and push

- **The `serial:` section is `serial.Config`, a struct holding `Ports
  []Port`, not a bare `[]serial.Port`.** The config walk only calls
  `SetDefaults` and `Validate` on struct fields, so a bare list would never
  be checked by `cafard config`. The struct reads and writes the plain YAML
  list (`UnmarshalYAML`/`MarshalYAML`); since a custom unmarshaler loses
  the decoder's unknown-key check, it checks the keys of each port itself.
- **Port defaults are 115200 8N1** for `device` and `rfc2217`. Raw `tcp`
  ports refuse `baud`, `data_bits`, `parity` and `stop_bits` rather than
  ignore them, so nobody believes they were applied. Names match
  `^[a-z0-9][a-z0-9._-]{0,62}$`.
- **Markers:** `serial: connected: <how>` on the first open too (its text
  says RFC 2217 with the framing, raw TCP, or plain Telnet and why), one
  `cannot open` marker per streak of failed opens, `disconnected from
  <addr>: <reason>` and `reconnected after <gap>: <how>`. Severity 4 for
  failures and drops, 6 for connects; `meta` is `{"serial": "<event>"}`.
  The backoff (1 s doubling to 30 s) resets only after a connection that
  lasted longer than 30 s, so a flapping adapter does not write a marker
  pair every second.
- **RFC 2217 details:** no answer to COM-PORT-OPTION within 5 s counts as
  a refusal (plain Telnet). Line settings are sent without waiting;
  SET-CONTROL and SET-BAUDRATE wait up to 2 s for the server's answer and
  a silent server is assumed to have obeyed; a different answer is an
  error. The modem and line state masks are left at the server's default.
  If the peer refuses BINARY, NVT rules apply (CR NUL). Carrier detect
  markers come from NOTIFY-MODEMSTATE only: local ports read CTS and DCD
  when a control request runs, they are not polled.
- **Line control API for track E2** (`serial.Manager.Lookup(stream)` then
  `Handle.Write` / `Handle.Control`): steps run in the order baud, dtr,
  rts, break, pulse; a pulse flips the line from its current state (taken
  as asserted when unknown, since ports open with DTR and RTS up) and puts
  it back, even if the context ends; `break_ms` and `pulse.ms` are at most
  10 s; a baud change is kept across reconnects until restart. The
  package enforces the config gate (`writable`) itself; scope and claim
  are E2's. An empty request only reads the line state. Errors wrap
  `ErrUnknownStream`, `ErrNotWritable`, `ErrDisconnected`, `ErrBadRequest`
  or `ErrUnsupported` (`*UnsupportedError` with the reason: raw TCP, or a
  plain Telnet peer). `Register` adds no tools; E2 adds them in
  `serial.Register`, where the Manager is built.
- **`cafard push` spool:** one file per server and stream under the user
  cache directory (`~/.cache/cafard/spool`, `%LocalAppData%\cafard\spool`,
  `~/Library/Caches/cafard/spool`), NDJSON as sent, the delivered offset in
  a `.pos` file beside it. It is created only when a line needs it and
  deleted once drained, so a healthy push leaves nothing behind. Over the
  100 MiB cap the newest lines are dropped and a line saying how many goes
  into the stream. While the spool holds anything, new lines go behind it,
  which keeps the order. A replayed batch is at most 1000 lines or 4 MiB.
  `--spool PATH` and `--spool off`; with no cache directory push warns and
  keeps lines in memory as before.
- **`push --serial` must open the port at start** (a mistyped COM5 fails at
  once) and reopens it with backoff after that. `--follow` starts at the
  end of the file like `tail -f`; the new `--from-start` sends what it
  already holds. Ctrl-C ends a serial or follow push with status 0.
  Disconnects of a pushed port are logged on stderr only: `/api/log`
  takes lines, not markers.
- **New dependency `go.bug.st/serial` v1.8.0** (named in PLAN section 2),
  pure Go; the five `CGO_ENABLED=0` cross-builds pass with it.

## 2026-11-16: track B network ingest (M4)

- **Three config sections and three `Register` lines** (`syslog`,
  `netconsole`, `otlp`), one per package of CONTRACTS.md 1.1, instead of
  the one-field / one-line rule of 7.3, which assumes one package per
  track. Each type has its own `SetDefaults` and `Validate`.
- **Shared helpers in `internal/ingest/ingestutil`** (stream names from
  untrusted text, capped meta, allowlists, declare-once) and fakes in
  `internal/ingest/ingestutil/ingesttest`. Both are track B packages.
- **Dependencies:** `go.opentelemetry.io/proto/otlp` v1.11.1 (only
  `logs/v1`, `common/v1`, `resource/v1` are imported) and
  `google.golang.org/protobuf` v1.36.12 (`proto`, `protojson`,
  `protowire`; tests also use `dynamicpb`, `protodesc`, `descriptorpb`).
  **`go mod graph` does list `google.golang.org/grpc` and grpc-gateway,
  as requirements of the otlp module's own go.mod** (the module also holds
  the collector packages). Nothing pulls them into the build: they are not
  in our `go.mod` or `go.sum`, and `go list -deps -test ./...` has no
  grpc, genproto or collector package. Every otlp version has those
  requirements, so this is the best available.
- **Reference types for the response test** are `dynamicpb` messages
  built from descriptors copied from `logs_service.proto` and
  `google/rpc/status.proto` (without `details`): the generated collector
  package cannot be imported, even in a test, without putting gRPC in
  `go.mod`. Protobuf bytes are compared exactly; JSON is compared as
  values, because `protojson` output is deliberately unstable.
- **OTLP errors are `google.rpc.Status` bodies** in the request's
  encoding (as the OTLP/HTTP spec says), with gRPC codes 3, 8 or 14; 415
  for an unknown Content-Type or Content-Encoding (JSON body then). The
  401/403 from `auth.Require` keep the cafard error format.
- **OTLP queues with `Submit` instead of `CommitBatch`:** a lossy submit
  first, then, if the queue is full, a blocking one bounded by 2 s per
  entry. If nothing could be queued the answer is 503 with
  `Retry-After: 5`; if the wait ran out part-way, the request succeeds
  with `partial_success` counting the records not stored (a 503 then
  would make the client resend records already stored). Records the
  writer refuses also count as rejected. The response comes before the
  commit, which the writer does within 50 ms and flushes on shutdown.
- **OTLP JSON bodies may hold several requests in a row** (Vector's
  legacy `json` codec sends newline-delimited objects); their resources
  are concatenated. Invalid or all-zero hex ids are dropped, not refused.
- **OTLP bodies become one entry per line**, as everywhere else; every
  line of a record shares its severity, `src_ts` and meta. A record with
  no body uses `event_name` as text.
- **Meta is capped at 4 KiB by dropping, not cutting:** scalar fields
  first, then object fields (`sd`, `attributes`, `resource`) keep the keys
  that fit, sorted, and `"truncated": true` marks the loss.
- **Syslog TCP streams are down when their last connection closes**
  (counted per stream across connections). `logger -T` opens one
  connection per run, so its stream shows down between runs. UDP streams
  are never marked connected or down.
- **RFC 3164 timestamps have no zone or year:** read in the server's
  local zone, current year, or last year when that lands more than two
  days in the future. RFC 3339 stamps (rsyslog high precision) and a
  trailing year or fraction are also read. A hostname is only taken from
  a 3164 message that has a timestamp.
- **RFC 6587:** octet counting is chosen when the first non-blank byte
  of a connection is 1-9; a frame that stops making sense switches the
  connection to newlines for good. Frames over 256 KiB keep their first
  256 KiB. Newline framing goes through `textclean.Splitter` (500 ms
  partial-line flush). At most 512 TCP connections at once.
- **Netconsole:** plain datagrams go through one `Splitter` per source IP,
  so `pr_cont` pieces join into one line and a prompt is flushed after
  500 ms. Extended messages split by the kernel (`ncfrag=`) are
  reassembled, or stored as received after 2 s. The boot-relative
  timestamp goes to `meta.ts_usec`, not `src_ts`, which has no absolute
  time to give. Severity is the low 3 bits of the level field.
- **`syslog.allow` applies to syslog only,** as in PLAN.md 11; refused UDP
  datagrams are ignored and refused TCP peers disconnected at once.
- **Shared test file edits:** `startServer` in `cmd/cafard/e2e_test.go`
  takes optional config tweaks and starts from `testConfig()`, and
  `TestServeAnswersHealthzAndShutsDownGracefully` uses it too, so no test
  binds the default ports 5514 and 6666 on a shared machine.

## 2026-11-16: track F deployment

- **The installer, not the service, does the first start.** `cafard serve`
  prints the admin token on stdout on an empty database, and under systemd
  or OpenRC stdout is the journal or `/var/log/cafard.log`: the token would
  be kept in a log. So `install.sh`, when the data directory has no
  `cafard.db`, runs `cafard token create --name admin --kind human --scopes
  ingest,read,message,admin --label "first start"` (what `Tokens.Bootstrap`
  does), prints the token to the terminal only, and chowns the data
  directory to `cafard`. The server then finds a principal and prints
  nothing. If `Bootstrap`'s name or scopes change, change the installer too.
- **The installer sets `public_url`** in a NEW `/etc/cafard.yaml` to
  `http://<first address>:8790` (or `CAFARD_INSTALL_PUBLIC_URL`), because
  pairing QR codes pointing at `localhost` are useless to a phone. An
  existing file is never edited.
- **Installer variables are `CAFARD_INSTALL_*`** (`VERSION`, `REPO`,
  `PUBLIC_URL`), not `CAFARD_*`, which cafard itself reads as config keys.
- **Paths:** binary `/usr/local/bin/cafard` (as grafit), config
  `/etc/cafard.yaml`, data `/var/lib/cafard`, OpenRC log
  `/var/log/cafard.log`.
- **SHA256SUMS fails closed in every case:** missing file, asset not
  listed, mismatch, and also no `sha256sum` on the machine (grafit only
  warned on the last).
- **The container drop-in is installed on exit status 226/NAMESPACE, and
  also 219/CGROUP** (what a refused device cgroup policy gives). Any other
  failure stops the installer with the journal, so a bad configuration is
  not mistaken for a container. The drop-in also resets `DevicePolicy` to
  `auto`: in an LXC container the host's device list (`dev0:`) is the
  allowlist.
- **No systemd test in containers:** dev-build has neither docker nor
  podman. `install.sh` was tested from a checkout in chroots of the
  Debian trixie and Alpine 3.24 LXC images (namespaced, no network), and
  the hardened unit on dev-build's own systemd; see the track report.
- **CI checks the unit with `systemd-analyze verify`** and the OpenRC script
  with shellcheck. The release smoke test now also starts the binary and
  checks `/healthz` reports exactly the tag, that `/app/main.js` is served
  (the bundle is embedded), and that the example config loads.
- **`scripts/devdeploy.sh` builds in its own tree on dev-build**
  (`REMOTE_DIR=cafard-deploy`), keeping its `node_modules` there, and takes
  `GOARCH` for arm appliances.

## 2026-11-16: M1 store, ingest and core MCP

- **`Deps` carries root settings (`deps.Server`), not `*config.Config`,
  and a feature's config section is a `Register` argument.**
  `internal/config` imports each feature for its section type, so a
  feature importing `internal/config` through `Deps` was an import cycle.
  CONTRACTS.md 1.2 updated. `Deps` also gained `Tokens`, `Janitor` and
  `Version`.
- **Feature defaults:** a section type with `SetDefaults()` gets it called
  by `config.Default()`, as `Validate()` is called after loading.
- **Migrations are recorded by file name** in `schema_migrations(name,
  applied_at)`, replacing the single `schema_version` number, because
  numbers are reserved per track and may land out of order.
- **The janitor deletes entries through the writer** (`writer.W.Exec`, a
  function run in the writer goroutine between batches, in its own
  transaction), in chunks of 5000 rows. "Only the writer writes entries"
  covers deletes too. Size retention counts text bytes plus 64 per row.
- **`store.Query.MinSeverity` is a `*int`** (nil keeps everything): 0 is a
  real severity, so a zero-value default would have meant "emergencies
  only". `store.Entry.Severity` has the same trap; producers set
  `store.DefaultSeverity` (6) explicitly. `hub.Filter.MinSeverity` keeps
  the contract's `int` with 7 for "all".
- **Stream globs never match message streams** unless the pattern itself
  starts with `chan:` or `dm:`, so `streams=["*"]` cannot leak a direct
  message. Log tools also drop `chan:*`/`dm:*` patterns outright.
- **`wait_for` takes "now" before subscribing,** then subscribes, then
  reads the store, then drains; an entry committed at any point after
  "now" is seen exactly once. A test commits entries at random moments
  around the subscription to pin it.
- **MCP tools are registered through `mcpserver.AddTool`,** which infers
  the schemas with `github.com/google/jsonschema-go` (already required by
  the SDK, now a direct dependency) and returns errors as `isError`
  results whose structured content is `{code, message}`. One SDK server is
  built per scope set, so `tools/list` hides what a token cannot call.
  Sessions are bound to the principal (`TokenInfo.UserID`), so a token
  cannot take over another principal's session.
- **`/mcp` accepts a token only if it has `read`.** Every MCP tool needs
  at least `read`, so an ingest-only device token gets 401 there.
- **The SDK's auth middleware answers 401 on `/mcp`** in its own format
  (with `WWW-Authenticate`), not the cafard error DTO: MCP clients expect
  it.
- **New dependencies:** `github.com/modelcontextprotocol/go-sdk` v1.8.0
  (brings `golang.org/x/oauth2`, `golang.org/x/time`,
  `github.com/segmentio/encoding`, all pure Go), `modernc.org/sqlite`,
  `golang.org/x/sync` (errgroup). The cross-builds stay `CGO_ENABLED=0`.
- **First start prints the admin token to stdout,** never to the log. The
  admin token gets `ingest read message admin`; scopes are independent, so
  admin does not imply `serial_write`.
- **`POST /api/log` also accepts `Content-Encoding: gzip`;** `cafard push`
  sends gzip. Lines get the request's receive time in `ts`, and NDJSON
  `ts` becomes `src_ts`. Body limit 16 MiB after decompression.
- **`cafard push` retries forever with backoff (1 s to 30 s, or the
  server's `Retry-After`)** while the server is unreachable or busy, and
  stops at once on a refusal (4xx), printing the server's sentence. The
  disk spool comes with M5.
- **`token` subcommands:** `cafard token create|list|revoke`, opening the
  database directly; SQLite WAL lets it run beside a live server.

## 2026-11-16: M0 skeleton

- **Templates and static files live under `internal/web/assets/`**
  (`templates/`, `static/`, `static/app/`). `go:embed` cannot reach a parent
  directory, and CONTRACTS.md 1.1 gives the embed to package
  `internal/web/assets`; CONTRACTS.md 7.3 already said
  `internal/web/assets/static/app.css`. PLAN.md and AGENTS.md paths fixed.
- **The bundle is served at `/app/main.js`** (CONTRACTS.md 2.2), with a
  content ETag and `Cache-Control: no-cache`. `/static/app/` is not served,
  so the bundle has one URL.
- **Every scalar config key gets a flag and an environment variable for
  free**, derived from its yaml path: `otlp.max_body_mib` is
  `-otlp-max-body-mib` and `CAFARD_OTLP_MAX_BODY_MIB`. Lists and maps get
  neither, which is the file-only rule of PLAN.md 11. A feature adds its
  sub-struct to `config.Config` and nothing else; a `Validate() error`
  method on it is called after loading.
- **Unknown keys in `cafard.yaml` are an error,** so a typo fails at
  `cafard config` rather than being silently ignored. Fields tagged
  `secret:"true"` print as `<redacted>`.
- **New root key `log_level`** (`debug | info | warn | error`, default
  `info`), for cafard's own slog output.
- **The HTTP server has no read or write timeout,** only a 10 s header
  timeout and a 2 min idle timeout: SSE and `wait_for` stay open for
  minutes, and phone uploads are slow. Request contexts end on shutdown.
- **The exposure guard lands with tokens in M1;** in M0 the server only
  serves `/healthz` and static files.
- **`golang.org/x/sync/errgroup` (named in CONTRACTS.md 1.2) is added in
  M1,** when there is more than one background task to run.
- **TypeScript test fakes live in `src/testing/`** (`FakeClock`). vitest
  runs every test under happy-dom. The `Clock` interface is `now()` plus
  `setTimeout()` returning a `Disposable`.
- **`scripts/check-sinks.sh`** is the sink grep, run in CI and by hand. It
  also forbids `createContextualFragment`, `setHTMLUnsafe`,
  `parseHTMLUnsafe` and `DOMParser`. `h()` refuses `on*` attributes,
  `style`, `srcdoc`, and URLs that are neither same-origin paths nor
  http(s).
- **Release binaries report the tag without its `v`** (`cafard 1.2.3`), as
  grafit's do.
- **`go test -race` needs cgo for the test binary,** so it runs on dev-build
  and in CI, which have a C compiler. Shipped binaries stay
  `CGO_ENABLED=0`.

## 2026-11-16: review fixes

- **`CONTRACTS.md` wins on contracts.** Routes, DTOs, SSE events, Go
  interfaces, templates and MCP scopes are written once there, so agents
  working in parallel agree; PLAN.md points to it.
- **One stream per direct conversation,** `dm:<a>~<b>` (names sorted).
  Visibility is per stream, and a DM is visible only to its two parties,
  admin included.
- **The writer owns `entries` only.** Other tables are written in short
  transactions of their own; their markers still go through the writer,
  so `seq` stays commit order without funnelling every update.
- **Claims move to M2,** so channels and markers are complete before the
  UI; `serial_write` and `serial_control` come with serial in M5.
- **`happy-dom` is the second frontend dev dependency,** as the vitest
  environment for renderer tests against the Go fixture page.
- **Stream status is `connected | idle | down`.** Only serial ports and
  TCP peers can be down; UDP, HTTP and OTLP sources are only idle.
- Logs views are called the "logs pane" everywhere (`--pane-logs`).
## 2026-11-16: log attachments

- **A message can attach slices of streams** (stream or channel, seq range or
  open-ended, optional grep and severity). They are references, and closed
  ranges within 2000 lines and 256 KiB are also snapshotted as text blobs,
  because log retention (14 days) is shorter than message retention (90
  days). Humans attach by selecting lines in a logs view. Agents attach
  through `send(logs=...)` and read with `get_message` and
  `get_log_attachment`.

## 2026-11-16: home is the chat list

- `/` is the chat list with a "Needs you" section on top: open questions
  you can answer in place, and unread direct messages. On a desktop the list
  sits beside an overview pane, and no chat opens automatically. A
  quick-drop button and the Android share target send a photo to any chat
  without opening it.

## 2026-11-16: chats as workspaces

- **Channels can be bound to streams** (ids or globs). The user works per
  device or per task. A channel then groups the conversation and the logs:
  - The web UI shows the bound logs beside the chat, in a pane on large
    screens or behind a Chat/Logs toggle on a phone.
  - Markers, and optionally error lines, appear as event rows in the chat.
  - Agents pass `channel=` instead of listing streams.
- **The web UI is organised by chats.** The main page is "Chats" (`/`,
  `/c/{name}`, `/dm/{principal}`), and Live stays the global view across
  all sources.
- **The channel timeline is a union computed on read, never copies,** so
  changing a channel's bindings also changes its history view.
- **Follows decide which broadcasts reach an inbox,** so agents are not
  flooded by every channel. Following is automatic on create, send and
  read, and humans follow all channels by default. It is not access
  control.

## 2026-11-16: web UI layout

- **Material 3 look in plain CSS, copied from grafit's stylesheet,** with a
  palette generated from a cafard seed (chestnut `#8b5a2b`). The window size
  classes are compact, medium, expanded and large: bottom navigation bar
  below 600 px, navigation rail above, two panes from 840 px, a logs pane
  from 1200 px.
- **Markup is defined once, in Go templates.** The bundle clones
  server-rendered `<template>` twins of each partial and fills `data-slot`
  parts. The no-JS page and the live page cannot drift apart, and there is
  no HTML parsing in the browser.
- **Message text is parsed into safe segments on the server** (text, code,
  link). There is no Markdown parser on either side.

## 2026-11-16: standard protocols

- **OTLP/HTTP logs ingest** (`POST /v1/logs`, protobuf and JSON). The
  standard way log pipelines export, so Vector, Fluent Bit, the OTel
  Collector and OTel SDKs work unchanged. `POST /api/log` stays for `curl`.
  Only message types from `go.opentelemetry.io/proto/otlp` are used; the
  collector service package is avoided because it pulls in gRPC.
- **RFC 2217 serial client.** Raw TCP leaves baud rate and line control to
  the far end; RFC 2217 lets cafard set them and toggle DTR/RTS/break, which
  is how boards get reset remotely. Raw TCP stays as a fallback. New MCP tool
  `serial_control`, gated like `serial_write`.
- **Static bearer tokens now, OAuth 2.1 later (M8, optional).** The MCP spec
  defines OAuth 2.1 for HTTP, but the target clients accept a fixed header,
  which suits a lab network. Auth sits behind an interface so OAuth can be
  added without touching handlers.
- **No A2A at the core.** A2A expects every agent to be a callable server;
  terminal agents are MCP clients and cannot be called. A shared mailbox
  over MCP is the only design they can use. An A2A gateway stays a possible
  extension.

## 2026-11-16: decided with the user while planning

- **Agents can message each other and the human.** The user asked for it.
  Messages are entries in the same timeline as the logs. Read state is kept
  per reader (acks). Questions to the human use `ask_human`.
- **Go for the server, TypeScript classes for the frontend.** The frontend
  is plain DOM, with no framework, as in grafit. The user asked for an
  object-oriented design and good use of design patterns; PLAN section 9.4
  lists the allowed patterns.
- **Deployment follows the other Go projects in `~/misc`:**
  - grafit: CI, release, installer, hardened systemd unit, `remote.sh`;
  - piCVert: OpenRC, the LXC container drop-in, `devdeploy.sh`, YAML config.
- **Module path is `github.com/nebuloss/cafard`,** matching the GitHub
  remote the other projects use. grafit's module path does not match its
  remote; that mismatch is not copied.
- **The Vite bundle is not committed,** as in grafit; piCVert commits its
  bundle. A committed `README.txt` placeholder keeps the embed working on a
  clean checkout.
