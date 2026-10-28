#!/bin/sh
# Development stack helper. `./up.sh` starts it, `./up.sh codes` prints the latest SMS texts
# Kratos queued (development has no SMS provider), `./up.sh down` stops it
# and keeps the data volumes. `./up.sh web` exports the web clients against
# the stack (needs Node and installed workspace packages) and serves the
# member app on http://localhost:8081 and the staff admin on :8082.
set -eu
cd "$(dirname "$0")"
case "${1:-up}" in
up)
	docker compose up -d --build
	docker compose ps --format '{{.Service}} {{.State}} {{.Status}}'
	;;
web)
	# BIRGE_PUBLIC_URL (e.g. https://demo.example.org) builds the member app for
	# the stack published at that origin; the admin stays local-only.
	member_url=${BIRGE_PUBLIC_URL:-http://localhost:8081}
	(cd ../../apps/mobile &&
		EXPO_NO_TELEMETRY=1 EXPO_PUBLIC_API_URL=$member_url/v1 \
			EXPO_PUBLIC_KRATOS_URL=$member_url/auth \
			npx expo export --platform web --output-dir dist --clear)
	(cd ../../apps/admin &&
		EXPO_NO_TELEMETRY=1 EXPO_PUBLIC_API_URL=http://localhost:8082/v1 \
			npx expo export --platform web --output-dir dist --clear)
	docker compose --profile web up -d --force-recreate web
	;;
codes)
	docker compose exec -T postgres psql -U kratos -d kratos -At \
		-c "SELECT created_at, body FROM courier_messages ORDER BY created_at DESC LIMIT 3"
	;;
down)
	docker compose --profile web down
	;;
*)
	echo "usage: $0 [up|web|codes|down]" >&2
	exit 2
	;;
esac
