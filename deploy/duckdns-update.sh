#!/usr/bin/env bash
#
# Refresh the DuckDNS record for the dashboard.
#
# DuckDNS expires a subdomain after 30 days without an update, which would take
# the hostname (and therefore the certificate renewal) down even though the
# Elastic IP never changes. A scheduled ping keeps it alive.
#
# The token lives in /etc/pgfleet-duckdns.env, root-owned and mode 600, so it
# stays out of the repository and off the process command line.
#
# Install: see the "Keeping the hostname alive" section of docs/deploy-aws.md.
set -euo pipefail

ENV_FILE=${DUCKDNS_ENV_FILE:-/etc/pgfleet-duckdns.env}

if [ ! -r "$ENV_FILE" ]; then
	echo "duckdns: cannot read $ENV_FILE" >&2
	exit 1
fi
# shellcheck source=/dev/null
. "$ENV_FILE"

: "${DUCKDNS_DOMAIN:?duckdns: DUCKDNS_DOMAIN is not set}"
: "${DUCKDNS_TOKEN:?duckdns: DUCKDNS_TOKEN is not set}"

# An empty ip parameter tells DuckDNS to use the source address of this
# request, so the record self-corrects if the instance ever changes address.
response=$(curl -fsS --max-time 30 \
	"https://www.duckdns.org/update?domains=${DUCKDNS_DOMAIN}&token=${DUCKDNS_TOKEN}&ip=")

if [ "$response" != "OK" ]; then
	# The token is never echoed, so this is safe to log.
	echo "duckdns: update failed for ${DUCKDNS_DOMAIN} (response: ${response:-empty})" >&2
	exit 1
fi

echo "duckdns: ${DUCKDNS_DOMAIN} refreshed"
