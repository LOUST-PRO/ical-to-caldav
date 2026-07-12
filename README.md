# ical-to-caldav

**iCal-to-CalDAV bridge daemon.** Converts public iCal (ICS) subscription URLs into a CalDAV
server, so calendar clients like [DankCalendar](https://github.com/AvengeMedia/DankCalendar),
[khal](https://github.com/pimutils/khal), Evolution, and Thunderbird/Lightning can consume
any public .ics feed without proprietary API locks.

Apache2 licensed.

## Why

Many calendar providers only offer **iCal subscription URLs** (Google Calendar free/busy,
academic calendars, government ICS feeds) but no CalDAV. Desktop clients prefer CalDAV for
bidirectional sync. This daemon bridges that gap:

```
iCal URL (Google free/busy, academic .ics, etc.)
        │
        ▼
  ical-to-caldav
  (fetches + parses + caches)
        │
        ▼
CalDAV server (:8080)
        │
        ▼
DankCalendar / khal / Evolution / Thunderbird
```

## Quick start

```bash
# Build
make build

# Configure
cp ical-to-caldav.yaml.example ical-to-caldav.yaml
$EDITOR ical-to-caldav.yaml

# Run
./icaltocaldav --config ical-to-caldav.yaml

# In DankCalendar: dcal account add caldav
#   URL: http://localhost:8080/caldav/sources/<source-id>/
```

## Config example

```yaml
listen: ":8080"
data_dir: "~/.local/share/ical-to-caldav"
refresh_interval: "30m"

sources:
  - id: google-freebusy
    name: "Google Free/Busy"
    url: "https://calendar.google.com/calendar/ical/.../public/basic.ics"
    color: "#1a73e8"
  - id: university
    name: "University Calendar"
    url: "https://example.edu/calendar.ics"
    color: "#34a853"
    refresh_interval: "1h"
```

## Tech stack

| Layer | Choice |
|---|---|
| Language | Go 1.23+ |
| HTTP | chi |
| iCal parsing | github.com/jszwec/ics |
| Cache | Badger (embedded KV) |
| License | Apache 2.0 |

## Architecture

```
ical-to-caldav/
├── cmd/icaltocaldav/     # main entrypoint
├── internal/
│   ├── source/          # fetcher + iCal parser
│   ├── caldav/          # CalDAV HTTP server (RFC 4791)
│   ├── store/           # Badger persistence
│   └── config/          # YAML config
└── Makefile
```

## CalDAV compliance

Implements the subset of [RFC 4791](https://www.rfc-editor.org/rfc/rfc4791) needed for
read-only calendar access:

- `PROPFIND` on calendar-home-set and calendar collections
- `REPORT` calendar-query with time-range filter
- `GET` on calendar object resources (`.ics` files)

Write operations (`PUT`, `DELETE`) are out of scope for v1 — the daemon is a
read-only bridge.

## Contributing

Issues and PRs welcome. Follow the LOUST contribution standards.

## Related

- [DankMaterialShell](https://github.com/AvengeMedia/DankMaterialShell) — desktop shell
- [DankCalendar](https://github.com/AvengeMedia/DankCalendar) — calendar daemon + QML UI
- [khal](https://github.com/pimutils/khal) — CLI calendar client (CalDAV/iCal)
- [vdirsyncer](https://github.com/pimutils/vdirsyncer) — CalDAV/iCal sync
