#!/bin/sh
# OpenRC/Alpine launcher — loads node.env then execs remnanode-lite.
set -eu

if [ -f /etc/remnanode/node.env ]; then
	set -a
	# shellcheck disable=SC1091
	. /etc/remnanode/node.env
	set +a
fi

export GOMEMLIMIT="${GOMEMLIMIT:-180MiB}"
cd /var/lib/remnanode
exec /usr/local/bin/remnanode-lite
