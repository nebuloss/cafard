<p align="center"><img src="web/src/shared/favicon.svg" width="96" height="96" alt="cafard's mark: a cockroach"></p>

# cafard

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
