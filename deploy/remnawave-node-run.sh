#!/bin/sh
# OpenRC/Alpine launcher — loads node.env then execs remnanode-lite.
set -eu

if [ -f /etc/remnanode/node.env ]; then
	set -a
	# shellcheck disable=SC1091
	. /etc/remnanode/node.env
	set +a
fi

# 内存上限由进程按 LOW_MEMORY=1 自动设置；需覆盖时在 node.env 设 GOMEMLIMIT
cd /var/lib/remnanode
exec /usr/local/bin/remnanode-lite
