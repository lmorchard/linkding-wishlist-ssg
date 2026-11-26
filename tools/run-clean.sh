#!/bin/sh
# Run linkding-wishlist-ssg with clean Linkding environment variables.
#
# Clears LINKDING_URL/LINKDING_TOKEN first: the interactive shell exports a
# LINKDING_URL pointing at links.lmorchard.com, which is the published static
# site, not the Linkding API. Process environment outranks .env by design, so
# leaving it set silently points every invocation at the wrong host and yields 404s.
set -e
unset LINKDING_URL
unset LINKDING_TOKEN
cd "$(dirname "$0")/.."
exec ./linkding-wishlist-ssg "$@"
