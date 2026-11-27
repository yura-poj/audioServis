"""Discogs-EffNet CPU worker. stdin and stdout carry newline-delimited JSON."""
import base64
import gc
import json
import sys
import tempfile
from collections import deque

import numpy as np
import onnxruntime as ort
import essentia
import essentia.streaming as streaming
from essentia.standard import TensorflowInputMusiCNN


def decode_mono(path, target):
    loader = streaming.MonoLoader(filename=path, sampleRate=16000, resampleQuality=4)
    sink = streaming.FileOutput(filename=target, mode="binary")
    loader.audio >> sink
    essentia.run(loader)


def frames(path):
    # Essentia decodes/resamples incrementally. Temporary PCM bounds RAM even for long MP3s.
    with tempfile.NamedTemporaryFile() as pcm:
        decode_mono(path, pcm.name)
        # Essentia connectors retain each other. Destroy the file sink to flush its last block.
        gc.collect()
        tail = np.zeros(256, dtype=np.float32)
        while True:
            block = np.fromfile(pcm, dtype=np.float32, count=65536)
            if not len(block):
                break
            tail = np.concatenate((tail, block))
            pos = 0
            while pos + 512 <= len(tail):
                yield tail[pos:pos + 512]
                pos += 256
            tail = tail[pos:]
        while len(tail):
            yield np.pad(tail, (0, 512 - len(tail)))
            tail = tail[256:]


def embed(path, session):
    bands = TensorflowInputMusiCNN()
    window = deque()
    batch = np.zeros((64, 128, 96), dtype=np.float32)
    summed = np.zeros(1280, dtype=np.float64)
    used = count = 0
    for frame in frames(path):
        window.append(bands(frame))
        if len(window) < 128:
            continue
        batch[used] = np.asarray(window)
        used += 1
        count += 1
        for _ in range(62):
            window.popleft()
        if used == 64:
            summed += session.run(["embeddings"], {"melspectrogram": batch})[0].sum(axis=0, dtype=np.float64)
            used = 0
    if not count:
        batch[0, :len(window)] = np.asarray(window)
        used = 1
    if used:
        vectors = session.run(["embeddings"], {"melspectrogram": batch})[0]
        summed += vectors[:used].sum(axis=0, dtype=np.float64)
    norm = np.linalg.norm(summed)
    if not np.isfinite(norm) or not norm:
        raise ValueError("invalid embedding")
    return base64.b64encode((summed / norm).astype("<f4").tobytes()).decode()


if __name__ == "__main__":
    opts = ort.SessionOptions()
    opts.intra_op_num_threads = opts.inter_op_num_threads = 1
    model = ort.InferenceSession(sys.argv[1], opts, providers=["CPUExecutionProvider"])
    if model.get_inputs()[0].shape != [64, 128, 96] or model.get_outputs()[0].shape != [64, 1280]:
        raise ValueError("unexpected Discogs-EffNet model signature")
    print("true", flush=True)
    for line in sys.stdin:
        try:
            reply = {"vector": embed(json.loads(line), model)}
        except Exception as exc:
            reply = {"error": str(exc)}
        print(json.dumps(reply), flush=True)
