# Screenshots on dev-build

A visual check of the web UI with a seeded server and Chromium
(puppeteer-core from `~/shots/node_modules` on dev-build), at 1280x800
and 390x844.

```sh
npm run build
rsync -a scripts/shots/ guillaume@10.0.50.21:cafard-shots/
REMOTE_DIR=cafard-ui scripts/remote.sh 'ln -sfn ~/shots/node_modules ~/cafard-shots/node_modules; REMOTE_DIR=cafard-ui sh ~/cafard-shots/again.sh'
rsync -a guillaume@10.0.50.21:cafard-shots/out/ /tmp/cafard-ui-shots/
```

`again.sh` rebuilds cafard from the synced tree, starts it on a fresh data
directory at 127.0.0.1:18790, seeds it (`seed.mjs`: two projects, chats,
a gallery, sinks with logs, agents, people, a question, direct messages)
and shoots it (`shoot.mjs`). The server keeps running for a look by hand
until the next `reset.sh`.
