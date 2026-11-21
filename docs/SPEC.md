# linkding-wishlist-ssg — Specification

A static site generator that turns a set of [Linkding](https://linkding.link/)
bookmarks into a browsable wishlist, enriched with whatever product data can be
scraped from the linked pages.

**Status:** design draft. No implementation yet.

---

## 1. Why this exists

Amazon wishlists no longer support saving arbitrary external product pages, and
most wishlist apps and sites are unpleasant to use. Meanwhile the collecting
already happens: product pages get bookmarked in a self-hosted Linkding instance
because they looked interesting or desirable.

So the wishlist is not a new place to put things. It is a *rendering* of a tag
search over bookmarks that already exist, plus an enrichment layer that goes and
looks at what is actually on those pages.

The app is therefore small by construction: **collect elsewhere, present here.**

---

## 2. Goals and non-goals

### Goals

- Render every bookmark tagged `wishlist` as a wishlist item.
- Enrich items with pricing, availability, and product metadata where the target
  page cooperates, and degrade cleanly where it does not.
- Group items into store collections automatically, so a shopping trip can be
  planned without cross-referencing anything.
- Produce a fully static site: no server, no database required to serve it.
- Leave a clean seam for a future stateful layer (gift claims) that the static
  build never needs to know about.

### Non-goals

- **Not an inventory or bill of goods.** No subtotals, no per-store tallies, no
  running totals. Items are discrete wants. Store collections exist to organize a
  shopping trip, not to add anything up.
- **Not a marketplace.** Nothing is for sale here.
- **Not a searchable archive.** Pagefind-style full-text search is explicitly out
  of scope; this is a few dozen grouped cards, not thousands of records.
- **Not a Linkding writer.** See §4.5.
- **Not a replacement for vault notes.** Local-shop browsing notes, neighborhood
  wishlists, and "look in the back room at Cargo" stay in the notes where they
  already live.

---

## 3. Relationship to `linkding-ssg`

This project begins as a **remix** of [`linkding-ssg`](https://github.com/lmorchard/linkding-ssg),
which compiles a Linkding archive into a Pagefind-searchable static site. It is a
separate repository with a separate identity, not a mode of that tool.

### 3.1 What is shared vs. new

| Area | Status |
|---|---|
| Linkding API client (auth, pagination, server-side tag filtering) | Shared, copied |
| Bookmark model | Shared, copied |
| Static output writing, asset handling, deploy wiring | Shared, copied |
| SQLite price/observation store | **New** |
| Scraper (JSON-LD / OpenGraph / per-site adapters) | **New** |
| Tier parsing (see §4.4) | **New** |
| Host clustering (see §5) | **New** |
| Lint pass (see §7) | **New** |
| JSON manifest emission | **New** |
| Card and collection layout | **New** |
| Pagefind search | **Dropped** |

The overlap is the boring input half. The new middle is the application.

### 3.2 Fork rules

These exist because "reuse" is the door through which overloading walks back in.

- **Rename immediately.** No shared release process, no `--wishlist` flag on the
  archive tool.
- **Delete what is not needed on day one** — Pagefind, the archive renderer, the
  thumbnail gutter. A fork that keeps everything "just in case" is the overloaded
  app arriving by the back door.
- **Do not plan to track upstream.** A fork expecting to merge from its parent
  drifts into permanent conflict. This repository is the source of truth.
- **Tripwire:** if you find yourself wanting one CLI with `build` and `wishlist`
  subcommands sharing a config file where half the fields are mode-specific, stop.
  That is two apps. Two deploy targets, two cron schedules, two release cadences —
  the coupling cost has already exceeded the shared-code benefit.

### 3.3 The duplicated Linkding client

Copying the API client means every fix — notably the server-side tag filter fix
described in §6.1 — has to be applied twice and will silently rot in one place.

**Do not pre-solve this.** Extract a shared Go module *on the second real
consumer, not in anticipation of it*: a subdirectory module with a local `replace`
makes that cheap when the time comes. Until both consumers exist, the right
interface is unknown, and a premature shared module becomes a negotiation with a
repository nobody is touching.

---

## 4. Data model

### 4.1 Source of truth

The Linkding instance is the source of truth for **intent** — what is wanted, what
category it falls in, what notes have been written about it.

A local SQLite database is the source of truth for **observation** — prices,
availability, fetch timestamps, scrape outcomes, history.

These do not mix. Nothing derived from a scrape is ever written back into
Linkding.

### 4.2 Tags are a query surface, not a data store

The del.icio.us-era instinct was to encode metadata in special tag formats and
semi-structured descriptions. It failed for a structural reason: **tags are a
flat, unordered, untyped set.** There is no key/value, no scoping, and no way to
express a repeatable field.

Therefore:

- **Tags select and coarsely group.** Root selector `wishlist`, plus optional
  low-cardinality facet tags such as `wishlist:book`, `wishlist:kitchen`.
- **No per-item data ever goes into a tag.** No prices, no variants, no IDs.
  Anything with a value belongs in the notes.
- **`is_archived` is free state.** "Got it" and "no longer wanted" are already
  modelled in Linkding. Use it rather than inventing a status field.

### 4.3 One bookmark per want

**Every wishlist item is its own bookmark.** A bookmark is never a bag of
separate items.

This is the decision that makes everything else simple: each item gets its own
tags, its own `is_archived` flag, its own URL, and its own stable identity. There
are no second-class sub-items, no bullet-text-as-identity fragility, and no
nested-item parser to write.

It also makes store clustering possible at all — clustering needs discrete rows
to cluster.

### 4.4 Tiers: graded structure that always degrades

Structure is optional and additive. A reader takes the highest tier present; a
bookmark with none renders fine at tier 0.

| Tier | Lives in | Provides |
|---|---|---|
| **0** | tag + title + URL | A valid card. Guaranteed. Content of the notes is prose. |
| **1** | facet tags | Cheap grouping and filtering |
| **2** | a fenced YAML block in the notes | Per-item overrides: `price`, `variant`, `image`, `priority`, `store` |

Tier 0 is the floor and the promise. **Any bookmark tagged `wishlist` renders,
regardless of structure, scrape result, or URL shape.** See §7.1.

Tier 2 exists as an escape hatch for the cases where derivation fails or the
human simply knows better — a hand-entered price for a shop with no product page,
a display name for a store collection, an explicit priority ordering that
Linkding does not model.

### 4.5 Read-only against Linkding

**The tool reads Linkding. It never writes.**

Hand-authored overrides live in the notes because a human put them there. Derived
observations live in SQLite. A scraper that writes back into bookmarks destroys
the ability to tell your own handwriting from a robot's.

Any future tool that modifies Linkding (bulk retagging, cleanup automation) must
be a separate, explicit, opt-in utility — never a quiet flag on the builder.

### 4.6 Identity

Item ID is derived from the bookmark URL (see §4.7 for fragment handling).

Identity must be stable across builds so that the deferred claims layer (§9) can
key against it. Identity is *not* required to survive arbitrary edits by the user;
see §4.8.

### 4.7 The URL-identity collision

Linkding treats the URL as the identity key: saving a URL that already exists
updates that bookmark rather than creating a second one.

This collides with §4.3 in one real case. Two wants at the same shop, where
neither has a product page of its own — the sardine-head salt bowl and the black
sesame crunchy butter at the same small storefront. Both would have to be the
shop's bare homepage URL, and only one can be.

Routes, in preference order:

1. **Use a real product URL whenever one exists.** This is the normal path and
   needs no trick. Most online wants have one.
2. **Otherwise, use a URL fragment as a pseudo-key** —
   `https://www.sevensisterspdx.com/#sardine-head-salt-bowl`. A different string,
   so Linkding stores it as a separate bookmark, and the fragment survives as the
   distinguishing identity. This is an honest hack: following the link lands on
   the shop page, not the item.
   **Must be verified against the live instance** — some bookmark tools normalize
   fragments away. Do this before depending on the mechanism.
3. **Decline to represent it.** If a want cannot be pinned to a URL even loosely,
   it does not belong in this application. Local browsing wants stay in vault
   notes. Not everything has to move.

Consequences of fragments:

- **Fragments are identity, not fetch targets.** Strip the fragment before making
  a request; keep it for keying.
- **The fragment must participate in the item ID hash**, or two
  fragment-differentiated wants collide.
- **Cache by URL minus fragment**, so several wants at one shop cost one page
  fetch.

---

## 5. Store collections

Items are grouped into store collections automatically, by derived key. This is
the payoff for §4.3.

**Purpose, stated narrowly:** organize a shopping trip. Which shops hold how many
of my wants, so a walk down one street can cover several of them. *Not* to total
anything up.

### 5.1 Mechanism

Group by the hostname of the bookmark URL. Emit a collection page per host
(`/store/<host>/`). Recomputed on every build.

No tag, no prefix convention, no stored directory.

### 5.2 Host is a good default key, but a bad identity

Grouping by hostname diverges from grouping by *store* in at least four ways:

- **One host, many stores.** `bookshop.org` hosts hundreds of independent
  bookstores at distinct paths. `amazon.com` is a marketplace. Host grouping
  buckets unrelated things together.
- **One store, many hosts.** A shop with both a Shopify domain and an Etsy
  storefront yields two collections for one store.
- **A host that is not a store at all.** Manufacturer pages, review sites, a
  project's own site ("want this when it ships").
- **Several retailers, one want.** See §5.5.

### 5.3 Derived key, human label

Treat the derived host as a *label*, not a taxonomy.

If a collection heading should read "Seven Sisters" instead of a bare hostname,
that is a `store:` value in the Tier 2 override block on any one of its items. The
first item in the group carrying a name wins for the group.

Hand-authored, stored in Linkding, no new file.

### 5.4 Do not build a store directory

The moment grouping produces an artifact you curate — merge two hosts, correct a
name, record an address — you have built a sidecar file with extra steps and a
synchronization problem.

**Derived views stay derived.** Regenerate on every build; hand overrides live on
the bookmarks.

Note the distinction: *building no new sidecar* is not the same as *refusing to
read things that already exist*. The vault already contains store names,
addresses, and neighborhood groupings for several Portland shops, maintained for
other reasons. A later version could read that as a store directory and gain a
"shopping trip by neighborhood" view at near-zero cost. That is a real new
dependency and out of scope for v1, but it is the one case where reuse is
genuinely free, and worth remembering before inventing a store table.

### 5.5 Deferred: multi-retailer wants

Hostname grouping fails when one want is purchasable in several places — a
specific book available from both a large retailer and an independent shop.

The eventual answer is a `wishlist:want:<slug>` tag tying several URLs to one
want, rendered as one card with multiple purchase options.

**Do not build this until a real instance exists.**

### 5.6 Empty collections

When the last item at a shop is archived or retagged, its collection page
disappears. That is acceptable: let it go quietly, and let the lint pass (§7)
mention it.

Do not emit a stub "nothing here right now" page. On a personal site, that is
clutter in someone's bookmarks.

---

## 6. Enrichment

### 6.1 Fetching

Fetch bookmarked pages over HTTP and extract product data. Extraction order, first
success wins:

1. **JSON-LD** `Product` / `Offer` — structured, reliable where present.
2. **OpenGraph / meta tags** — `og:title`, `og:image`, `product:price:amount`,
   and friends.
3. **Per-site adapters** — hand-written selectors for shops that matter and
   cooperate.
4. **Tier 2 override** — the human just says what the price is.

Shopify-backed shops are generally generous (structured data, `/products.json`).
Large retailers build walls, and their terms of service are not an invitation.

**Politeness is not optional.** Bookmarks concentrate traffic: N items at one shop
means N URLs at one domain.

- Cache by URL minus fragment.
- Rate-limit per domain.
- Respect `robots.txt`.
- Set a real User-Agent that identifies the tool.

### 6.2 Observation store

SQLite holds price and availability observations, keyed by item ID, with
timestamps. This is what makes price *history* possible rather than a single
current value.

The static build reads the latest observation. Nothing about the store is
required to serve the site.

### 6.3 Missing data is normal

A want with no price is a normal want, not an error. Much of a real wishlist is
handmade goods, local storefronts, and one-off objects with nothing to poll.

The build must never fail because a page did not yield product data. See §7.1.

---

## 7. Resilience and lint

The user maintains the `wishlist` tag contract by discipline, and the data will
drift because humans drift. The system handles that with two *separate*
mechanisms. Keeping them separate is the load-bearing part of this section.

### 7.1 Rendering fails open — always

**Every bookmark tagged `wishlist` renders as an item. Unconditionally.**

No absence of a scrape result, no unusual URL shape, no missing metadata, no host
oddity is ever grounds for dropping or hiding an item. Tier 0 guarantees this: tag,
title, and URL render a valid card on their own.

The build must not hard-fail on scrape errors — 404, bot wall, timeout, malformed
HTML. A missing price is an expected state.

Put plainly: **the site is never wrong about what is wanted, only occasionally
uninformed about it.**

### 7.2 Lint is a separate pass, and it is not public

A build-time report of items whose *fit* looks doubtful, each with a reason code:

| Signal | Why it suggests misfit |
|---|---|
| Scrape returned no product signals | Probably not a product page |
| URL path is a bare or shallow root (`/`, `/shop`) | Probably a store homepage, not an item |
| No facet tag at all | Unfiled; possibly a stray tag |
| Scrape failed outright (404 / blocked / timeout) | Dead link, or a site that will never cooperate |
| Host looks like a manufacturer, docs, or project site | "Want this when it ships" is not a purchasable item |
| Title is approximately the hostname | Suggests a shop bookmarked, not a thing |

These are **heuristics, not rules.** They suggest. They never suppress. An item
flagged by every signal still renders exactly like any other item.

**Where the report goes: stdout at build time.** Not a file. A report written into
`tmp/` is the sidecar the user has already said they would neglect, wearing a
different hat — it accumulates, goes stale, and is then ignored *silently* instead
of being ignored in scrollback.

If scrollback turns out to be insufficient, the cheapest improvement is a single
summary line at the very *end* of the build (`lint: 4 items look off`). Not a
file, not a page, not a dashboard.

### 7.3 Lint does not remediate

"It would be nice to have tools to assist cleanup" means: the lint pass, plus the
suggested action printed inline on each flagged line (`consider: retag store`,
`consider: delete — 404`).

The tool does **not** perform the retag or the delete. That would break the
read-only boundary in §4.5 and blur intent from observation the moment a robot's
opinion lands in the bookmarks.

If a write-capable cleanup tool is ever wanted, it is its own explicit opt-in
utility, reviewed on its own terms.

---

## 8. Output

### 8.1 Site

- An index of all items.
- One page per store collection (§5).
- Facet-based groupings as they emerge from tier 1 tags.

Layout is a card per item: image if available, title, store label, price if
known. Items with no image and no price are still complete cards.

No client-side search.

### 8.2 JSON manifest

The build emits a machine-readable manifest of all items with their stable IDs,
store keys, and known attributes.

This is the seam for the deferred stateful layer. It is cheap to produce and
costs the static build nothing.

---

## 9. Deferred: gift claims (stateful layer)

The one feature aimed at other people: a visitor can mark an item as reserved,
so that two people do not buy the same gift. **Not** a storefront — nothing is
being sold.

Design constraint: this must be drapeable over the static site as progressive
enhancement, without the static build ever needing to know it exists.

- A small KV service keys claims against **item IDs** from the manifest (§8.2).
- A progressive-enhancement script overlays "reserved" badges on items.
- With JavaScript off, or the service down, the static site is unchanged and
  fully functional.

**Privacy:** the fact that an item is reserved must be visible to everyone — that
is the entire point, and hiding it causes duplicate gifts. The **identity** of the
person who reserved it must never appear in the public overlay.

Out of scope for v1. Not to be stubbed, not to be scaffolded, not to be
"prepared for" beyond emitting the manifest.

---

## 10. Open questions

1. **Does Linkding preserve URL fragments, or normalize them away?** Must be
   tested against the live instance before §4.7 route 2 can be relied upon. Cheap
   test: save one fragment URL, read it back through the API, compare strings.
2. **Ordering.** Linkding has no priority field. Start with facet plus
   `date_added`; add explicit ordering only through the Tier 2 override.
3. **Scraper politeness specifics** — per-domain rate limits, retry policy, and
   how to handle sites that are persistently hostile.
4. **Lint summary line** — is a one-line tally at end of build worth it, or is
   scrollback enough?

---

## 11. Decision log

| Date | Decision |
|---|---|
| 2026-11-21 | Static first; no backend for v1 |
| 2026-11-21 | Buyer-facing feature is gift claims only, not selling one's own goods |
| 2026-11-21 | Gifting deferred to a progressive-enhancement layer |
| 2026-11-21 | Remix `linkding-ssg` into a separate repository rather than adding a mode |
| 2026-11-21 | No sidecar files; editorial data lives in Linkding notes |
| 2026-11-21 | One bookmark per want; shop-with-bullet-list design dropped |
| 2026-11-21 | Store collections derived per build, never curated or stored |
| 2026-11-21 | No subtotals or tallies; items are discrete, not an inventory |
| 2026-11-21 | `wishlist` always means item; resilience is the system's job, discipline is the user's |
| 2026-11-21 | Rendering fails open; lint suggests but never suppresses |
| 2026-11-21 | Lint reports to stdout, never to a file; lint never writes to Linkding |
