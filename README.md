# agy-go

Go wrapper for the Google Antigravity CLI.

`CLIClient` keeps a native stream-json `agy` process per session, reusing the same OAuth/keyring credentials as the Antigravity CLI. Call `CloseSession` or `Close` to release processes. Cancellation stops the active process; subsequent turns resume the native conversation.

It implements:

- `ListModels`
- `AuthStatus`
- `Chat`
- plan requests
- native reasoning effort selection
- session-to-conversation persistence

`agy-acp` builds the ACP stdio adapter on top of this package.
