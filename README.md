# agy-go

Go wrapper for Google Antigravity agent backends.

## Backends

- `CLIClient`: shells out to `agy`, reusing the same OAuth/keyring credentials as the Antigravity CLI.
- `SDKClient`: shells out to Python and the official `google-antigravity` SDK, using `GEMINI_API_KEY`.

Both implement:

- `ListModels`
- `AuthStatus`
- `Chat`
- plan requests
- session-to-conversation persistence

`agy-acp` builds the ACP stdio adapter on top of this package.
