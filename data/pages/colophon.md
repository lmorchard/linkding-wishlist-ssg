---
title: "Colophon"
---

## Behind the Wishlist

This site is statically generated and hosted entirely on my homelab infrastructure.

- **Data Source:** Managed via bookmarks tagged `wishlist` in my private [linkding](https://linkding.link/) instance.
- **Generator:** Built with [linkding-wishlist-ssg](https://github.com/lmorchard/linkding-wishlist-ssg) written in Go.
- **Scraping & Observation:** Price, stock status, and product imagery are harvested politely using scheduled Go workers and a headless Chromium container over Chrome DevTools Protocol (CDP).
- **Presentation:** Single-page data explorer powered by vanilla JavaScript and pure CSS, supporting live sorting, priority buckets (`P0`–`P5`), price filters, store facet filtering, and responsive table and grid layouts.
- **Hosting:** Served locally on my LAN via systemd and published to the web with Caddy.
