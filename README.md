# OneBoard Safe Paste

**Protect sensitive information before it leaves your clipboard.**

OneBoard Safe Paste is a Windows local-first privacy tool that redacts sensitive
information before text is pasted into AI assistants, support portals, tickets,
chat systems, email, or web forms.

Processing is performed locally. No account is required for core local
functionality, and no cloud scrubbing service is required.

## Features

- Built-in detection for common credentials, secrets, and personally identifiable information
- Forensic presets and a configurable rules library
- Custom regular-expression rules
- Ghost Mode for background clipboard monitoring and redaction
- Satellite Mode, Auto-Copy, timed clipboard wipe, and Debug Mode
- Local processing with no telemetry or cloud redaction dependency

Redaction is pattern-based and may not detect every sensitive value. Review the
sanitized result before sharing it.

## Build from source

Prerequisites:

- Windows 10 or later with WebView2 installed
- Go 1.24.12 or a compatible Go 1.24+ toolchain
- Node.js 18 or later and npm
- Wails CLI v2.11.0

Install the matching Wails CLI if necessary:

```powershell
go install github.com/wailsapp/wails/v2/cmd/wails@v2.11.0
```

Restore locked dependencies and build:

```powershell
go mod download
Set-Location frontend
npm ci
npm run build
Set-Location ..
wails build -clean
```

The Windows executable is generated at
`build/bin/OneBoardSafePaste.exe`.

## Privacy

Clipboard and workspace text is processed on the local device by the core
redaction engine. OneBoard Safe Paste does not require an account, telemetry,
analytics, an API key, or a cloud redaction service for its core functionality.

The operating system and applications or websites you paste into may have their
own network behavior and privacy policies.

## Upstream / Attribution

OneBoard Safe Paste is derived from
[Silo-Redact](https://github.com/AlexanderMckain/Silo-Redact) by Alexander McKain.
The upstream Git history, original MIT License, and original copyright notice are
preserved. See [UPSTREAM.md](UPSTREAM.md) for the maintenance workflow and
[NOTICE](NOTICE) for attribution details.

## License

Licensed under the MIT License. See [LICENSE](LICENSE).
