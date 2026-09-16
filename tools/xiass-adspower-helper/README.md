# XIASS AdsPower Helper

The helper keeps AdsPower control local while allowing an authenticated XIASS
administrator to open an OpenAI OAuth URL in the account's persistent browser
profile.

For XIASS workbench tasks, it also connects to AdsPower's loopback CDP endpoint
and completes the same supported OpenAI login steps as the built-in browser:
email, saved password, authenticator code or saved mailbox code, workspace
selection, and the localhost OAuth callback. Login material is delivered only
inside the one-time launch response, remains in helper memory for the active
run, and is cleared when the run finishes.

## Security boundary

- The AdsPower API key and SOCKS credentials stay in the local config file.
- XIASS stores only the AdsPower device/profile IDs, public proxy address, exit
  IP, WebRTC policy, and verification timestamps.
- A XIASS launch ticket is random, short-lived, single-use, and bound to one
  OAuth session and one execution-node environment.
- Existing account bindings cannot silently move to another device or profile.
- New profiles use randomized AdsPower fingerprint defaults, automatic
  IP-based timezone/language, and disabled WebRTC. Chromium is also launched
  with non-proxied WebRTC UDP disabled.

## Install and configure

The XIASS workbench links to the fixed `adspower-helper-latest` downloads for
macOS and Windows. Start AdsPower first, then start the helper and open:

```text
http://127.0.0.1:34987/setup
```

The local setup page accepts the AdsPower Local API key and one SOCKS5 route
for each XIASS server/execution-node environment. It checks the AdsPower API
and the real SOCKS5 exit IP before saving. A missing config file is created
automatically on first launch; administrators do not need to create JSON by
hand.

The fixed release assets are:

```text
xiass-adspower-helper-macos-universal.zip
xiass-adspower-helper-macos-universal.dmg
xiass-adspower-helper-windows-x64.exe
```

The macOS app installs a per-user LaunchAgent. It starts automatically after
login, restarts after a crash, and uses a macOS idle-sleep assertion so the
display may turn off while authorization jobs continue in the background.
Closing the Mac lid or shutting the computer down still pauses the local
AdsPower runtime; the helper resumes automatically after the next login/wake.

## Config

The default macOS config path is:

```text
~/Library/Application Support/XIASS/adspower-helper/config.json
```

Example without secrets:

```json
{
  "listen_address": "127.0.0.1:34987",
  "callback_address": "127.0.0.1:1455",
  "adspower_base_url": "http://local.adspower.net:50325",
  "device_id": "random-device-id",
  "servers": {
    "https://api.example.com": {
      "environment_key": "api",
      "proxy_host": "api.example.com",
      "proxy_port": "1104",
      "proxy_user": "local-user",
      "proxy_password": "local-password"
    },
    "https://api2.example.com": {
      "environment_key": "api2",
      "proxy_host": "api2.example.com",
      "proxy_port": "1104",
      "proxy_user": "local-user",
      "proxy_password": "local-password"
    }
  }
}
```

Existing installations may continue using `template_profile_id`. New
installations should use the local setup page and direct SOCKS5 fields; the
helper creates each account profile with that route automatically.

When AdsPower API security verification is enabled, place the key in
`api_key` and keep the file mode at `0600`, or provide `ADSPOWER_API_KEY` to
the process environment. Never add the real config to the repository.

## Commands

```bash
go run . doctor
go run . serve
./install-macos.sh
```

`doctor` validates AdsPower itself and makes a real request through every
configured SOCKS template. `serve` exposes only loopback HTTP endpoints. For a
task launched by the XIASS workbench, the callback listener returns the full
localhost callback URL through that task's short-lived one-time token. It never
uploads AdsPower credentials, proxy credentials, browser cookies, or OAuth
tokens. A callback without a matching task remains available for manual copy.
