# XIASS AdsPower Helper

The helper keeps AdsPower control local while allowing an authenticated XIASS
administrator to open an OpenAI OAuth URL in the account's persistent browser
profile.

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
    "https://api.xiass.com": {
      "environment_key": "api",
      "template_profile_id": "template-profile-id"
    },
    "https://api2.xiass.com": {
      "environment_key": "api2",
      "template_profile_id": "template-profile-id"
    }
  }
}
```

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
