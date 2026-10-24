-- +goose Up
CREATE TABLE users (
    id uuid PRIMARY KEY,
    state text NOT NULL CHECK (state IN ('active', 'disabled', 'deleting')),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE auth_identities (
    issuer text NOT NULL,
    subject text NOT NULL,
    user_id uuid NOT NULL REFERENCES users(id),
    PRIMARY KEY (issuer, subject),
    UNIQUE (user_id)
);

CREATE TABLE user_roles (
    user_id uuid NOT NULL REFERENCES users(id),
    role text NOT NULL CHECK (role = 'platform_admin'),
    PRIMARY KEY (user_id, role)
);

-- +goose Down
DROP TABLE user_roles;
DROP TABLE auth_identities;
DROP TABLE users;
