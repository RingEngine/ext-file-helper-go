# ext-file Compatible Helper (Go)

This is a Windows native messaging host that is compatible with the existing
[`ichaoX/ext-file`](https://github.com/ichaoX/ext-file) Firefox extension.

Goals:

- keep the original Firefox add-on unchanged
- replace the original Python/PyInstaller helper with a smaller native binary
- preserve the native messaging host name: `webext.fsa.app`

Current build target:

- Windows x64

Build output:

- `dist\webext-fsa-app.exe`
- `dist\ext-file-helper-go-<version>.msi`

Build defaults:

- release builds use `-trimpath -ldflags "-s -w"` to reduce binary size

Install:

- run `install-helper.ps1`
- Firefox will then resolve `webext.fsa.app` to this binary
- restart Firefox after installation

MSI packaging:

- run `build-msi.ps1`
- this builds a per-user MSI that installs into `%LOCALAPPDATA%\ExtFileHelperGo`
- the MSI writes the Firefox native messaging registry key under `HKCU`
- no manual registry step is required for end users
- the build script bootstraps WiX CLI automatically by downloading the official `wix-cli-x64.msi`

GitHub Actions:

- `webext-fsa-app.exe` can be cross-compiled on Linux/macOS runners with Go
- `.msi` should be built on a Windows runner because the packaging step uses WiX
- pushing a `v*` tag builds `exe` and `msi`
- GitHub Release publishes only the `.msi` installer to avoid download confusion
- the raw `exe` remains available in Actions artifacts for debugging and internal use

The install script registers the following Firefox extension ids:

- `file@example.com`
- `framework@example.com`

What the install script does:

- writes the native host manifest to `%LOCALAPPDATA%\ExtFileHelperGo\webext.fsa.app.json`
- registers `HKCU\Software\Mozilla\NativeMessagingHosts\webext.fsa.app`
- points Firefox native messaging for `webext.fsa.app` to `dist\webext-fsa-app.exe`

How it runs:

- this helper is not a resident background service
- Firefox launches it on demand through native messaging when the extension needs file or folder access
