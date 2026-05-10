# Calendarr

Calendarr syncs upcoming movie and TV release dates from Radarr and Sonarr into Google Calendar. It runs as a Windows service, includes a local web dashboard, and keeps your calendar updated with theater releases, digital releases, and upcoming episodes.

I originally made a small Python script years ago to solve this for my own setup. Over time I tuned it, tweaked it, and kept adding small fixes as my media automation stack changed. With the tools available today, I was able to turn that old script into a full Windows app that I hope can help the wider media automation community.

## Features

- Sync Radarr movie theater release dates to Google Calendar.
- Sync Radarr digital or physical release dates to Google Calendar.
- Sync Sonarr upcoming episodes to Google Calendar.
- Runs quietly as a Windows service.
- Local web UI for settings, logs, history, ignored shows, preview changes, cleanup, and updates.
- Google OAuth support with selectable calendars.
- Optional Pushover notifications for added, updated, deleted, failed sync, or available update events.
- Custom event title templates for movies and episodes.
- Ignored shows list with automatic cleanup of unwanted events.
- Built-in update checker for GitHub releases.

## Requirements

- Windows
- Radarr and/or Sonarr
- Google account with Calendar access
- API keys from Radarr and Sonarr
- Optional: Pushover account for mobile notifications

## Installation

Download the latest installer from the GitHub Releases page and run it on the machine that will host Calendarr.

After installation, open the local web UI:

```text
http://localhost:5000
```

The port can be changed later in Settings.

By default the web UI listens only on the local machine. If you enable Local network access, Calendarr still serves the UI over plain HTTP unless you place it behind your own HTTPS reverse proxy or VPN such as Tailscale. Use Local network access only on networks you trust.

## First Setup

1. Open Calendarr in a browser on the server machine.
2. Enter your Radarr and Sonarr API URLs and API keys.
3. Connect Google Calendar from the Settings page.
4. Pick the calendar Calendarr should write to.
5. Adjust sync interval, event templates, notifications, and cleanup options.
6. Use Preview Changes to confirm what will be added before running a sync.

Default API URLs are:

```text
Radarr: http://localhost:7878/api/v3
Sonarr: http://localhost:8989/api/v3
```

## Google Calendar Notes

Calendarr uses Google OAuth and stores a refresh token locally in its config file. It requests calendar event access so it can create, update, and delete the events it manages.

Google Calendar connection must be started from a browser on the Calendarr server itself. Google redirects back to `http://localhost:<port>/oauth/callback`; if you start the flow from another computer on the LAN, that other computer's localhost will not be Calendarr.

If a release changes dates in Radarr or Sonarr, the next sync should move the existing Google Calendar event back to the correct date.

## Running as a Service

Calendarr is designed to run as a Windows service. The installer handles normal service setup. Advanced users can install or uninstall the service manually:

```powershell
calendarr.exe --install --data "C:\ProgramData\Calendarr"
calendarr.exe --uninstall
```

## Data and Logs

Runtime data is stored in the configured data directory. A normal install uses:

```text
C:\ProgramData\Calendarr
```

Important files include:

- `config.json` for settings and tokens
- `history.json` for recent sync history
- `ignored_shows.json` for ignored Sonarr shows
- `logs/sync-YYYY-MM-DD.log` for daily logs

Do not share files that contain API keys, Google tokens, or personal calendar data.

The Windows installer currently leaves the data folder broadly accessible to local Windows users. Treat any Windows account on the same machine as able to read or change Calendarr settings until the deferred installer permission change is completed.

## Updating

Calendarr can check GitHub Releases for updates from the About page. The updater expects these release assets:

```text
calendarr.exe
calendarr.exe.sha256
calendarr.exe.sig
```

The checksum file must match the exact `calendarr.exe` uploaded to the same release, and the signature file must verify against the public key embedded in the app. If the checksum or signature is missing or invalid, the in-app updater will stop before installing.

## Building from Source

Calendarr is written in Go.

Debug build:

```powershell
go build -trimpath -o calendarr.exe .
```

Release build:

```powershell
.\build_release.ps1
```

The release build script reads the local ignored `release_secret.txt`, injects it only for the build, restores the public-safe placeholder in source, and generates the updater checksum and signature. Create `release_secret.txt` locally with the Google OAuth client secret before building releases. Create `release_signing_private_key.txt` once with `.\build_release.ps1 -GenerateSigningKey`, then back it up outside the repo. Do not commit either file.

Installer release build:

```powershell
.\build_release.ps1 -BuildInstaller
```

The installer build uses Inno Setup and produces `calendarr-setup-<version>.exe`. It includes the Calendarr icon, branded setup artwork, and an optional desktop shortcut checkbox. Normal in-app updater releases need `calendarr.exe`, `calendarr.exe.sha256`, and `calendarr.exe.sig`.

For development builds that do not need embedded OAuth:

```powershell
go build -trimpath -o calendarr.exe .
```

If the app icon artwork changes, regenerate the Windows resource file before building:

```powershell
go run github.com/akavel/rsrc@latest -ico installer\calendarr.ico -o rsrc_windows.syso
```

## Contributing

Bug reports and feature requests are welcome through GitHub Issues. Please include:

- Calendarr version
- Windows version
- Radarr or Sonarr version if relevant
- A clear description of what happened
- Relevant log lines with secrets removed

Pull requests should keep changes focused, include testing notes, and avoid committing personal config, logs, tokens, or built release files.

## Project Status

Calendarr is currently focused on Windows service support. Planned ideas include multi-calendar improvements, deeper event customization, a first-time setup wizard, and Linux support later.

## Support

If Calendarr saves you time and you want to say thanks, a coffee is always appreciated: [paypal.me/jefabell](https://paypal.me/jefabell)

## License

Calendarr is released under the MIT License. See [LICENSE](LICENSE) for details.
