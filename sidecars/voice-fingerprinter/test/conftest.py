"""Test fixtures for the voice-fingerprinter sidecar.

Installs lightweight stand-ins for `torch`, `torchaudio` and
`speechbrain.inference.speaker` into sys.modules *before* app.py is
imported, so the test suite never imports real torch/speechbrain, never
touches the network, and never downloads the ECAPA model. The stubs
implement just enough surface area (unsqueeze/squeeze/tolist, a real
linear resample, a deterministic fake encoder) to exercise app.py's own
logic — decode, downmix, resample, error handling — faithfully.

numpy is a real dependency here (small, no model weights) since app.py's
decode path is written against a real ndarray API.
"""
from __future__ import annotations

import sys
import types

import numpy as np


class _FakeTensor:
    """Minimal stand-in for a torch.Tensor: wraps a numpy array and
    supports the handful of ops app.py actually calls."""

    def __init__(self, array: np.ndarray):
        self.array = np.asarray(array, dtype=np.float32)

    def unsqueeze(self, dim):
        return _FakeTensor(np.expand_dims(self.array, dim))

    def squeeze(self, dim=None):
        if dim is None:
            return _FakeTensor(np.squeeze(self.array))
        return _FakeTensor(np.squeeze(self.array, axis=dim))

    def numpy(self):
        return self.array

    def detach(self):
        return self

    def cpu(self):
        return self

    def tolist(self):
        return self.array.tolist()

    @property
    def shape(self):
        return self.array.shape


def _install_fake_torch():
    torch_mod = types.ModuleType("torch")
    torch_mod.Tensor = _FakeTensor

    def from_numpy(arr):
        return _FakeTensor(arr)

    def zeros(shape):
        return _FakeTensor(np.zeros(shape, dtype=np.float32))

    def set_num_threads(n):
        return None

    class _Cuda:
        @staticmethod
        def is_available():
            return False

    torch_mod.from_numpy = from_numpy
    torch_mod.zeros = zeros
    torch_mod.set_num_threads = set_num_threads
    torch_mod.cuda = _Cuda()
    sys.modules["torch"] = torch_mod

    torchaudio_mod = types.ModuleType("torchaudio")
    functional_mod = types.ModuleType("torchaudio.functional")

    def resample(tensor: _FakeTensor, orig_freq: int, new_freq: int) -> _FakeTensor:
        arr = tensor.array
        if orig_freq == new_freq:
            return _FakeTensor(arr)
        # Real (if simple) linear-interpolation resample — good enough to
        # verify app.py actually calls resample with the right args and
        # produces the right output length, without pulling in torchaudio.
        n_in = arr.shape[-1]
        n_out = max(1, round(n_in * new_freq / orig_freq))
        x_old = np.linspace(0.0, 1.0, num=n_in, endpoint=False)
        x_new = np.linspace(0.0, 1.0, num=n_out, endpoint=False)
        resampled = np.interp(x_new, x_old, arr[0]).astype(np.float32)
        return _FakeTensor(resampled[np.newaxis, :])

    functional_mod.resample = resample
    torchaudio_mod.functional = functional_mod
    sys.modules["torchaudio"] = torchaudio_mod
    sys.modules["torchaudio.functional"] = functional_mod


class FakeEncoderClassifier:
    """Deterministic fake ECAPA encoder: returns a fixed-dimension vector
    derived from simple stats of the input so different audio produces
    different (but stable, comparable-within-a-test) embeddings."""

    DIM = 192
    last_loaded_source = None
    last_savedir = None
    from_hparams_calls = 0

    def __init__(self):
        pass

    @classmethod
    def from_hparams(cls, source, savedir, run_opts=None):
        cls.from_hparams_calls += 1
        cls.last_loaded_source = source
        cls.last_savedir = savedir
        return cls()

    def encode_batch(self, tensor: _FakeTensor) -> _FakeTensor:
        arr = tensor.array
        flat = arr.reshape(-1).astype(np.float64)
        seed = float(np.sum(flat[: min(len(flat), 4000)]))
        rng = np.random.default_rng(abs(int(seed * 1000)) % (2**31) or 1)
        vec = rng.normal(size=(1, 1, self.DIM)).astype(np.float32)
        return _FakeTensor(vec)


def _install_fake_speechbrain():
    speechbrain_mod = types.ModuleType("speechbrain")
    inference_mod = types.ModuleType("speechbrain.inference")
    speaker_mod = types.ModuleType("speechbrain.inference.speaker")
    speaker_mod.EncoderClassifier = FakeEncoderClassifier
    inference_mod.speaker = speaker_mod
    speechbrain_mod.inference = inference_mod
    sys.modules["speechbrain"] = speechbrain_mod
    sys.modules["speechbrain.inference"] = inference_mod
    sys.modules["speechbrain.inference.speaker"] = speaker_mod


_install_fake_torch()
_install_fake_speechbrain()
