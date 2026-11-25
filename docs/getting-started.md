# Getting started

1. **Start werkz in a project folder:**

   ```bash
   cd ~/code/my-app
   werkz
   ```

2. **Connect at least one provider.** The first run opens the key screen. Paste any keys you have, press
   **Ctrl+S** to save and verify them, then **Enter** to start. No keys at all? Install
   [Ollama](https://ollama.com/download), run `ollama pull llama3.2` and press **Esc**.
   See [Providers and models](providers-and-models.md) for where to get keys.

3. **Ask for something:**

   ```text
   ❯ explain how authentication works in this repo
   ❯ add pagination to the /users endpoint and update the tests
   ❯ why does `go test ./...` fail? fix it
   ```

4. **Approve the risky steps.** werkz shows each command, and a preview of each edit as a diff, before
   running it. Choose with **↑/↓** and **Enter**: yes, yes and don't ask again this session, or no
   (also **Esc**: no, and tell werkz what to do instead).

## What a session looks like

```text
❯ the login form accepts empty passwords, fix it and add a test

⏺ Grep(password in src)
  ⎿  3 files
⏺ Read(src/auth/login.ts)
  ⎿  Read 84 lines
⏺ Edit(src/auth/login.ts)
  ⎿  +4 −1 lines
⏺ Bash(npm test -- login)
  ⎿  exit 0
⏺ Empty and whitespace-only passwords are now rejected before the API call, with a test covering both.
```

## Next steps

- `/model` to switch models, `/effort` to set how hard the model thinks: [Providers and models](providers-and-models.md)
- `/workspace` to work across several repositories: [Workspaces](workspaces.md)
- **Shift+Tab** to change the permission mode: [Safety and privacy](safety.md)
- `/help` for every command: [Usage](usage.md)
