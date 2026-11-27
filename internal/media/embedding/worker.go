package embedding

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"time"

	"audioservice/internal/media"
	"audioservice/internal/store"
)

//go:embed analyze.py
var script string

func Run(ctx context.Context, db *store.Store, files *media.Files, wake <-chan struct{}) error {
	model := os.Getenv("AUDIOSERVICE_WAVE_MODEL")
	if model == "" {
		return errors.New("AUDIOSERVICE_WAVE_MODEL is not configured")
	}
	python := os.Getenv("AUDIOSERVICE_WAVE_PYTHON")
	if python == "" {
		python = "python3"
	}
	cmd := exec.CommandContext(ctx, python, "-u", "-c", script, model)
	cmd.Stderr = os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err = cmd.Start(); err != nil {
		return err
	}
	defer func() { stdin.Close(); cmd.Wait() }()
	dec := json.NewDecoder(stdout)
	enc := json.NewEncoder(stdin)
	var ready bool
	if err = dec.Decode(&ready); err != nil || !ready {
		return fmt.Errorf("analyzer startup: %v", err)
	}
	for ctx.Err() == nil {
		id, err := db.PendingEmbedding(ctx)
		if errors.Is(err, store.ErrNotFound) {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-wake:
			case <-time.After(10 * time.Second):
			}
			continue
		}
		if err != nil {
			return err
		}
		var result struct {
			Vector []byte
			Error  string
		}
		file, err := files.Read(id)
		if err == nil {
			path := file.Name()
			file.Close()
			if err = enc.Encode(path); err != nil {
				return err
			}
			if err = dec.Decode(&result); err != nil {
				return err
			}
		} else {
			result.Error = err.Error()
		}
		if result.Error != "" {
			slog.Warn("track analysis failed", "track", id, "error", result.Error)
		} else if len(result.Vector) != 5120 {
			return errors.New("invalid embedding size")
		}
		if err = db.SaveEmbedding(ctx, id, result.Vector); err != nil {
			return err
		}
	}
	return ctx.Err()
}
