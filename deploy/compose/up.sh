#!/bin/sh
# Development stack helper. `./up.sh` starts it (creating .env with random
# Kratos secrets on first use), `./up.sh codes` prints the latest SMS texts
# Kratos queued (development has no SMS provider), `./up.sh down` stops it
# and keeps the data volumes.
set -eu
cd "$(dirname "$0")"
case "${1:-up}" in
up)
	if [ ! -f .env ]; then
		umask 077
		printf 'KRATOS_SECRET=%s\nKRATOS_CIPHER=%s\n' \
			"$(openssl rand -hex 32)" "$(openssl rand -hex 16)" >.env
	fi
	docker compose up -d --build
	docker compose ps --format '{{.Service}} {{.State}} {{.Status}}'
	;;
codes)
	docker compose exec -T postgres psql -U kratos -d kratos -At \
		-c "SELECT created_at, body FROM courier_messages ORDER BY created_at DESC LIMIT 3"
	;;
down)
	docker compose down
	;;
*)
	echo "usage: $0 [up|codes|down]" >&2
	exit 2
	;;
esac
