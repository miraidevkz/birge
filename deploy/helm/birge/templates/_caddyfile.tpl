{{/*
Caddyfile of a web client. Call with (dict "app" "member") or "admin".
No admin API, no automatic HTTPS (TLS ends at the ingress), no state on
disk. Access logs stay off: client addresses are personal data (ADR-0021).
*/}}
{{- define "birge.caddyfile" -}}
{
	admin off
	auto_https off
	persist_config off
}

:8080 {
	handle /healthz {
		respond 204
	}
{{- if eq .app "member" }}
	# The external emergency plan page: never cached, and no Referer leaves
	# it (the token itself is in the fragment and cleared by the page). The
	# ingress sets the same headers; this keeps them without it.
	header /plan {
		Cache-Control "no-store"
		Referrer-Policy "no-referrer"
	}
	# The external SOS page, the same way.
	header /sos-link {
		Cache-Control "no-store"
		Referrer-Policy "no-referrer"
	}
{{- end }}
	handle {
		root * /srv
		try_files {path} /index.html
		file_server
	}
}
{{- end -}}
