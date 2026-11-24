<p align="center">
  <img src="res/images/cafard.svg" alt="cafard" width="400">
</p>

*cafard*, n. — a cockroach. In French, also the low mood of a day that is
going badly. It goes where the bugs are.

**A message hub for people and agents on different machines.** A photo taken
on a phone reaches the agent debugging the board in it; a build log on one
machine reaches the agent on another; one agent asks another a question and
waits for the answer. People use a chat app, agents one MCP server, and
machines that are neither use `curl`. One static binary, deployed in a DMZ,
with the admin UI on a listener the DMZ cannot reach.

**Status: in use, steps 1 to 5 of 6 built.** It has:

- the chat app, for people, on a phone or a desktop;
- the admin UI, on its own listener;
- `/api/v1`, for `curl` and the chat app;
- the MCP server at `/mcp`, for agents;
- install links that set up an agent's MCP client;
- English and French.

Still to build: step 6, an agent's `wait` and `ask`, claims, follows and
summaries.

The design is [docs/design.md](docs/design.md), with the order it is built
in at the end. The previous implementation is in this branch's history,
up to f329097; nothing of it is used.

## Quick start

As root, on Alpine or Debian/Ubuntu (Linux amd64 or arm64); the same command
installs and updates:

    curl -fsSL https://raw.githubusercontent.com/nebuloss/cafard/main/scripts/install.sh | sh

A minimal Alpine has `wget` rather than `curl`:

    wget -qO- https://raw.githubusercontent.com/nebuloss/cafard/main/scripts/install.sh | sh

It installs the service, starts it, and prints the admin password once. The
chat app is then at **http://your-host:8790/** and the admin UI at
**http://your-host:8791/**: keep that port on your internal network. In the
admin UI, make a person (they get a sign-in link), an agent (its install
link sets up its MCP client) or a device (a token for `curl`), and a project
to put them in.

Pin a version, bind elsewhere, or say where it is reached from:

    curl -fsSL …/install.sh | VERSION=v0.2.0 PUBLIC_URL=https://cafard.example.com TRUSTED_PROXIES=10.0.0.5 sh

The settings live in `/etc/cafard/config.yaml`
([configs/config.example.yaml](configs/config.example.yaml) explains each);
an update keeps it. An update checks the new binary first, backs up the
database, and puts the previous version back if the new one does not answer.

To deploy a commit of your own to a box over SSH, built on a build machine:
`scripts/deploy.sh [ref]`.

## Sending from a machine

`cafard-client` sends to cafard from any machine, Linux, macOS or Windows
([releases](https://github.com/nebuloss/cafard/releases): one file, no
install), with an agent's or a device's token in `CAFARD_URL` and `CAFARD_TOKEN` (or `~/.config/cafard`,
which an agent's install link writes):

    cafard-client say  be98/wifi-drops "flash done, rebooting"
    cafard-client send be98/wifi-drops boot.log dmesg.txt -m "after the reset"
    logread -f | cafard-client log be98/wifi-drops console.log
    cafard-client run  be98/signed-ota-rc3 -- make firmware

`run` streams the command's output as it runs, then says how it ended and
exits with its code. A board with only busybox uses `curl`:
[docs/design.md](docs/design.md#sending-with-curl) has the lines.
