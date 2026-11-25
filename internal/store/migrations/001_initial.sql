CREATE TABLE users (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    display_name text NOT NULL CHECK (char_length(display_name) BETWEEN 1 AND 200),
    key_hash bytea NOT NULL UNIQUE CHECK (octet_length(key_hash) = 32)
);

CREATE TABLE tracks (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users (id),
    title text NOT NULL CHECK (char_length(title) BETWEEN 1 AND 200),
    size_bytes bigint NOT NULL CHECK (size_bytes > 0),
    created_at timestamptz NOT NULL DEFAULT date_trunc('second', now()),
    liked boolean NOT NULL DEFAULT false,
    UNIQUE (user_id, id)
);

CREATE TABLE playlists (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users (id),
    name text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 200),
    created_at timestamptz NOT NULL DEFAULT date_trunc('second', now()),
    UNIQUE (user_id, id)
);

CREATE TABLE playlist_tracks (
    user_id uuid NOT NULL,
    playlist_id uuid NOT NULL,
    track_id uuid NOT NULL,
    position bigint NOT NULL CHECK (position > 0),
    PRIMARY KEY (playlist_id, track_id),
    UNIQUE (playlist_id, position),
    FOREIGN KEY (user_id, playlist_id) REFERENCES playlists (user_id, id) ON DELETE CASCADE,
    FOREIGN KEY (user_id, track_id) REFERENCES tracks (user_id, id) ON DELETE CASCADE
);

CREATE INDEX playlist_tracks_track_idx ON playlist_tracks (user_id, track_id);
