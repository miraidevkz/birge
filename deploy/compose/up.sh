#!/bin/sh
# Development stack helper. `./up.sh` starts it, `./up.sh codes` prints the latest SMS texts
# Kratos queued (development has no SMS provider), `./up.sh down` stops it
# and keeps the data volumes. `./up.sh web` exports the web clients against
# the stack (needs Node and installed workspace packages) and serves the
# member app on http://localhost:8081 and the staff admin at /admin on the
# same origin. BIRGE_PUBLIC_URL (e.g. https://demo.example.org) is the origin the
# stack is published at: the clients are built for it and staff sign in there.
set -eu
cd "$(dirname "$0")"
public_url=${BIRGE_PUBLIC_URL:-}
if [ -n "$public_url" ]; then
	export BIRGE_STAFF_ISSUER="$public_url/" BIRGE_STAFF_LOGIN_URL="$public_url/staff-login"
fi
case "${1:-up}" in
up)
	docker compose up -d --build
	docker compose ps --format '{{.Service}} {{.State}} {{.Status}}'
	;;
web)
	member_url=${public_url:-http://localhost:8081}
	# The staff gate in front of /admin and the development login app: a
	# random password, printed once; only its bcrypt hash is kept.
	auth_env=$HOME/.config/birge/admin-auth.env
	if [ ! -s "$auth_env" ]; then
		mkdir -p "$(dirname "$auth_env")"
		password=$(od -An -tx1 -N12 /dev/urandom | tr -d ' \n')
		hash=$(docker run --rm caddy:2.10.2-alpine caddy hash-password --plaintext "$password")
		(umask 077 && printf "BIRGE_ADMIN_AUTH_USER=staff\nBIRGE_ADMIN_AUTH_HASH='%s'\n" "$hash" >"$auth_env")
		echo "staff gate (/admin): user staff, password $password (shown once)"
	fi
	# Served builds live outside the repository tree: tree syncs to the stack
	# host delete untracked files and took the public site down with them.
	web_dir=${BIRGE_WEB_DIR:-$HOME/.local/share/birge-web}
	mkdir -p "$web_dir"
	# Export next to the served copy and swap only after both exports
	# succeeded: a failed export used to leave the site empty (404).
	rm -rf "$web_dir/mobile.next" "$web_dir/admin.next"
	(cd ../../apps/mobile &&
		EXPO_NO_TELEMETRY=1 EXPO_PUBLIC_API_URL=$member_url/v1 \
			EXPO_PUBLIC_KRATOS_URL=$member_url/auth \
			EXPO_PUBLIC_DEMO_CODES_URL=$member_url/demo/code \
			npx expo export --platform web --output-dir "$web_dir/mobile.next" --clear)
	(cd ../../apps/admin &&
		EXPO_NO_TELEMETRY=1 EXPO_PUBLIC_API_URL=$member_url/v1 \
			EXPO_PUBLIC_STAFF_ISSUER=${BIRGE_STAFF_ISSUER:-http://localhost:4444/} \
			npx expo export --platform web --output-dir "$web_dir/admin.next" --clear)
	for app in mobile admin; do
		rm -rf "$web_dir/$app.old"
		[ -d "$web_dir/$app" ] && mv "$web_dir/$app" "$web_dir/$app.old"
		mv "$web_dir/$app.next" "$web_dir/$app"
	done
	BIRGE_WEB_DIR="$web_dir" docker compose --profile web up -d --force-recreate web
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
