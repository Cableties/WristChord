# ug-chords-backend

A small Go HTTP proxy in front of the (unofficial, reverse-engineered)
Ultimate Guitar mobile API, built for a Samsung Galaxy Watch chord-scroller app.

## Why this exists

The watch shouldn't talk to Ultimate Guitar directly:
- weak CPU/battery for scraping logic
- lets you cache results and rate-limit outbound requests
- lets you swap/fix the scraper without touching the watch app

## Endpoints

- `GET /search?q=<query>` → list of matching songs
- `GET /tab/<id>` → structured chord chart for a specific tab ID:

```json
{
  "id": 96835,
  "title": "...",
  "artist": "...",
  "key": "G",
  "capo": 0,
  "lines": [
    { "lyrics": "Swing low, sweet chariot", "chords": [{ "symbol": "G", "offset": 0 }, { "symbol": "C", "offset": 12 }] }
  ]
}
```

This shape is designed so the watch can render chord letters positioned
above the exact character in the lyric line, and scroll smoothly through
`lines` without any client-side parsing.

## One remaining step before this runs

I confirmed the package's auth mechanism (`pkg/ultimateguitar/api.go`) and
that the CLI (`cmd.FetchTab`) wraps a fetch-by-id call, but GitHub blocked
me from browsing the rest of `pkg/ultimateguitar/` to grab the *exact*
method name/signature for fetching a tab by ID and for searching.

To finish wiring `main.go`:

```bash
go get github.com/Pilfer/ultimate-guitar-scraper
go doc github.com/Pilfer/ultimate-guitar-scraper/pkg/ultimateguitar
```

That'll print every exported method on the `Scraper` type. Drop the real
method names into the two `TODO` spots in `main.go` (search for `TODO`),
and paste me the `go doc` output if you want me to wire it up directly —
it's a two-line change once we know the signature.

## Running it

```bash
go mod tidy
go run .
```

Then `curl "http://localhost:8080/tab/96835"` once wired up.

## Notes

- Ultimate Guitar's developer has previously sent takedown notices to
  similar scraper projects, citing publisher licensing deals — keep this
  to personal use, don't stand up a public-facing instance, and add caching
  (already stubbed in, 12h TTL) so you're not hammering their API.
- Deploy anywhere that runs a Go binary: a $5/mo VPS, a Raspberry Pi on
  your home network, or a serverless container (Cloud Run, Fly.io). Since
  the watch app will call this over the internet (not just your home
  wifi), a small always-on host is simplest to start.
