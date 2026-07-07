# agy-go

Go wrapper for the Google Antigravity CLI.

`CLIClient` shells out to `agy`, reusing the same OAuth/keyring credentials as the Antigravity CLI.

It implements:

- `ListModels`
- `AuthStatus`
- `Chat`
- plan requests
- session-to-conversation persistence

`agy-acp` builds the ACP stdio adapter on top of this package.
