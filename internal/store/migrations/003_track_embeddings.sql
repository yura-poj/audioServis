CREATE TABLE track_embeddings (
    track_id uuid PRIMARY KEY REFERENCES tracks(id) ON DELETE CASCADE,
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','ready','failed')),
    vector bytea,
    CHECK ((status = 'ready') = (vector IS NOT NULL)),
    CHECK (octet_length(vector) = 5120)
);
CREATE INDEX track_embeddings_pending ON track_embeddings(track_id) WHERE status = 'pending';
INSERT INTO track_embeddings(track_id) SELECT id FROM tracks;
