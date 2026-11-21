# Implementation Plan — v0

A work order for the first implementation of `linkding-wishlist-ssg`. This
document resolves every interface decision the [spec](SPEC.md) deliberately left
open. Where the spec explains *why*, this file says *what*.

**Read `docs/SPEC.md` first.** This document does not restate the reasoning, and
several rules here only make sense against the spec's §7 (resilience) and §4
(data model).

---

## 1. Scope

### v0 delivers

- Read bookmarks tagged `wishlist` from a Linkding instance.
- Map each bookmark to one item; derive a stable ID and a store key per item.
- Parse optional Tier 2 overrides from the notes.
- Render a static site: index, one page per store collection, one page per facet.
- Emit the JSON manifest (§7).
- Run the lint pass and print its report to stdout.

### v0 explicitly does not deliver

Nothing below may be stubbed, scaffolded, or "prepared for":

- Fetching or scraping the target pages of bookmarks. **v0 makes no HTTP requests
  except to the Linkding API.**
- Prices, availability, price history, SQLite, or any observation store.
- Images, thumbnails, or OpenGraph metadata.
- Pagefind, client-side search, or any full-text index.
- Gift claims, the progressive-enhancement overlay, or any KV service.
- A configuration file. Flags and environment variables only.
- Multi-retailer wants (`wishlist:want:<slug>`), per spec §5.5.

**Rationale for the scraping cut:** SPEC.md §7.1 requires rendering to fail open
and treats a missing price as a normal state. A site with no prices anywhere is
therefore a *complete* application, not a partial one. Building it first isolates
every load-bearing decision (identity, tiers, clustering, fail-open, manifest)
from the scraper's unresolved questions (politeness, retries, hostile sites).

It also removes the fragment test (SPEC §4.7.1) as a blocker: v0 keys on the URL
exactly as Linkding stores it, so fragments work if they survive and the
mechanism is simply unused if they do not.

---

## 2. Repository layout

Go, module `github.com/lmorchard/linkding-wishlist-ssg`. Go 1.22+.

```
cmd/linkding-wishlist-ssg/main.go   CLI entry, flag parsing, exit codes
internal/linkding/                  API client — copied from linkding-ssg, then diverged
internal/model/                     Item, Store, Facet types; bookmark → item mapping
internal/identity/                  ID derivation and store-key derivation
internal/tier2/                     fenced-block parser for note overrides
internal/cluster/                   grouping into stores and facets
internal/lint/                      heuristics and reason codes
internal/render/                    html/template rendering, embedded templates + CSS
internal/manifest/                  JSON emission
testdata/                           fixture API responses
docs/
```

Templates and CSS live under `internal/render/assets/` and are embedded with
`go:embed`. No template files required at runtime.

### Copying from `linkding-ssg`

Copy the API client and bookmark model, then **delete** everything else: Pagefind
integration, the archive renderer, the thumbnail gutter, and any tag-page or
pagination logic that does not serve this spec. A fork that keeps the parent's
features "just in case" is the overloading failure mode described in SPEC §3.2.

Do not add a `replace` directive or reference the parent module. This repository
is standalone from the first commit.

---

## 3. CLI

Single command, single subcommand-free invocation:

```
linkding-wishlist-ssg build [flags]
```

### Flags

| Flag | Default | Meaning |
|---|---|---|
| `--out` | `./build` | Output directory; created if absent, wiped if present |
| `--tag` | `wishlist` | Root selector tag |
| `--base-url` | `/` | Prefix for absolute links in the manifest |
| `--include-archived` | `false` | Include archived bookmarks (debugging only) |
| `--lint-format` | `text` | `text` or `json`; both go to stdout |
| `--no-lint` | `false` | Suppress the lint report |

### Credentials

From the environment, matching the convention already used by the
`linkding-ingest` skill:

- `LINKDING_URL` — base URL of the instance, no trailing slash
- `LINKDING_TOKEN` — API token

Missing either is a fatal error with a clear message. Never accept a token as a
flag; it would land in shell history and process listings.

### Streams and exit codes

- **stdout** — progress lines, the lint report, and the final summary.
- **stderr** — warnings (malformed Tier 2 blocks, unknown keys) and fatal errors.
- **Exit 0** — build succeeded. This includes builds where *every* item looks
  wrong to the linter. Odd data is not failure.
- **Exit 1** — the Linkding API was unreachable, rejected credentials, or returned
  unusable data. Only the data *source* failing is fatal.

---

## 4. Identity

### 4.1 Item ID

```
id = hex(sha256(raw_url))[0:12]
```

where `raw_url` is the `url` field **byte-for-byte as returned by the Linkding
API**. No normalization whatsoever — not host case, not trailing slashes, not
query ordering, not percent-encoding, not fragments.

**Why no normalization:** the ID must be 1:1 with Linkding's own duplicate key. If
two bookmark URLs that Linkding considers distinct ever collapsed to one ID, the
manifest would silently lose an item and the future claims layer would key two
wants to one claim. Mirroring the source's identity rule is the only way to
guarantee that cannot happen.

Consequence: if Linkding normalizes a URL on save, this function sees the
normalized form and hashes that. Correct by construction.

**Collision guard:** if two distinct raw URLs produce the same 12-hex-character
ID, abort with exit 1 and print both URLs. This will not happen at this scale, but
a silent collision is exactly the failure mode §7.1 exists to prevent.

### 4.2 Store key

Separately derived, and it *does* normalize:

1. Parse `raw_url`. On parse failure, store key is the literal string `unknown`
   and the item gets lint code `unparseable_url`.
2. Take the hostname, lowercase it.
3. Strip a single leading `www.`.
4. Strip any port.

Result is used both for grouping and for the output path `/store/<key>/`.

Examples:

| URL | Store key |
|---|---|
| `https://www.sevensisterspdx.com/` | `sevensisterspdx.com` |
| `https://Sevensisterspdx.com/shop` | `sevensisterspdx.com` |
| `https://bookshop.org/books/foo` | `bookshop.org` |
| `http://localhost:8080/x` | `localhost` |

### 4.3 Store label

The display name for a collection, in precedence order:

1. The `store` value from any item's Tier 2 block in that group. **First item in
   build order wins**; build order is defined in §8.1.
2. Otherwise, the store key itself.

No other source. There is no store directory file, per SPEC §5.4.

---

## 5. Tier 2 overrides

### 5.1 Syntax

A fenced code block in the bookmark's **notes**, with the info string exactly
`wishlist` (case-insensitive):

````
```wishlist
store: Seven Sisters
price: 42.00
currency: USD
priority: 1
variant: small, green
image: https://example.com/pic.jpg
```
````

The info string `wishlist` is deliberate: a bare ```yaml block would collide with
YAML that notes may contain for unrelated reasons. This makes the contract
unambiguous to both the parser and a human reading the note.

### 5.2 Parse rules

- The **first** fenced block whose info string is `wishlist` is the override
  block. Any later such block is ignored.
- The block may appear anywhere in the notes.
- Parsed as YAML into a map of string → string, int, or float.

### 5.3 Failure is never fatal

Per SPEC §7.1, a bad override block means the item renders at Tier 0/1, with a
warning on stderr. It never drops the item and never aborts the build.

| Condition | Behavior |
|---|---|
| Malformed YAML | stderr warning `tier2: parse error in <url>: …`; ignore block |
| Unknown key | stderr warning `tier2: unknown key "<k>" in <url>`; ignore key |
| Wrong value type | stderr warning; ignore that key |
| Empty block | No-op, no warning |

### 5.4 Recognized keys

| Key | Type | Effect in v0 |
|---|---|---|
| `store` | string | Collection display label (§4.3) |
| `priority` | int | Sort weight; **1 is highest**. Absent = lowest |
| `price` | float | Rendered as text if `currency` is also present; otherwise ignored with a warning |
| `currency` | string | ISO 4217 code, rendered alongside `price` |
| `variant` | string | Rendered as a subtitle line |
| `image` | string (URL) | **Accepted and ignored in v0.** Warn once that images are not yet rendered |

`image` is the one place where an accepted-but-unused key is justified: it is in
the spec, humans will type it, and silently rejecting it would be worse than
accepting it and saying so.

---

## 6. Linkding access

### 6.1 Fetching

Use the copied client's bookmark listing. Two requirements that must not be
sacrificed for convenience:

1. **Tag filtering happens server-side.** The `q` parameter must carry the tag
   filter. Never fetch the most recent N bookmarks and filter client-side — that
   is the "Star Trek: Remember Me" defect described in SPEC §6.1, where the list
   silently starves as unrelated bookmarks accumulate.
2. **Paginate to completion.** Follow `next`/offset until exhausted. Do not
   assume a single page.

Confirm the exact query-parameter syntax against the instance with one manual
`curl` before writing the client call. Do not guess it from memory.

### 6.2 Archived bookmarks

Excluded from output by default. `--include-archived` includes them, and when it
does, their cards carry a visible "archived" marker — the flag exists for
debugging, not for producing a public page full of dead wants.

---

## 7. Manifest

Written to `<out>/manifest.json`. UTF-8, two-space indent, trailing newline.

```json
{
  "schema_version": 1,
  "generated_at": "2026-11-21T12:00:00Z",
  "source": {
    "tag": "wishlist",
    "base_url": "https://wishlist.example.com/"
  },
  "items": [
    {
      "id": "a1b2c3d4e5f6",
      "url": "https://www.sevensisterspdx.com/",
      "title": "Seven Sisters",
      "description": "",
      "notes": "Prose from the bookmark, verbatim.",
      "tags": ["wishlist", "wishlist:shop"],
      "facets": ["shop"],
      "store_key": "sevensisterspdx.com",
      "store_label": "Seven Sisters",
      "added_at": "2026-11-01T09:14:22Z",
      "modified_at": "2026-11-01T09:14:22Z",
      "archived": false,
      "overrides": {
        "store": "Seven Sisters",
        "priority": 1,
        "price": 42.0,
        "currency": "USD",
        "variant": "small, green"
      }
    }
  ],
  "stores": [
    {
      "key": "sevensisterspdx.com",
      "label": "Seven Sisters",
      "path": "/store/sevensisterspdx.com/",
      "item_ids": ["a1b2c3d4e5f6"]
    }
  ],
  "facets": [
    { "name": "shop", "path": "/facet/shop/", "item_ids": ["a1b2c3d4e5f6"] }
  ]
}
```

Rules:

- `overrides` contains only keys present in the Tier 2 block. Omit the object
  entirely when empty.
- `facets` derives from tags matching `wishlist:<name>`. The root tag `wishlist`
  is not a facet. Nested values (`wishlist:book:fiction`) yield facet `book` and
  keep the full tag in `tags`.
- **The manifest contains no lint data.** Lint is stdout-only per SPEC §7.2; the
  manifest is a published artifact and must not carry internal opinions about the
  data's quality.
- Never include the API token or any credential in `source`.

---

## 8. Rendering

`html/template`, embedded. No JavaScript. One CSS file, hand-written, no
framework, no build step.

### 8.1 Ordering

Defined once, used everywhere so that §4.3's "first item wins" is deterministic:

1. Items with a `priority` override, ascending by priority (1 first).
2. Then items without one, by `added_at` descending.
3. Finally, tie-break by `id` ascending.

### 8.2 Pages

**Index** (`/index.html`) — every item as a card, grouped by store collection.
One section per store, ordered by store label.

**Store page** (`/store/<key>/index.html`) — cards for that store, the store label
as the heading.

**Facet page** (`/facet/<name>/index.html`) — cards for that facet.

### 8.3 The card

Minimum viable card, which is also the *maximum* for v0:

- Title. If the bookmark has no title, fall back to the URL's path (or the
  hostname if the path is empty).
- Link to the source URL.
- Store label.
- `variant`, if present.
- `price` + `currency`, if both present.
- Notes, collapsed to a short excerpt or omitted.

No image element in v0. No price element when no price exists — absence is
rendered as absence, never as "unknown", "$0", or a placeholder.

---

## 9. Lint

Per SPEC §7.2. A separate pass over the built item set, printed to stdout after
the build, before the summary line.

### 9.1 Rules and codes

| Code | Trigger |
|---|---|
| `no_facet` | Item carries no `wishlist:<name>` tag |
| `shallow_path` | URL path is empty, `/`, or a single segment |
| `title_matches_host` | Normalized title equals the store key or hostname |
| `unparseable_url` | URL failed to parse for store-key derivation |

`no_product_signals` and `scrape_failed` from the spec's table are **not
implementable in v0**, since v0 never fetches target pages. Do not implement stubs
for them.

### 9.2 Output format

Text (default):

```
lint: 4 item(s) look off
  a1b2c3d4e5f6  https://www.sevensisterspdx.com/
    shallow_path        consider: is this a store, not a want?
  b2c3d4e5f6a1  https://example.com/a-book
    no_facet            consider: retag wishlist:book
```

JSON (`--lint-format=json`): an array of objects with `id`, `url`, `codes`, and
`suggestions`. Still stdout, still not written to disk, still not in the manifest.

### 9.3 Boundaries

- Lint **never** suppresses an item. A flagged item renders identically to any
  other.
- Lint **never** writes to Linkding. It prints suggestions; the human acts.
- No file is written. Not in `--out`, not in `tmp/`, not anywhere. This is the
  sidecar prohibition (SPEC §7.2).

---

## 10. Acceptance criteria

The build is done when all of the following pass.

### 10.1 Behavioral

1. With `LINKDING_URL` pointing at an unreachable host, the build exits 1 with a
   clear message and writes no partial output.
2. Against the fixture set in `testdata/`, the build exits 0 and produces
   `index.html`, one page per store, one page per facet, and `manifest.json`.
3. **Network isolation test:** run the build with outbound network access blocked
   except for the Linkding host. It must succeed. This proves no target-page
   fetching crept in.
4. A bookmark with only the `wishlist` tag and a title renders a card at
   `/index.html`.
5. A bookmark with only the `wishlist` tag and an empty title renders a card using
   a URL-derived title.
6. A bookmark whose notes contain a malformed `wishlist` fenced block renders as a
   normal item and emits one stderr warning.
7. A bookmark with `wishlist:book:fiction` appears on `/facet/book/`, carries both
   tags in the manifest, and yields facet `book`.
8. Two bookmarks on the same host produce one store page containing both.
9. Two bookmarks on the same host, one carrying `store: Seven Sisters`, produce a
   store page headed "Seven Sisters".
10. An archived bookmark is absent by default and present with
    `--include-archived`.
11. The lint report appears on stdout and `manifest.json` contains no lint data.
12. **ID stability:** run the build twice against the same fixtures. The two
    manifests are byte-identical except for `generated_at`.

### 10.2 Tests

- `go test ./...` passes.
- Table-driven unit tests for `internal/identity` (ID and store key), including
  the malformed-URL case.
- Table-driven unit tests for `internal/tier2`: valid block, malformed YAML,
  unknown key, wrong type, no block, two blocks (first wins).
- Unit tests for each lint rule: fires and does not fire.
- Golden-file tests for `manifest.json` and one store page against `testdata/`.

### 10.3 Fixtures

Commit a hand-written `testdata/bookmarks.json` shaped like a real Linkding API
response, covering every case in §10.1. Add a recorded-and-sanitized response
alongside it if convenient, but hand-written is authoritative for tests — recorded
data drifts and makes failures harder to read.

---

## 11. Suggested build order

1. `internal/model` + `internal/identity` with tests. Identity is the foundation;
   getting it wrong later is expensive because observations and claims key on it.
2. `internal/linkding` copied and trimmed, against fixtures only — no live calls.
3. `internal/tier2` with tests.
4. `internal/cluster` + `internal/manifest` with a golden test.
5. `internal/lint` with tests.
6. `internal/render` last, once the manifest is trustworthy.
7. Wire the CLI, then run the acceptance list end to end against the live
   instance.

---

## 12. Open questions carried forward

These do not block v0 and must not be guessed at implementation time:

1. **Fragment behavior** (SPEC §4.7.1) — unblocks nothing in v0; v0 keys on the
   raw URL either way. Run the test before v1 adds an observation store.
2. **Scraper politeness specifics** (SPEC §10.3) — needed before v1.
3. **Whether the lint summary line is worth keeping** — v0 includes it; drop it if
   it proves noisy.
