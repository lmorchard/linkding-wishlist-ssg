# linkding-wishlist-ssg

Render bookmarks tagged `wishlist` in a [Linkding](https://linkding.link/)
instance as a static wishlist site.

This is v0.1: no target-page scraping, no prices beyond Tier 2 overrides, no
client-side search, no gift claims. It reads Linkding, renders HTML, and emits a
JSON manifest. See [`docs/SPEC.md`](docs/SPEC.md) for the design and
[`docs/IMPLEMENTATION-v0.md`](docs/IMPLEMENTATION-v0.md) for the v0 contract.

## Requirements

- Go 1.22+
- A Linkding instance and API token

## Build

```sh
make build
```

## Run

```sh
./linkding-wishlist-ssg build --out ./build
```

The output directory is created if absent and wiped if present. It contains:

```
index.html
manifest.json
assets/style.css
stores/index.html
archive/<year>/index.html
archive/<year>/<month>/index.html
store/<store-key>/index.html
facet/<facet-name>/index.html
```

The front page is a chronological stream: items are ordered by `added_at`
descending and grouped under a heading per day, newest first. Items tagged
`wishlist:featured` render in their own block above the stream and are removed
from it, so nothing appears twice.

The front page covers a rolling 12-month window by default; archive, store, and
tag pages always cover full history. Runs of consecutive items from one store can
collapse behind `<details>`, but the default is off: every run renders expanded.
At 1,580 items the longest run in the dataset is 8, so folding hides content
without buying much.

`/stores/` lists every store by item count, descending. `/tags/` lists every tag
meeting the page threshold. The archive pages use the same day-grouped rendering
as the front page. No JavaScript is emitted.

## Configuration

Credentials come from the process environment or an optional `.env` file in
the working directory. The token is never accepted as a flag.

| Variable | Meaning |
|---|---|
| `LINKDING_URL` | Linkding base URL, e.g. `https://links.example.com` |
| `LINKDING_TOKEN` | API token from Linkding **Settings → Integrations** |

Export them:

```sh
export LINKDING_URL="https://links.example.com"
export LINKDING_TOKEN="..."
```

Or put them in `.env`:

```sh
LINKDING_URL=https://links.example.com
LINKDING_TOKEN=your-token
```

Existing environment variables take precedence over `.env`; the file is only a
fallback. Blank lines and `#` comments are ignored. An optional `export ` prefix
and single or double quotes around values are accepted. `.env` is gitignored.

## Flags

| Flag | Default | Meaning |
|---|---|---|
| `--out` | `./build` | Output directory |
| `--tag` | `wishlist` | Root selector tag |
| `--base-url` | `/` | Prefix for absolute links |
| `--include-archived` | `false` | Include archived bookmarks (debugging only) |
| `--lint-format` | `text` | `text` or `json`; both go to stdout |
| `--no-lint` | `false` | Suppress the lint report |
| `--index-months` | `12` | Front page reach in months; `0` shows everything |
| `--pinned-tag` | `wishlist:featured` | Items with this tag float above the stream |
| `--tag-page-threshold` | `3` | Minimum items for a tag page; `0` builds every tag |
| `--exclude-tags` | `wishlist,amazon,imported:*` | Tags omitted from the tag index; trailing `:*` matches a prefix |
| `--collapse-threshold` | `0` | Fold same-store runs of this length behind `<details>`; `0` keeps all expanded |

## Option precedence

Every option can be set three ways. Later sources are only consulted when the
earlier ones are silent:

1. **Command-line flag** — an explicitly given flag always wins. A flag left
   alone carries its default without claiming to have been set, so the next
   layers still apply.
2. **Process environment variable**
3. **`.env` file**
4. *(future)* a TOML config file would sit below `.env`

Environment variable names are the flag name, upper-cased, with dashes as
underscores, prefixed `LINKDING_WISHLIST_`:

| Flag | Environment variable |
|---|---|
| `--index-months` | `LINKDING_WISHLIST_INDEX_MONTHS` |
| `--pinned-tag` | `LINKDING_WISHLIST_PINNED_TAG` |
| `--tag-page-threshold` | `LINKDING_WISHLIST_TAG_PAGE_THRESHOLD` |
| `--exclude-tags` | `LINKDING_WISHLIST_EXCLUDE_TAGS` |
| `--collapse-threshold` | `LINKDING_WISHLIST_COLLAPSE_THRESHOLD` |
| `--out` | `LINKDING_WISHLIST_OUT` |
| `--tag` | `LINKDING_WISHLIST_TAG` |
| `--base-url` | `LINKDING_WISHLIST_BASE_URL` |
| `--include-archived` | `LINKDING_WISHLIST_INCLUDE_ARCHIVED` |
| `--lint-format` | `LINKDING_WISHLIST_LINT_FORMAT` |
| `--no-lint` | `LINKDING_WISHLIST_NO_LINT` |

Two behaviors worth knowing:

- **A variable set to an empty string counts as set.** `LINKDING_WISHLIST_PINNED_TAG=`
  clears a `.env` value without editing the file.
- **A bad value warns and falls through** rather than aborting. A stale
  `LINKDING_WISHLIST_INDEX_MONTHS=banana` in `.env` prints a warning and the flag
  default applies, so it cannot break a build you overrode on the command line.

## Tier 2 overrides

A fenced block in a bookmark's notes, with info string `wishlist`:

````
```wishlist
store: Seven Sisters
price: 42.00
currency: USD
priority: 1
variant: small, green
```
````

A malformed block warns on stderr and never drops the item.

## Development

```sh
make check   # gofmt, go vet, go test ./...
```

Golden fixtures live in `testdata/`. Regenerate the manifest and store-page
goldens after intentional output changes:

```sh
go test ./cmd/linkding-wishlist-ssg -run TestGolden -update
```
