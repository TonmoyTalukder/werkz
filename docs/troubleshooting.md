# Troubleshooting

| Problem | Fix |
|---|---|
| *"No provider available"* | Add a key with `/login`, or start Ollama (`ollama serve`) |
| *"rate limited"* on a free model | Free tiers allow only a few requests per minute. Wait a moment, pick another model with `/model`, or turn on [fallback](fallback.md) |
| *"model not found"* or *"unavailable for free"* | That model isn't available to your key (OpenRouter withdraws free models from time to time). Choose one from `/model` |
| *"out of API credits"* | Top up in your provider's console, then run `/login` so werkz uses the provider again. Claude credit is managed at platform.claude.com |
| *"… can't use tools"* | That model can't call tools, so it can't read or change files. Choose a tool-capable model with `/model` for coding work |
| *"fallback is off"* | The model failed and fallback is off (the default). Switch model with `/model`, or choose a mode with `/fallback` |
| werkz takes a few seconds to appear in some terminals | At start-up the UI library asks the terminal for its background colour; terminals that never answer delay it by up to 5 seconds. Wait for the screen before typing |
| Every edit asks, even in auto mode | You're in your home folder, which is never treated as a project. Open a project with `/workspace` or `/add-dir`, or start werkz inside the project |
| *"Couldn't load your settings"* | `~/.config/werkz/config.json` isn't valid JSON. Fix or delete it; werkz won't overwrite it meanwhile |
| The Desktop icon opens as a text file (Linux) | Right-click it → **Allow Launching** |
| *"werkz.app is damaged / can't be opened"* (macOS) | It's the unsigned-app quarantine: right-click → **Open**, or run `xattr -dr com.apple.quarantine /Applications/werkz.app` |
| Selected text isn't copied | On Linux werkz uses `wl-copy`, `xclip` or `xsel` when installed, and otherwise serves the clipboard itself on any desktop with X11 (including GNOME and KDE on Wayland), so it works with nothing installed. Over SSH it uses OSC 52, which some terminals (GNOME Terminal, older xterm) ignore: press **Ctrl+S** and select with the terminal, or use `/copy` and press **w** to write a file |
| `werkz: command not found` after installing | Add `~/.local/bin` to your `PATH` (see [Installation](installation.md)) |
