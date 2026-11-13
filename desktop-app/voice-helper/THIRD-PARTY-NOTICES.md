# Third-party notices — dictation

Paladin's dictation bundles these components inside `resources/voice/`:

- **Moonshine Voice** (Moonshine AI) — the English streaming speech-to-text
  models and the `libmoonshine` runtime.
  MIT License. <https://github.com/moonshine-ai/moonshine>
- **ONNX Runtime** (Microsoft) — the inference runtime that `libmoonshine`
  links against.
  MIT License. <https://github.com/microsoft/onnxruntime>

The model files are downloaded by `make voice` from
`https://download.moonshine.ai` and verified against the SHA-256 hashes in
`desktop-app/scripts/voice-models.json`. The runtime archive is fetched from
the Moonshine GitHub release pinned in that file.

Everything runs on the machine; the helper has no network code and opens no
socket.
