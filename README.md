[![Build Status](https://github.com/abzcoding/hget/actions/workflows/build.yml/badge.svg)](https://github.com/abzcoding/hget/actions/workflows/build.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/abzcoding/hget)](https://goreportcard.com/report/github.com/abzcoding/hget)
[![Maintainability](https://api.codeclimate.com/v1/badges/936e2aacab5946478295/maintainability)](https://codeclimate.com/github/abzcoding/hget/maintainability)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE.txt)

# hget

> Fast, multi-connection HTTP downloader with a live [Bubble Tea](https://github.com/charmbracelet/bubbletea) TUI

## Features

- **Parallel downloading** — splits files into chunks and downloads them simultaneously over multiple connections
- **Retro modem-themed TUI** — vintage data-link terminal aesthetic with animated dial-up handshake, blinking status LEDs (PWR/CD/TX/RX/OH/AA), per-channel progress bars, and aggregate signal meter. Animated file assembly and GPG verification phases. All driven by [Bubble Tea](https://github.com/charmbracelet/bubbletea), [Bubbles](https://github.com/charmbracelet/bubbles), [Lip Gloss](https://github.com/charmbracelet/lipgloss), and [Harmonica](https://github.com/charmbracelet/harmonica) spring physics
- **Auto-resume detection** — automatically detects partial downloads and prompts to resume without requiring `--resume` flag
- **Real cancellation** — `q` / `Ctrl-C` actually stops the current download (state saved when resumable); `s` in batch mode skips the current item; `Ctrl-C` (or `q` in batch mode) aborts the entire queue. A live "stopping…/skipping…" overlay shows progress while state is drained safely
- **Visual state feedback** — LED patterns signal every state: handshake (TX/RX blink), downloading (all active), stopping (OH blinks amber), skipping (all blink red), complete (all solid), error (OH solid red)
- **Beautiful console logging** — colored, structured output via [charmbracelet/log](https://github.com/charmbracelet/log) when running non-interactively
- **GPG signature verification** — pass `--verify` to automatically fetch the `.sig` file and verify it with GPG; result is shown inline in the TUI completion screen
- **Smart re-download prompt** — if the target file already exists, a styled [Huh](https://github.com/charmbracelet/huh) confirmation form asks before overwriting
- **Interrupt & resume** — `q` / `Ctrl-C` at any point persists byte offsets to `~/.hget/<task>/state.json`; resume automatically on next run or explicitly with `--resume`
- **State reconstruction** — if the state file is missing, reconstructs progress from existing part files
- **Bandwidth limiting** — cap aggregate download speed with `--rate` (e.g. `5MiB`, `500kB`)
- **Proxy support** — HTTP and SOCKS5 proxies via `--proxy`
- **Batch downloads** — supply a file of URLs (one per line) with `--file`; per-item skip + whole-batch abort, distinct `done / skipped / failed / aborted` accounting
- **Server probing** — inspect `Accept-Ranges` and `Content-Length` without downloading via `--probe`
- **TLS skip** — bypass certificate verification with `--skip-tls`

## Install

```bash
go install github.com/abzcoding/hget/cmd/hget@latest
```

Or build from source:

```bash
git clone https://github.com/abzcoding/hget
cd hget
make install        # builds to ./bin/hget and copies to /usr/local/bin
```

## Usage

```bash
# Download a file with 8 parallel connections
hget -n 8 https://example.com/largefile.iso

# If you interrupt (Ctrl-C), just run the same command again
# hget will detect the partial download and ask if you want to resume
hget -n 8 https://example.com/largefile.iso

# Limit bandwidth to 5 MB/s across all connections
hget -n 4 -rate 5MiB https://example.com/largefile.iso

# Resume an interrupted download (by saved task name)
hget --resume largefile.iso

# Resume by original URL (works even without a state file)
hget --resume https://example.com/largefile.iso

# Download all URLs listed in a file (serial)
hget --file urls.txt

# Use a SOCKS5 proxy
hget --proxy "127.0.0.1:1080" https://example.com/file.tar.gz

# Use an HTTP proxy
hget --proxy "http://proxy.corp.com:3128" https://example.com/file.tar.gz

# Probe server capabilities without downloading
hget --probe https://example.com/file.tar.gz

# Skip TLS certificate verification
hget --skip-tls https://self-signed.example.com/file.zip

# Download an Arch Linux ISO and verify its GPG signature
hget --verify https://fastly.mirror.pkgbuild.com/iso/2026.05.01/archlinux-2026.05.01-x86_64.iso
# hget fetches the .iso, then automatically downloads the .iso.sig and runs gpg --verify.
# The TUI completion screen shows Valid (or Invalid) inline.

# Real-world example: Ubuntu ISO with 16 threads, capped at 10 MiB/s
hget -n 16 -rate 10MiB "https://releases.ubuntu.com/24.04/ubuntu-24.04-live-server-amd64.iso"
```

## Flags

| Flag | Default | Description |
|---|---|---|
| `-n int` | #CPUs | Number of parallel connections |
| `--verify` | `false` | Download `.sig` file and GPG-verify after download |
| `--skip-tls` | `false` | Skip TLS certificate verification |
| `--proxy string` | — | HTTP (`http://…`) or SOCKS5 (`host:port`) proxy |
| `--file string` | — | Path to file with one URL per line |
| `--rate string` | — | Bandwidth cap (e.g. `10kB`, `5MiB`, `1GiB`) |
| `--resume string` | — | Resume by task name or original URL |
| `--probe string` | — | Probe URL for range/content-length, then exit |
| `--timeout duration` | `15s` | Timeout waiting for response headers |

## How It Works

1. **Handshake** — animated modem connection sequence shows 4 phases: DIALING → CARRIER DETECT → HANDSHAKE → LINK ESTABLISHED
2. **Probe** — `HEAD` request (falls back to `GET bytes=0-0`) detects `Accept-Ranges` and `Content-Length`
3. **Resume check** — if partial download exists, prompts "Resume from where you left off? [Y/n]"
4. **Exists check** — if the destination file is already present, a [Huh](https://github.com/charmbracelet/huh) confirmation form asks whether to overwrite (skipped in non-TTY mode)
5. **Split** — file is divided into *n* equal byte ranges; each range is stored as `~/.hget/<task>/<task>.part000000` … `.partN`
6. **Download** — data-link panel shows per-channel progress with blinking activity LEDs; goroutines download each part concurrently; a shared `rate.Limiter` enforces the bandwidth cap
7. **Join** — animated assembly visualization shows parts being merged; files are sorted lexicographically and concatenated into the final file
8. **Verify** *(optional, `--verify`)* — animated key/lock visualization while `.sig` file is fetched and `gpg --verify` runs; result shown on completion screen
9. **Cleanup** — `~/.hget/<task>/` is removed

On **Ctrl-C** or **q**, the data-link panel shows STOPPING status with blinking OH LED while byte offsets are persisted to `~/.hget/<task>/state.json`. On **s** (skip in batch mode), all LEDs blink red while the download is discarded.

## Project Structure

```
hget/
├── cmd/hget/           # CLI entrypoint (main.go) and e2e tests
├── internal/
│   ├── downloader/     # HTTP client, part calculation, state reconstruction
│   ├── joiner/         # Part file assembly
│   ├── state/          # State persistence, resume, task listing
│   ├── ui/             # Bubble Tea TUI + charmbracelet/log console fallback
│   └── util/           # Shared pure utilities
├── Makefile
└── .goreleaser.yml
```

## Cleanup

Part files and state accumulate in `~/.hget/`. To nuke everything:

```bash
rm -rf ~/.hget
```

## Dependencies

| Package | Purpose |
|---|---|
| [charmbracelet/bubbletea](https://github.com/charmbracelet/bubbletea) | TUI framework |
| [charmbracelet/bubbles](https://github.com/charmbracelet/bubbles) | Progress bars & spinner |
| [charmbracelet/lipgloss](https://github.com/charmbracelet/lipgloss) | Styles & colors |
| [charmbracelet/harmonica](https://github.com/charmbracelet/harmonica) | Spring-physics animations |
| [charmbracelet/log](https://github.com/charmbracelet/log) | Structured console logging |
| [charmbracelet/huh](https://github.com/charmbracelet/huh) | Interactive confirmation prompts |
| [alecthomas/units](https://github.com/alecthomas/units) | Bandwidth string parsing |
| [fujiwara/shapeio](https://github.com/fujiwara/shapeio) | Per-reader rate limiting |
| [imkira/go-task](https://github.com/imkira/go-task) | Serial task groups |
| [golang.org/x/net/proxy](https://pkg.go.dev/golang.org/x/net/proxy) | SOCKS5 dialer |
| [golang.org/x/time/rate](https://pkg.go.dev/golang.org/x/time/rate) | Token-bucket rate limiter |

## License

[MIT](LICENSE.txt) — Contributions welcome.
