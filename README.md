# arlo-term

Watch your Arlo cameras from the terminal. Streams come from Arlo's cloud RTSP endpoint and play in mpv. Installs as `arlo-term` with `arlo` as an alias.

## Requirements

- macOS or Linux
- mpv and ffmpeg (`brew install mpv ffmpeg`)

## Install

```sh
go build .
./arlo-term install          # copies to /usr/local/bin and links arlo
./arlo-term install ~/bin    # or any directory in your PATH
```

## Use

```sh
arlo login       # sign in once (email, password, then approve on your phone)
arlo             # open the camera panel
arlo uninstall   # remove the binary, the alias and everything it stored
```

In the panel, `tab` switches between **Windows** (one mpv window per camera) and **Mosaic** (all cameras in one window), `r` reconnects to Arlo and `q` quits. Each camera keeps a single connection to Arlo, relayed locally, so switching modes is instant and never reconnects. Streams reconnect in place when Arlo closes them; cameras that keep failing are marked offline and retry every 30 seconds.

## Files

arlo-term keeps a single directory, removed by `arlo uninstall`:

- macOS: `~/Library/Application Support/arlo-term`
- Linux: `~/.config/arlo-term`

It holds `auth.json` (email, password and session token, readable only by you) and, when `ARLO_DEBUG=1` is set, `debug.log` with stream tokens redacted. Arlo sessions expire after a few hours; arlo-term then signs in again on its own with the saved password, without phone approval.
