# Workspaces

By default there is **no workspace**: werkz works in the folder you start it from. When a task spans several
repositories, open a workspace, which is a named set of folders plus optional environment variables.

- **`/workspace`** opens a picker: No workspace (default), your starred and recent workspaces, and
  **+ New workspace**. In the picker, **Ctrl+S** stars a workspace, **Ctrl+E** edits it and **Ctrl+X**
  (pressed twice) deletes it.
- **`/workspace new`** asks for a name, the folders (Tab completes paths; the first folder is the main one)
  and any environment variables, such as `APP_ENV=dev`.
- **`/workspace <name>`** opens one directly; **`/workspace none`** goes back to no workspace.
- **`/add-dir <path>`** adds a folder on the fly. If a workspace is open, the folder is saved into it.
  **`/dirs`** lists the working folders.

From the command line: `werkz -w my-workspace`, or `werkz ~/code/api ~/code/web` for an unsaved set.

Inside a workspace, the model can read, search and edit in every folder, and `Grep` and `Glob` search all of
them. Shell commands run in the main folder with the workspace's variables. The status bar shows the
workspace, for example `▸ my-app (2)`. Anything outside the workspace folders still asks first
(see [Safety and privacy](safety.md)).
