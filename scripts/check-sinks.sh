#!/usr/bin/env sh
# Fails if src/ uses a DOM sink that parses HTML. Log lines and messages come
# from anyone on the LAN, so text reaches the DOM through textContent only.
set -eu

cd "$(dirname "$0")/.."

pattern='innerHTML|outerHTML|insertAdjacentHTML|document\.write|createContextualFragment|setHTMLUnsafe|parseHTMLUnsafe|DOMParser'

if grep -rnE "$pattern" src/; then
	echo "forbidden HTML sink in src/ (see PLAN.md 9.4 rule 1)" >&2
	exit 1
fi
echo "no HTML sinks in src/"
