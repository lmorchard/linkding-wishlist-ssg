# Test fixtures

`bookmarks.json` is hand-written and authoritative. It is shaped like one page
of a real Linkding `/api/bookmarks/` response, but deliberately contains an
archived bookmark (`id: 6`) so that one file covers every acceptance case in
`docs/IMPLEMENTATION-v0.md` §10.1.

Tests that emulate the Linkding API split this fixture by `is_archived`: the
active endpoint returns the unarchived results, and `/api/bookmarks/archived/`
returns the archived ones. That mirrors Linkding's own endpoint split.
