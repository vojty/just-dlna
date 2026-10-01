#!/bin/sh
# Container entrypoint. Started as root, it hands /cache and /config to
# PUID:PGID (default 1000:1000) and runs just-dlna as that user, so bind
# mounted folders created by Docker as root work without manual chown.
# Started as another user (`user:` / `--user`), it runs just-dlna directly.
set -e

if [ "$(id -u)" = 0 ]; then
	PUID=${PUID:-1000}
	PGID=${PGID:-1000}
	chown -R "$PUID:$PGID" /cache /config
	exec su-exec "$PUID:$PGID" /usr/local/bin/just-dlna "$@"
fi
exec /usr/local/bin/just-dlna "$@"
