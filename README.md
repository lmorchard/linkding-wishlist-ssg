# linkding-wishlist-ssg

Render bookmarks tagged `wishlist` in a [Linkding](https://linkding.link/)
instance as a static wishlist site.

This is v0: no target-page scraping, no prices beyond Tier 2 overrides, no
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
store/<store-key>/index.html
facet/<facet-name>/index.html
```

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
