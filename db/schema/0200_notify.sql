-- Desired schema of the notify bounded context (declarative). See ADR-0006.

CREATE SCHEMA IF NOT EXISTS notify;

-- In-app inbox is the source of truth for notifications.
-- recipient_kind=member: recipient_ref is the member id (inbox + push).
-- recipient_kind=external: recipient_ref is the trusted_contact id (SMS only; no inbox).
-- recipient_kind=staff: recipient_ref is a safety duty shift id (SMS only; no inbox).
CREATE TABLE notify.notifications (
    id               uuid        PRIMARY KEY DEFAULT uuidv7(),
    request_id       uuid        NOT NULL,
    recipient_kind   text        NOT NULL
                                 CHECK (recipient_kind IN ('member', 'external', 'staff')),
    recipient_ref    uuid        NOT NULL,
    template         text        NOT NULL
                                 CHECK (template ~ '^[a-z]+\.[a-z_]+$'),
    params           jsonb       NOT NULL DEFAULT '{}'::jsonb,
    class            text        NOT NULL
                                 CHECK (class IN ('critical', 'normal', 'reminder')),
    created_at       timestamptz NOT NULL DEFAULT now(),
    read_at          timestamptz,
    acknowledged_at  timestamptz
);

CREATE UNIQUE INDEX notifications_request_recipient_uidx
    ON notify.notifications (request_id, recipient_kind, recipient_ref);
CREATE INDEX notifications_inbox_idx
    ON notify.notifications (recipient_ref, created_at DESC, id DESC)
    WHERE recipient_kind = 'member';

-- Delivery attempts on push and SMS channels. Inbox itself needs no delivery row.
CREATE TABLE notify.deliveries (
    id                  uuid        PRIMARY KEY DEFAULT uuidv7(),
    notification_id     uuid        NOT NULL REFERENCES notify.notifications (id),
    channel             text        NOT NULL CHECK (channel IN ('push', 'sms')),
    status              text        NOT NULL
                                    CHECK (status IN ('pending', 'sent', 'failed', 'skipped')),
    attempts            integer     NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    provider_message_id text,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX deliveries_notification_idx ON notify.deliveries (notification_id);
CREATE UNIQUE INDEX deliveries_notification_channel_uidx
    ON notify.deliveries (notification_id, channel);

-- Push devices registered by the member.
CREATE TABLE notify.devices (
    member_id  uuid        NOT NULL,
    device_id  uuid        NOT NULL,
    platform   text        NOT NULL CHECK (platform IN ('ios', 'android')),
    push_token text        NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (member_id, device_id)
);

CREATE INDEX devices_member_idx ON notify.devices (member_id);
