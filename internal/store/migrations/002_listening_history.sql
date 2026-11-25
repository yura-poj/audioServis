CREATE TABLE listening_history (
    user_id uuid NOT NULL,
    id uuid NOT NULL,
    track_id uuid NOT NULL,
    queue_id uuid NOT NULL,
    sequence bigint NOT NULL CHECK (sequence > 0),
    previous_play_id uuid,
    started_at timestamptz NOT NULL,
    listened_ms bigint NOT NULL CHECK (listened_ms >= 0),
    duration_ms bigint CHECK (duration_ms > 0),
    start_reason text NOT NULL CHECK (start_reason IN ('selection','queue','shuffle','skip','automatic')),
    outcome text NOT NULL CHECK (outcome IN ('in_progress','completed','skipped','stopped','error','interrupted')),
    PRIMARY KEY (user_id, id),
    CONSTRAINT listening_history_queue_position UNIQUE (user_id, queue_id, sequence),
    FOREIGN KEY (user_id, track_id) REFERENCES tracks (user_id, id) ON DELETE CASCADE
);
CREATE INDEX listening_history_chronology ON listening_history (user_id, started_at DESC, id DESC);
CREATE INDEX listening_history_track ON listening_history (user_id, track_id);
