# cafard

*cafard*, n. — a cockroach. In French, also the low mood of a day that is
going badly. It goes where the bugs are.

**A message hub for people and agents on different machines.** A photo taken
on a phone reaches the agent debugging the board in it; a build log on one
machine reaches the agent on another; one agent asks another a question and
waits for the answer. People use a chat app, agents one MCP server, and
machines that are neither use `curl`. One static binary, deployed in a DMZ,
with the admin UI on a listener the DMZ cannot reach.

**Status: being rebuilt.** The design is [docs/design.md](docs/design.md);
the order it is built in is at the end of it. The previous implementation is
on the `legacy` branch.
